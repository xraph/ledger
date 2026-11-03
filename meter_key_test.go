package ledger_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/store"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/store/storetest"
)

func TestMeterWithKeyRefusesAnEmptyKey(t *testing.T) {
	l := ledger.New(memory.New())
	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	if err := l.MeterWithKey(ctx, "f", 1, ""); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("empty key: got %v, want ErrInvalidInput", err)
	}
}

func TestMeterWithKeyIdempotent(t *testing.T) {
	backends := []struct {
		name string
		new  func(*testing.T) store.Store
	}{
		{"memory", storetest.NewMemory},
		{"sqlite", storetest.NewSQLite},
		{"postgres", storetest.NewPostgres}, // skips without LEDGER_TEST_POSTGRES_DSN
	}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.new(t)
			// Postgres is never dropped between runs, so the tenant and the key
			// are unique to this run.
			suffix := id.NewUsageEventID().String()
			tenant, key := "t-"+suffix, "k-"+suffix

			l := ledger.New(s, ledger.WithoutMigrate(), ledger.WithLifecycleInterval(0),
				ledger.WithMeterConfig(100, 5*time.Millisecond))
			if err := l.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			t.Cleanup(func() { _ = l.Stop() })

			ctx := ledger.WithTenant(context.Background(), tenant, "a1")
			for range 2 {
				if err := l.MeterWithKey(ctx, "f", 5, key); err != nil {
					t.Fatalf("MeterWithKey: %v", err)
				}
			}
			// One worker flushes in order, so once this marker has landed both
			// duplicates above have been through IngestBatch.
			if err := l.MeterWithKey(ctx, "marker", 1, "marker-"+key); err != nil {
				t.Fatalf("MeterWithKey marker: %v", err)
			}
			waitFor(t, "the marker", func() bool { return usageOf(t, s, tenant, "a1", "marker") == 1 })

			if got := usageOf(t, s, tenant, "a1", "f"); got != 5 {
				t.Errorf("usage = %d after the same key twice, want 5 (counted once)", got)
			}
		})
	}
}

// flakyStore fails the first failures calls to IngestBatch, and records every
// call.
type flakyStore struct {
	*memory.Store

	mu       sync.Mutex
	failures int
	calls    []flakyCall
}

type flakyCall struct {
	at   time.Time
	keys []string // idempotency key of every event, empty for unkeyed ones
	err  error
}

func (f *flakyStore) IngestBatch(ctx context.Context, events []*meter.UsageEvent) error {
	f.mu.Lock()
	call := flakyCall{at: time.Now()}
	for _, e := range events {
		call.keys = append(call.keys, e.IdempotencyKey)
	}
	if len(f.calls) < f.failures {
		call.err = errors.New("store is down")
	}
	f.calls = append(f.calls, call)
	f.mu.Unlock()

	if call.err != nil {
		return call.err
	}
	return f.Store.IngestBatch(ctx, events)
}

func (f *flakyStore) snapshot() []flakyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]flakyCall(nil), f.calls...)
}

func TestKeyedEventsRetriedAfterFailedFlush(t *testing.T) {
	s := &flakyStore{Store: memory.New(), failures: 2}
	// A batch of two means the unkeyed and the keyed event go out together.
	l := ledger.New(s, ledger.WithLifecycleInterval(0), ledger.WithMeterConfig(2, time.Hour))
	ledger.SetMeterRetryBackoff(l, 40*time.Millisecond, time.Second)
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = l.Stop() })

	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	if err := l.Meter(ctx, "unkeyed", 7); err != nil {
		t.Fatalf("Meter: %v", err)
	}
	if err := l.MeterWithKey(ctx, "keyed", 3, "k1"); err != nil {
		t.Fatalf("MeterWithKey: %v", err)
	}
	waitFor(t, "the keyed event to land", func() bool { return usageOf(t, s, "t1", "a1", "keyed") == 3 })

	if got := usageOf(t, s, "t1", "a1", "keyed"); got != 3 {
		t.Errorf("keyed usage = %d, want 3 (ingested once)", got)
	}
	if got := usageOf(t, s, "t1", "a1", "unkeyed"); got != 0 {
		t.Errorf("unkeyed usage = %d, want 0: events without a key are dropped when a flush fails", got)
	}

	calls := s.snapshot()
	if len(calls) != 3 {
		t.Fatalf("IngestBatch ran %d times, want 3 (two failures, then success)", len(calls))
	}
	if len(calls[0].keys) != 2 {
		t.Errorf("first call carried %d events, want 2", len(calls[0].keys))
	}
	for i := 1; i < 3; i++ {
		if len(calls[i].keys) != 1 || calls[i].keys[0] != "k1" {
			t.Errorf("retry %d carried %v, want just k1", i, calls[i].keys)
		}
	}
	// The wait doubles: one initial delay, then two.
	if gap := calls[1].at.Sub(calls[0].at); gap < 40*time.Millisecond {
		t.Errorf("first retry came after %v, want at least 40ms", gap)
	}
	if gap := calls[2].at.Sub(calls[1].at); gap < 80*time.Millisecond {
		t.Errorf("second retry came after %v, want at least 80ms", gap)
	}
}

func TestHeldEventsGoAheadOfNewerOnes(t *testing.T) {
	s := &flakyStore{Store: memory.New(), failures: 2}
	l := ledger.New(s, ledger.WithLifecycleInterval(0), ledger.WithMeterConfig(1, time.Hour))
	ledger.SetMeterRetryBackoff(l, 200*time.Millisecond, time.Second)
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = l.Stop() })

	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	if err := l.MeterWithKey(ctx, "f", 1, "k1"); err != nil {
		t.Fatalf("MeterWithKey: %v", err)
	}
	waitFor(t, "the first attempt", func() bool { return len(s.snapshot()) >= 1 })
	if err := l.MeterWithKey(ctx, "f", 1, "k2"); err != nil {
		t.Fatalf("MeterWithKey: %v", err)
	}
	waitFor(t, "both events to land", func() bool { return usageOf(t, s, "t1", "a1", "f") == 2 })

	// The batch size is 1, so a retry goes out one event per write. What counts
	// is the order the store saw them land in.
	var landed []string
	for _, c := range s.snapshot() {
		if c.err == nil {
			landed = append(landed, c.keys...)
		}
	}
	if len(landed) != 2 || landed[0] != "k1" || landed[1] != "k2" {
		t.Errorf("events landed in the order %v, want [k1 k2]: the held event goes first", landed)
	}
}

func TestStopMakesOneLastAttempt(t *testing.T) {
	s := &flakyStore{Store: memory.New(), failures: 1}
	l := ledger.New(s, ledger.WithLifecycleInterval(0), ledger.WithMeterConfig(1, time.Hour))
	ledger.SetMeterRetryBackoff(l, time.Hour, time.Hour) // no retry before Stop
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	if err := l.MeterWithKey(ctx, "f", 9, "k1"); err != nil {
		t.Fatalf("MeterWithKey: %v", err)
	}
	waitFor(t, "the failed attempt", func() bool { return len(s.snapshot()) == 1 })

	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := usageOf(t, s, "t1", "a1", "f"); got != 9 {
		t.Errorf("usage after Stop = %d, want 9: Stop retries held events once", got)
	}
}
