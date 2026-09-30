package ledger_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	log "github.com/xraph/go-utils/log"

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

// dueStore is a memory store whose ListDueSubscriptions, the clock's first
// query of every run, calls hook before it answers.
type dueStore struct {
	*memory.Store
	calls atomic.Int32
	hook  func(ctx context.Context, call int32) error
}

func (s *dueStore) ListDueSubscriptions(ctx context.Context, opts subscription.DueOpts) ([]*subscription.Subscription, error) {
	n := s.calls.Add(1)
	if s.hook != nil {
		if err := s.hook(ctx, n); err != nil {
			return nil, err
		}
	}
	return s.Store.ListDueSubscriptions(ctx, opts)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up after 2s waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Stop cancels the run in flight. A run blocked in the store until its
// context ends is released by Stop, not by the run's own one-interval
// deadline, and Stop is not logged as a failure.
func TestStopCancelsALifecycleRunInFlight(t *testing.T) {
	const interval = 600 * time.Millisecond
	entered := make(chan struct{})
	s := &dueStore{Store: memory.New(), hook: func(ctx context.Context, _ int32) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	logger := log.NewTestLogger().(*log.TestLogger)
	l := ledger.New(s, ledger.WithLogger(logger), ledger.WithLifecycleInterval(interval))
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the worker never started a run")
	}

	stopped := make(chan error, 1)
	began := time.Now()
	go func() { stopped <- l.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
		// The run's own deadline is the full interval: returning in under
		// half of it means Stop cancelled the run.
		if took := time.Since(began); took > interval/2 {
			t.Errorf("Stop took %v with a run blocked in the store, want well under the %v interval", took, interval)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop is still waiting on the lifecycle run after 3s")
	}
	if n := logger.CountLogs("WARN"); n != 0 {
		t.Errorf("a run cut short by Stop logged %d warnings, want none", n)
	}
	if n := s.calls.Load(); n != 1 {
		t.Errorf("%d runs started, want exactly the one Stop cancelled", n)
	}
}

// A panic in a store call ends one run, is logged at Error, and the next tick
// runs.
func TestLifecycleWorkerSurvivesAPanic(t *testing.T) {
	s := &dueStore{Store: memory.New(), hook: func(_ context.Context, call int32) error {
		if call == 1 {
			panic("driver blew up")
		}
		return nil
	}}
	logger := log.NewTestLogger().(*log.TestLogger)
	l := ledger.New(s, ledger.WithLogger(logger), ledger.WithLifecycleInterval(5*time.Millisecond))
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer l.Stop()

	waitFor(t, "a run after the panicking one", func() bool { return s.calls.Load() >= 3 })
	if !logger.AssertHasLog("ERROR", "ledger: lifecycle run panicked") {
		t.Errorf("the panic was not logged at Error: %d error entries", logger.CountLogs("ERROR"))
	}
}

// A failed run is logged as a warning, for an operator to see.
func TestLifecycleRunErrorIsLogged(t *testing.T) {
	s := &dueStore{Store: memory.New(), hook: func(context.Context, int32) error { return errors.New("due query failed") }}
	logger := log.NewTestLogger().(*log.TestLogger)
	l := ledger.New(s, ledger.WithLogger(logger), ledger.WithLifecycleInterval(5*time.Millisecond))
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer l.Stop()

	waitFor(t, "the warning", func() bool { return logger.AssertHasLog("WARN", "ledger: lifecycle run left work undone") })
}

// A subscription created through the engine is stamped from its clock: the
// first period and the trial start on the clock's date, and the clock then
// rolls it and ForPeriod bills what ended, with no wall-clock date involved.
func TestCreateSubscriptionReadsTheInjectedClock(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	ev := &lifecycleEvents{}
	now := at(2031, 3, 15)
	l := ledger.New(s, ledger.WithPlugin(ev), ledger.WithClock(func() time.Time { return now }))
	trial := activePlan(t, l, "trial", "app_1", 14)

	sub := &subscription.Subscription{TenantID: "t1", PlanID: trial.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if !sub.CreatedAt.Equal(now) || !sub.UpdatedAt.Equal(now) {
		t.Errorf("stamps %v, %v; want the clock's %v", sub.CreatedAt, sub.UpdatedAt, now)
	}
	samePeriod(t, sub, at(2031, 3, 15), at(2031, 4, 15))
	if sub.Status != subscription.StatusTrialing || sub.TrialStart == nil || !sub.TrialStart.Equal(now) ||
		sub.TrialEnd == nil || !sub.TrialEnd.Equal(at(2031, 3, 29)) {
		t.Errorf("trial %v to %v (%s), want 15 to 29 March 2031", sub.TrialStart, sub.TrialEnd, sub.Status)
	}

	// Two months on, the clock ends the trial and rolls the period, and each
	// ended period can be invoiced.
	now = at(2031, 5, 20)
	report, err := l.Advance(ctx, now)
	if err != nil || len(report.PeriodsAdvanced) != 1 || len(report.TrialsEnded) != 1 {
		t.Fatalf("Advance: %+v, %v; want the period rolled and the trial ended", report, err)
	}
	if len(ev.renewals) != 1 || len(ev.renewals[0].Ended) != 2 {
		t.Fatalf("renewals %+v, want one listing two ended periods", ev.renewals)
	}
	for _, period := range ev.renewals[0].Ended {
		if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(period.Start, period.End)); err != nil {
			t.Errorf("GenerateInvoice for %v to %v: %v", period.Start, period.End, err)
		}
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
