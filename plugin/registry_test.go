package plugin

import (
	"context"
	"testing"
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
