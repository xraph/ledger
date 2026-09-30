package ledger_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/store/memory"
)

// migrateCounter is a memory store that counts its migrations.
type migrateCounter struct {
	*memory.Store
	migrations atomic.Int32
}

func (m *migrateCounter) Migrate(ctx context.Context) error {
	m.migrations.Add(1)
	return m.Store.Migrate(ctx)
}

// meterAndWait meters one event for t1 in app_1 and waits up to two seconds
// for the flusher to write it.
func meterAndWait(t *testing.T, l *ledger.Ledger, s *memory.Store) {
	t.Helper()
	ctx := context.WithValue(context.WithValue(context.Background(), "tenant_id", "t1"), "app_id", "app_1")
	if err := l.Meter(ctx, "api_calls", 3); err != nil {
		t.Fatalf("Meter: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		events, err := s.QueryUsage(context.Background(), "t1", "app_1", meter.QueryOpts{FeatureKey: "api_calls"})
		if err == nil && len(events) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the meter flusher wrote %d events in 2s, want 1 (err %v)", len(events), err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartWithoutMigrateStillRunsTheWorkers(t *testing.T) {
	s := &migrateCounter{Store: memory.New()}
	l := ledger.New(s, ledger.WithoutMigrate(), ledger.WithMeterConfig(1, 5*time.Millisecond))
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	meterAndWait(t, l, s.Store)
	if n := s.migrations.Load(); n != 0 {
		t.Errorf("Migrate ran %d times, want 0", n)
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStartMigratesByDefault(t *testing.T) {
	s := &migrateCounter{Store: memory.New()}
	l := ledger.New(s)
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if n := s.migrations.Load(); n != 1 {
		t.Errorf("Migrate ran %d times, want 1", n)
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
