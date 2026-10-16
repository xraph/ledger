// Package telemetry is the tracing and metrics plumbing the engine and the
// plugin registry share. Spans go to the global OpenTelemetry tracer
// provider, which forge.New installs. Metrics go to a go-utils
// metrics.Metrics the caller hands over, and a nil one records nothing.
package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	gometrics "github.com/xraph/go-utils/metrics"
)

// InstrumentationName is the tracer name on every span ledger starts.
const InstrumentationName = "github.com/xraph/ledger"

// Tracer returns ledger's tracer from the global provider. It is looked up on
// every call rather than kept in a package variable, so a provider installed
// after this package loaded (forge.New, or a test) is the one that records.
func Tracer() trace.Tracer { return otel.Tracer(InstrumentationName) }

// Start starts an internal span.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, append(opts, trace.WithSpanKind(trace.SpanKindInternal))...)
}

// StartRoot starts an internal span that begins its own trace, for
// background work whose context may still carry the span of whatever
// started the worker.
func StartRoot(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Start(ctx, name, append(opts, trace.WithNewRoot())...)
}

// StartChild starts an internal span only when ctx already carries one, so a
// hot call made outside any trace does not begin a trace of its own. Without
// a parent it returns ctx and a span that records nothing.
func StartChild(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx, trace.SpanFromContext(ctx)
	}
	return Start(ctx, name, opts...)
}

// Detach returns ctx with no span in it, keeping its values, deadline and
// cancellation. A worker started from a traced context uses it so that the
// queries it runs while idle hang off no trace.
func Detach(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(ctx, trace.SpanContext{})
}

// Fail marks span as failed. The status description and the exception event
// carry only typ, the error's class, never its message: driver and provider
// messages quote the values they failed on.
func Fail(span trace.Span, typ string) {
	span.SetStatus(codes.Error, typ)
	span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", typ)))
}

// TypeOf names an error by the Go type of the innermost error in its chain.
// Where a chain branches (errors.Join, or fmt.Errorf with two %w verbs) it
// follows the first branch.
func TypeOf(err error) string {
	for {
		switch e := err.(type) { //nolint:errorlint // walking the chain by hand is the point
		case interface{ Unwrap() error }:
			next := e.Unwrap()
			if next == nil {
				return fmt.Sprintf("%T", err)
			}
			err = next
		case interface{ Unwrap() []error }:
			errs := e.Unwrap()
			if len(errs) == 0 || errs[0] == nil {
				return fmt.Sprintf("%T", err)
			}
			err = errs[0]
		default:
			return fmt.Sprintf("%T", err)
		}
	}
}

// maxCachedSeries bounds the per-vec cache of children. Every label ledger
// uses is a closed set, so it is never reached; past it a child still
// records, through the registry each time.
const maxCachedSeries = 1024

// vec is a metric family with label names, caching a child per label set.
type vec[T any] struct {
	labels []string
	build  func(map[string]string) T

	mu       sync.RWMutex
	children map[string]T
}

func newVec[T any](labels []string, build func(map[string]string) T) *vec[T] {
	return &vec[T]{labels: labels, build: build, children: map[string]T{}}
}

// with returns the child for values, given in the order the labels were
// declared. ok is false when the count is wrong, which is a bug in ledger.
func (v *vec[T]) with(values []string) (child T, ok bool) {
	if len(values) != len(v.labels) {
		return child, false
	}
	var buf [96]byte
	key := buf[:0]
	for i, s := range values {
		if i > 0 {
			key = append(key, 0xff)
		}
		key = append(key, s...)
	}

	v.mu.RLock()
	child, ok = v.children[string(key)]
	v.mu.RUnlock()
	if ok {
		return child, true
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if child, ok = v.children[string(key)]; ok {
		return child, true
	}
	l := make(map[string]string, len(values))
	for i, name := range v.labels {
		l[name] = values[i]
	}
	child = v.build(l)
	if len(v.children) < maxCachedSeries {
		v.children[string(key)] = child
	}
	return child, true
}

// CounterVec is a labeled counter. A nil *CounterVec records nothing.
type CounterVec struct{ v *vec[gometrics.Counter] }

// NewCounterVec declares a counter on m. It returns nil when m is nil.
func NewCounterVec(m gometrics.Metrics, name, help string, labels ...string) *CounterVec {
	if m == nil {
		return nil
	}
	return &CounterVec{v: newVec(labels, func(l map[string]string) gometrics.Counter {
		return m.Counter(name, gometrics.WithDescription(help), gometrics.WithLabels(l))
	})}
}

// Add adds n to the child named by values.
func (c *CounterVec) Add(n float64, values ...string) {
	if c == nil {
		return
	}
	if child, ok := c.v.with(values); ok {
		child.Add(n)
	}
}

// Inc adds one to the child named by values.
func (c *CounterVec) Inc(values ...string) { c.Add(1, values...) }

// HistogramVec is a labeled histogram. A nil *HistogramVec records nothing.
type HistogramVec struct{ v *vec[gometrics.Histogram] }

// NewHistogramVec declares a histogram in seconds on m. It returns nil when m
// is nil.
func NewHistogramVec(m gometrics.Metrics, name, help string, buckets []float64, labels ...string) *HistogramVec {
	if m == nil {
		return nil
	}
	return &HistogramVec{v: newVec(labels, func(l map[string]string) gometrics.Histogram {
		return m.Histogram(name,
			gometrics.WithDescription(help),
			gometrics.WithUnit("s"),
			gometrics.WithBuckets(buckets...),
			gometrics.WithLabels(l),
		)
	})}
}

// ObserveSince records the seconds since start on the child named by values.
func (h *HistogramVec) ObserveSince(start time.Time, values ...string) {
	if h == nil {
		return
	}
	if child, ok := h.v.with(values); ok {
		child.Observe(time.Since(start).Seconds())
	}
}
