// Package telemetrytest holds what ledger's tests use to see its telemetry: a
// span recorder installed as the global tracer provider, and a metrics sink
// that keeps every series it hands out.
package telemetrytest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	gometrics "github.com/xraph/go-utils/metrics"
)

// RecordSpans installs a span recorder as the global tracer provider until
// the test ends, then puts the previous provider back.
func RecordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shut down the test tracer provider: %v", err)
		}
	})
	return rec
}

// Named returns the ended spans called name, in the order they ended.
func Named(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// Only returns the one ended span called name, and fails the test unless
// there is exactly one.
func Only(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := Named(rec, name)
	if len(spans) != 1 {
		t.Fatalf("want one %q span, got %d (all: %v)", name, len(spans), Names(rec))
	}
	return spans[0]
}

// Names lists the names of the ended spans.
func Names(rec *tracetest.SpanRecorder) []string {
	ended := rec.Ended()
	out := make([]string, len(ended))
	for i, s := range ended {
		out[i] = s.Name()
	}
	return out
}

// Attr returns the value of the attribute key on s.
func Attr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// AssertNoValue fails the test if value appears anywhere on an ended span:
// its name, an attribute, an event or its status.
func AssertNoValue(t *testing.T, rec *tracetest.SpanRecorder, value string) {
	t.Helper()
	for _, s := range rec.Ended() {
		if strings.Contains(s.Name(), value) || strings.Contains(s.Status().Description, value) {
			t.Errorf("span %q carries %q in its name or status", s.Name(), value)
		}
		for _, kv := range s.Attributes() {
			if strings.Contains(kv.Value.String(), value) {
				t.Errorf("span %q carries %q in attribute %s", s.Name(), value, kv.Key)
			}
		}
		for _, ev := range s.Events() {
			if strings.Contains(ev.Name, value) {
				t.Errorf("span %q carries %q in event %q", s.Name(), value, ev.Name)
			}
			for _, kv := range ev.Attributes {
				if strings.Contains(kv.Value.String(), value) {
					t.Errorf("span %q carries %q in event %q attribute %s", s.Name(), value, ev.Name, kv.Key)
				}
			}
		}
	}
}

// Sink is a go-utils metrics registry that keeps every counter and histogram
// it hands out, keyed the way Prometheus prints a series:
// name{key="value",...}, keys sorted.
type Sink struct {
	*gometrics.MockMetrics

	mu         sync.Mutex
	counters   map[string]gometrics.Counter
	histograms map[string]gometrics.Histogram
}

// NewSink returns an empty Sink.
func NewSink() *Sink {
	s := &Sink{
		MockMetrics: gometrics.NewMockMetrics(),
		counters:    map[string]gometrics.Counter{},
		histograms:  map[string]gometrics.Histogram{},
	}
	s.CounterFunc = func(name string, opts ...gometrics.MetricOption) gometrics.Counter {
		s.mu.Lock()
		defer s.mu.Unlock()
		key := seriesKey(name, opts)
		if c, ok := s.counters[key]; ok {
			return c
		}
		c := gometrics.NewCounter(name, opts...)
		s.counters[key] = c
		return c
	}
	s.HistogramFunc = func(name string, opts ...gometrics.MetricOption) gometrics.Histogram {
		s.mu.Lock()
		defer s.mu.Unlock()
		key := seriesKey(name, opts)
		if h, ok := s.histograms[key]; ok {
			return h
		}
		h := gometrics.NewHistogram(name, opts...)
		s.histograms[key] = h
		return h
	}
	return s
}

func seriesKey(name string, opts []gometrics.MetricOption) string {
	var o gometrics.MetricOptions
	for _, opt := range opts {
		opt(&o)
	}
	keys := slices.Sorted(maps.Keys(o.Labels))
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%q", k, o.Labels[k])
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

// Value returns the value of a counter series, zero if it was never created.
func (s *Sink) Value(series string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.counters[series]; ok {
		return c.Value()
	}
	return 0
}

// Observations returns how many values a histogram series has seen, zero if
// it was never created.
func (s *Sink) Observations(series string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.histograms[series]; ok {
		return h.Count()
	}
	return 0
}

// Series lists every series created, sorted.
func (s *Sink) Series() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Collect(maps.Keys(s.counters))
	out = slices.AppendSeq(out, maps.Keys(s.histograms))
	slices.Sort(out)
	return out
}

// AssertOneLabelSet fails the test if two series of one metric carry
// different label keys.
func (s *Sink) AssertOneLabelSet(t *testing.T) {
	t.Helper()
	keysOf := map[string]string{}
	for _, series := range s.Series() {
		name, labels, _ := strings.Cut(series, "{")
		parts := strings.Split(strings.TrimSuffix(labels, "}"), ",")
		keys := make([]string, 0, len(parts))
		for _, part := range parts {
			k, _, _ := strings.Cut(part, "=")
			keys = append(keys, k)
		}
		joined := strings.Join(keys, ",")
		if prev, ok := keysOf[name]; ok && prev != joined {
			t.Errorf("%s has label keys %q and %q", name, prev, joined)
		}
		keysOf[name] = joined
	}
}
