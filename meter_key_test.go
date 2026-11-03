package ledger_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/internal/telemetrytest"
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

func TestMeterWithKeyRefusesKeysAStoreWouldAlwaysReject(t *testing.T) {
	l := ledger.New(memory.New())
	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	cases := []struct {
		name, key string
		ok        bool
	}{
		{"plain", "t1/a1/msg-1", true},
		{"unicode", "t1/a1/\u00e9\u4e16", true},
		{"255 bytes", strings.Repeat("k", 255), true},
		{"256 bytes", strings.Repeat("k", 256), false},
		{"NUL byte", "k\x00k", false},
		{"invalid UTF-8", "k\xffk", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := l.MeterWithKey(ctx, "f", 1, c.key)
			switch {
			case c.ok && err != nil:
				t.Errorf("got %v, want the key accepted", err)
			case !c.ok && !errors.Is(err, ledger.ErrInvalidInput):
				t.Errorf("got %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestMeterAfterStopIsRefused(t *testing.T) {
	l := ledger.New(memory.New(), ledger.WithLifecycleInterval(0))
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	if err := l.Meter(ctx, "f", 1); !errors.Is(err, ledger.ErrStoreClosed) {
		t.Errorf("Meter after Stop: got %v, want ErrStoreClosed", err)
	}
	if err := l.MeterWithKey(ctx, "f", 1, "k1"); !errors.Is(err, ledger.ErrStoreClosed) {
		t.Errorf("MeterWithKey after Stop: got %v, want ErrStoreClosed", err)
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
			// flushed meters a marker event behind whatever came before it and
			// waits for the marker: one worker flushes in order, so everything
			// metered earlier has been through IngestBatch by then.
			flushed := func(marker string) {
				t.Helper()
				if err := l.MeterWithKey(ctx, marker, 1, marker+"-"+suffix); err != nil {
					t.Fatalf("MeterWithKey %s: %v", marker, err)
				}
				waitFor(t, marker, func() bool { return usageOf(t, s, tenant, "a1", marker) == 1 })
			}

			// The same key twice in one go: the pair may share a batch.
			for range 2 {
				if err := l.MeterWithKey(ctx, "f", 5, key); err != nil {
					t.Fatalf("MeterWithKey: %v", err)
				}
			}
			flushed("m1")
			if got := usageOf(t, s, tenant, "a1", "f"); got != 5 {
				t.Errorf("usage = %d after the same key twice, want 5 (counted once)", got)
			}

			// And again after the first has been written: a batch of its own.
			if err := l.MeterWithKey(ctx, "f", 5, key); err != nil {
				t.Fatalf("MeterWithKey: %v", err)
			}
			flushed("m2")
			if got := usageOf(t, s, tenant, "a1", "f"); got != 5 {
				t.Errorf("usage = %d after the key came back in a later batch, want 5", got)
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
	sink := telemetrytest.NewSink()
	// A batch of two means the unkeyed and the keyed event go out together.
	l := ledger.New(s, ledger.WithMetrics(sink), ledger.WithLifecycleInterval(0), ledger.WithMeterConfig(2, time.Hour))
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
	// The counter moves just after the write returns.
	waitFor(t, "the landed event to be counted", func() bool {
		return sink.Value(`ledger_meter_events_total{result="ok"}`) == 1
	})

	if got := usageOf(t, s, "t1", "a1", "keyed"); got != 3 {
		t.Errorf("keyed usage = %d, want 3 (ingested once)", got)
	}
	if got := usageOf(t, s, "t1", "a1", "unkeyed"); got != 0 {
		t.Errorf("unkeyed usage = %d, want 0: events without a key are dropped when a flush fails", got)
	}

	// The unkeyed event was lost with the first batch and counted as lost, and
	// the keyed one was not: it was held, not dropped.
	if got := sink.Value(`ledger_meter_events_total{result="error"}`); got != 1 {
		t.Errorf("events counted as lost = %v, want 1 (the unkeyed one only)", got)
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
	// Nothing is due before Stop, so no step below waits on the clock.
	ledger.SetMeterRetryBackoff(l, time.Hour, time.Hour)
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	// k1 fails and is held. k2 arrives while k1 is not due, goes out alone,
	// fails too, and queues behind k1.
	for i, key := range []string{"k1", "k2"} {
		if err := l.MeterWithKey(ctx, "f", 1, key); err != nil {
			t.Fatalf("MeterWithKey: %v", err)
		}
		waitFor(t, key+"'s failed attempt", func() bool { return len(s.snapshot()) == i+1 })
	}
	if got := usageOf(t, s, "t1", "a1", "f"); got != 0 {
		t.Fatalf("usage = %d before the retry, want 0", got)
	}

	// Stop retries what is held, oldest first.
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// The batch size is 1, so the retry goes out one event per write. What
	// counts is the order the store saw them land in.
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
