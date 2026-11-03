package ledger_test

import (
	"context"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
)

// startLedger starts a ledger on s that flushes every few milliseconds and
// stops it when the test ends.
func startLedger(t *testing.T, s *memory.Store, opts ...ledger.Option) *ledger.Ledger {
	t.Helper()
	opts = append([]ledger.Option{
		ledger.WithMeterConfig(100, 5*time.Millisecond),
		ledger.WithLifecycleInterval(0),
	}, opts...)
	l := ledger.New(s, opts...)
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = l.Stop() })
	return l
}

// aggregator is the part of a store the usage assertions read.
type aggregator interface {
	Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error)
}

// usageOf totals a feature's usage for one tenant and app.
func usageOf(t *testing.T, s aggregator, tenantID, appID, feature string) int64 {
	t.Helper()
	n, err := s.Aggregate(context.Background(), tenantID, appID, feature, plan.PeriodMonthly)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	return n
}

func TestWithTenantScopesMeter(t *testing.T) {
	s := memory.New()
	l := startLedger(t, s)

	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	if err := l.MeterWithKey(ctx, "f", 3, "k1"); err != nil {
		t.Fatalf("MeterWithKey: %v", err)
	}
	waitFor(t, "the flush", func() bool { return usageOf(t, s, "t1", "a1", "f") == 3 })

	if got := usageOf(t, s, "t2", "a1", "f"); got != 0 {
		t.Errorf("t2 usage = %d, want 0", got)
	}
	if got := usageOf(t, s, "t1", "a2", "f"); got != 0 {
		t.Errorf("t1 in another app usage = %d, want 0", got)
	}
}

func TestWithTenantBeatsLegacyKeys(t *testing.T) {
	s := memory.New()
	l := startLedger(t, s)

	ctx := context.WithValue(context.Background(), "tenant_id", "old")
	ctx = ledger.WithTenant(ctx, "t1", "a1")
	if err := l.Meter(ctx, "f", 2); err != nil {
		t.Fatalf("Meter: %v", err)
	}
	waitFor(t, "the flush", func() bool { return usageOf(t, s, "t1", "a1", "f") == 2 })
	if got := usageOf(t, s, "old", "a1", "f"); got != 0 {
		t.Errorf("legacy tenant usage = %d, want 0: the typed scope wins", got)
	}
}

func TestLegacyStringKeysStillWork(t *testing.T) {
	s := memory.New()
	l := startLedger(t, s)

	// Existing callers write plain string keys, and the keys stay strings on
	// purpose: that is the contract. (staticcheck is off in test files, so
	// there is no nolint to carry.)
	ctx := context.WithValue(context.Background(), "tenant_id", "t1")
	ctx = context.WithValue(ctx, "app_id", "a1")
	if err := l.Meter(ctx, "f", 4); err != nil {
		t.Fatalf("Meter: %v", err)
	}
	waitFor(t, "the flush", func() bool { return usageOf(t, s, "t1", "a1", "f") == 4 })
}

func TestMeterWithoutScopeIsInvalid(t *testing.T) {
	l := ledger.New(memory.New())
	if err := l.Meter(context.Background(), "f", 1); err == nil {
		t.Error("Meter with no tenant: want ErrInvalidInput, got nil")
	}
	if err := l.MeterWithKey(ledger.WithTenant(context.Background(), "t1", ""), "f", 1, "k"); err == nil {
		t.Error("MeterWithKey with no app: want ErrInvalidInput, got nil")
	}
}
