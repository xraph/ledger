package telemetry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/ledger/internal/telemetry"
	"github.com/xraph/ledger/internal/telemetrytest"
)

func TestStartChildNeedsAParent(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)

	ctx, span := telemetry.StartChild(context.Background(), "check entitlement")
	if span.IsRecording() || trace.SpanContextFromContext(ctx).IsValid() {
		t.Error("a child span started with no parent")
	}
	span.End()

	parentCtx, parent := telemetry.Start(context.Background(), "request")
	_, child := telemetry.StartChild(parentCtx, "check entitlement")
	child.End()
	parent.End()

	got := telemetrytest.Only(t, rec, "check entitlement")
	if got.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("the child span is not under its parent")
	}
}

func TestDetachAndStartRootLeaveTheCallersTrace(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	bootCtx, boot := telemetry.Start(context.Background(), "boot")
	boot.End()

	if trace.SpanContextFromContext(telemetry.Detach(bootCtx)).IsValid() {
		t.Error("Detach kept the span")
	}
	_, root := telemetry.StartRoot(bootCtx, "flush meter batch")
	root.End()
	got := telemetrytest.Only(t, rec, "flush meter batch")
	if got.Parent().IsValid() || got.SpanContext().TraceID() == boot.SpanContext().TraceID() {
		t.Error("StartRoot joined the trace of its context")
	}
}

type wrapped struct{ err error }

func (w wrapped) Error() string { return "wrapped" }
func (w wrapped) Unwrap() error { return w.err }

func TestTypeOf(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("x"), "*errors.errorString"},
		{fmt.Errorf("a: %w", errors.New("x")), "*errors.errorString"},
		{wrapped{}, "telemetry_test.wrapped"},
		{wrapped{err: context.Canceled}, "*errors.errorString"},
		{errors.Join(), "<nil>"},
		{fmt.Errorf("%w and %w", wrapped{}, errors.New("x")), "telemetry_test.wrapped"},
	}
	for _, c := range cases {
		if got := telemetry.TypeOf(c.err); got != c.want {
			t.Errorf("TypeOf(%#v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestVecsCacheAChildPerLabelSet(t *testing.T) {
	sink := telemetrytest.NewSink()
	c := telemetry.NewCounterVec(sink, "ledger_things_total", "Things.", "kind", "result")
	c.Inc("a", "ok")
	c.Add(2, "a", "ok")
	c.Inc("b", "error")
	c.Inc("too few") // a wrong count records nothing
	if sink.CounterCalls != 2 {
		t.Errorf("registered %d children, want one per label set", sink.CounterCalls)
	}
	if got := sink.Value(`ledger_things_total{kind="a",result="ok"}`); got != 3 {
		t.Errorf("a/ok = %v, want 3", got)
	}

	h := telemetry.NewHistogramVec(sink, "ledger_thing_duration_seconds", "Thing.", []float64{0.1, 1}, "result")
	h.ObserveSince(time.Now(), "ok")
	h.ObserveSince(time.Now(), "ok")
	if got := sink.Observations(`ledger_thing_duration_seconds{result="ok"}`); got != 2 {
		t.Errorf("observations = %d, want 2", got)
	}
}

func TestNilVecsRecordNothing(t *testing.T) {
	var c *telemetry.CounterVec
	var h *telemetry.HistogramVec
	c.Inc("x")
	h.ObserveSince(time.Now(), "x")
	if telemetry.NewCounterVec(nil, "n", "h") != nil || telemetry.NewHistogramVec(nil, "n", "h", nil) != nil {
		t.Error("a vec on no sink is not nil")
	}
}
