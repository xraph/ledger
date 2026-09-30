package extension

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/store/memory"
)

type migrateCounter struct {
	*memory.Store
	migrations atomic.Int32
}

func (m *migrateCounter) Migrate(ctx context.Context) error {
	m.migrations.Add(1)
	return m.Store.Migrate(ctx)
}

// DisableMigrate used to skip Ledger.Start altogether, and with it the meter
// flusher: metered usage was never written.
func TestDisableMigrateStillStartsTheWorkers(t *testing.T) {
	ctx := context.Background()
	s := &migrateCounter{Store: memory.New()}
	e := New(WithStore(s), WithDisableMigrate(), WithLedgerOption(ledger.WithMeterConfig(1, 5*time.Millisecond)))
	e.config = e.mergeWithDefaults(e.config)
	e.engine = ledger.New(s, e.buildLedgerOpts()...)
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	mctx := context.WithValue(context.WithValue(ctx, "tenant_id", "t1"), "app_id", "app_1")
	if err := e.engine.Meter(mctx, "api_calls", 3); err != nil {
		t.Fatalf("Meter: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		events, err := s.QueryUsage(ctx, "t1", "app_1", meter.QueryOpts{FeatureKey: "api_calls"})
		if err == nil && len(events) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("with DisableMigrate the flusher wrote %d events in 2s, want 1", len(events))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := s.migrations.Load(); n != 0 {
		t.Errorf("Migrate ran %d times with DisableMigrate, want 0", n)
	}
	if err := e.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
