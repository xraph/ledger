package plugin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xraph/ledger/internal/telemetrytest"
)

// stubAggregator satisfies UsageAggregator exactly; see plugin.go.
type stubAggregator struct{ name string }

func (s *stubAggregator) Name() string           { return s.name }
func (s *stubAggregator) AggregatorName() string { return s.name }
func (s *stubAggregator) Aggregate(_ context.Context, events []interface{}) (int64, error) {
	return int64(len(events)), nil
}

var _ UsageAggregator = (*stubAggregator)(nil)

func TestGetUsageAggregatorByName(t *testing.T) {
	r := NewRegistry()

	if got := r.GetUsageAggregator("absent"); got != nil {
		t.Errorf("got %v for an unregistered name, want nil", got)
	}

	if err := r.Register(&stubAggregator{name: "count"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := r.GetUsageAggregator("count"); got == nil {
		t.Error("got nil for a registered aggregator, want it returned by name")
	}
}

// lifecycleHooks implements the four lifecycle hooks and counts calls.
type lifecycleHooks struct{ calls map[string]int }

func (h *lifecycleHooks) Name() string { return "lifecycle-hooks" }
func (h *lifecycleHooks) OnSubscriptionCancelScheduled(context.Context, interface{}) error {
	h.calls["scheduled"]++
	return nil
}
func (h *lifecycleHooks) OnSubscriptionTrialEnded(context.Context, interface{}) error {
	h.calls["trial"]++
	return nil
}
func (h *lifecycleHooks) OnSubscriptionRenewed(context.Context, interface{}) error {
	h.calls["renewed"]++
	return nil
}
func (h *lifecycleHooks) OnInvoicePastDue(context.Context, interface{}) error {
	h.calls["past_due"]++
	return nil
}

func TestRegistryDispatchesTheLifecycleHooks(t *testing.T) {
	r := NewRegistry()
	h := &lifecycleHooks{calls: map[string]int{}}
	if err := r.Register(h); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ctx := context.Background()
	r.EmitSubscriptionCancelScheduled(ctx, nil)
	r.EmitSubscriptionTrialEnded(ctx, nil)
	r.EmitSubscriptionRenewed(ctx, nil)
	r.EmitInvoicePastDue(ctx, nil)
	for _, k := range []string{"scheduled", "trial", "renewed", "past_due"} {
		if h.calls[k] != 1 {
			t.Errorf("%s: %d calls, want 1", k, h.calls[k])
		}
	}
}

// failingHooks fails OnPlanCreated in words that quote a customer, and
// blocks OnPlanArchived until release is closed.
type failingHooks struct{ release chan struct{} }

func (h *failingHooks) Name() string { return "failing-hooks" }
func (h *failingHooks) OnPlanCreated(context.Context, interface{}) error {
	return errors.New("alice@example.com is not a customer")
}

func (h *failingHooks) OnPlanArchived(context.Context, string) error {
	<-h.release
	return nil
}

func TestFailedHooksAreCountedByHookAndReason(t *testing.T) {
	sink := telemetrytest.NewSink()
	r := NewRegistry().WithMetrics(sink)
	h := &failingHooks{release: make(chan struct{})}
	defer close(h.release)
	if err := r.Register(h); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r.EmitPlanCreated(context.Background(), nil)
	// The caller's context is already over, so the registry stops waiting on
	// the hook at once.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	r.EmitPlanArchived(canceled, "plan_1")

	if got := sink.Value(`ledger_plugin_hook_failures_total{hook="OnPlanCreated",reason="error"}`); got != 1 {
		t.Errorf("OnPlanCreated errors = %v, want 1 (series: %v)", got, sink.Series())
	}
	if got := sink.Value(`ledger_plugin_hook_failures_total{hook="OnPlanArchived",reason="canceled"}`); got != 1 {
		t.Errorf("OnPlanArchived cancels = %v, want 1 (series: %v)", got, sink.Series())
	}
}

func TestASlowHookTimesOutAndIsCounted(t *testing.T) {
	sink := telemetrytest.NewSink()
	r := NewRegistry().WithMetrics(sink)
	r.hookTimeout = 10 * time.Millisecond
	h := &failingHooks{release: make(chan struct{})}
	defer close(h.release)
	if err := r.Register(h); err != nil {
		t.Fatalf("Register: %v", err)
	}

	reason, err := r.callWithTimeout(context.Background(), h.Name(), func() error {
		return h.OnPlanArchived(context.Background(), "plan_1")
	})
	if reason != "timeout" || !errors.Is(err, errHookTimeout) || err.Error() != "plugin timeout: failing-hooks" {
		t.Errorf("a hook past its timeout: reason %q, error %v", reason, err)
	}

	r.EmitPlanArchived(context.Background(), "plan_1")
	if got := sink.Value(`ledger_plugin_hook_failures_total{hook="OnPlanArchived",reason="timeout"}`); got != 1 {
		t.Errorf("OnPlanArchived timeouts = %v, want 1 (series: %v)", got, sink.Series())
	}
}

func TestAHookThatReturnsNilIsNoFailure(t *testing.T) {
	reason, err := NewRegistry().callWithTimeout(context.Background(), "p", func() error { return nil })
	if reason != "" || err != nil {
		t.Errorf("a hook that returned nil: %q, %v", reason, err)
	}
}
