package ledger_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

func TestLifecycleWorkerRunsTheClock(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s,
		ledger.WithLifecycleInterval(5*time.Millisecond),
		ledger.WithClock(func() time.Time { return at(2026, 3, 1) }),
	)
	p := activePlan(t, l, "pro", "app_1", 0)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) { x.CancelAt = ptr(at(2026, 2, 1)) })

	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := s.GetSubscription(ctx, sub.ID)
		if err == nil && got.Status == subscription.StatusCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker did not enact the cancel within 2s: %+v, %v", got, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestLifecycleIntervalOption(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []ledger.Option
		want time.Duration
	}{
		{"default", nil, time.Minute},
		{"set", []ledger.Option{ledger.WithLifecycleInterval(90 * time.Second)}, 90 * time.Second},
		{"off", []ledger.Option{ledger.WithLifecycleInterval(0)}, 0},
		{"negative is off", []ledger.Option{ledger.WithLifecycleInterval(-time.Second)}, 0},
	} {
		if got := ledger.New(memory.New(), c.opts...).LifecycleInterval(); got != c.want {
			t.Errorf("%s: interval %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStartAndStopWithTheClockOff(t *testing.T) {
	l := ledger.New(memory.New(), ledger.WithLifecycleInterval(0))
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestLifecycleWorkerRunsWithoutMigrate(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s,
		ledger.WithoutMigrate(),
		ledger.WithLifecycleInterval(5*time.Millisecond),
		ledger.WithClock(func() time.Time { return at(2026, 3, 1) }),
	)
	p := activePlan(t, l, "pro", "app_1", 0)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) { x.CancelAt = ptr(at(2026, 2, 1)) })
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer l.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, err := s.GetSubscription(ctx, sub.ID); err == nil && got.Status == subscription.StatusCanceled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker did not run with WithoutMigrate")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLifecycleWorkerSurvivesACanceledStartContext(t *testing.T) {
	s := memory.New()
	l := ledger.New(s,
		ledger.WithLifecycleInterval(5*time.Millisecond),
		ledger.WithClock(func() time.Time { return at(2026, 3, 1) }),
	)
	p := activePlan(t, l, "pro", "app_1", 0)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) { x.CancelAt = ptr(at(2026, 2, 1)) })

	startCtx, cancel := context.WithCancel(context.Background())
	if err := l.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()
	defer l.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, err := s.GetSubscription(context.Background(), sub.ID); err == nil && got.Status == subscription.StatusCanceled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker stopped with the Start context")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStopEndsTheLifecycleWorker(t *testing.T) {
	before := runtime.NumGoroutine()
	l := ledger.New(memory.New(), ledger.WithLifecycleInterval(time.Millisecond))
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stop waits for its workers, so none may outlive it. Allow the runtime a
	// moment to reap goroutines that have already returned.
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d before Start, %d after Stop", before, runtime.NumGoroutine())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Invoicing runs on the engine's clock too. A clock driven past wall time
// ends periods that have not started by the wall clock, and each of them must
// still be invoiceable through ForPeriod: the periods check reads l.now(), not
// time.Now().
func TestInvoicingReadsTheInjectedClock(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	ev := &lifecycleEvents{}
	// Three months past wall time: its last ended periods start in the wall
	// clock's future.
	now := time.Now().UTC().AddDate(0, 3, 0)
	l := ledger.New(s, ledger.WithPlugin(ev), ledger.WithClock(func() time.Time { return now }))
	p := activePlan(t, l, "pro", "app_1", 0)
	sub := seedSub(t, s, p, func(*subscription.Subscription) {})

	if _, err := l.Advance(ctx, now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(ev.renewals) != 1 || len(ev.renewals[0].Ended) < 2 {
		t.Fatalf("renewals %+v, want one listing every ended period", ev.renewals)
	}
	for _, period := range ev.renewals[0].Ended {
		inv, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(period.Start, period.End))
		if err != nil {
			t.Fatalf("GenerateInvoice for %v to %v: %v", period.Start, period.End, err)
		}
		if !inv.PeriodStart.Equal(period.Start) || !inv.PeriodEnd.Equal(period.End) {
			t.Errorf("invoice period %v to %v, want %v to %v", inv.PeriodStart, inv.PeriodEnd, period.Start, period.End)
		}
	}
}

// A coupon's window and an invoice's due date are read against the engine's
// clock as well, so a clock moved off wall time stays consistent with the
// past-due step that judges the due date.
func TestCouponWindowAndDueDateReadTheInjectedClock(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	now := time.Now().UTC().AddDate(0, 6, 0).Truncate(time.Second)
	l := ledger.New(s, ledger.WithClock(func() time.Time { return now }))
	p := activePlan(t, l, "pro", "app_1", 0)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = now.AddDate(0, -1, 0), now.AddDate(0, 1, 0)
	})

	// Valid under wall time, over under the clock.
	until := time.Now().UTC().AddDate(0, 1, 0)
	c := baseCoupon()
	c.ValidUntil = &until
	mustCreateCoupon(t, s, c)
	if _, err := l.ApplyCoupon(ctx, sub.ID, c.Code); !errors.Is(err, ledger.ErrCouponExpired) {
		t.Errorf("a coupon that ended before the engine's clock: got %v, want ErrCouponExpired", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if err = l.FinalizeInvoice(ctx, inv.ID); err != nil {
		t.Fatalf("FinalizeInvoice: %v", err)
	}
	got, err := s.GetInvoice(ctx, inv.ID)
	if err != nil || got.Status != invoice.StatusPending || got.DueDate == nil {
		t.Fatalf("invoice %+v, %v; want pending with a due date", got, err)
	}
	if want := now.AddDate(0, 0, 30); !got.DueDate.Equal(want) {
		t.Errorf("due date %v, want 30 days after the engine's clock, %v", got.DueDate, want)
	}
}
