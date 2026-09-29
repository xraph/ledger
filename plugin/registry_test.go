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
