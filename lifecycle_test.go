package ledger_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// lifecycleEvents records the lifecycle hooks by subscription or invoice id.
type lifecycleEvents struct {
	mu        sync.Mutex
	canceled  []string
	scheduled []string
	trials    []string
	renewed   []string
	pastDue   []string
	renewals  []*subscription.Renewal
	// scheduledSubs is what OnSubscriptionCancelScheduled received.
	scheduledSubs []*subscription.Subscription
}

func (e *lifecycleEvents) Name() string { return "lifecycle-events" }

func (e *lifecycleEvents) note(list *[]string, v interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch x := v.(type) {
	case *subscription.Subscription:
		*list = append(*list, x.ID.String())
	case *subscription.Renewal:
		*list = append(*list, x.Subscription.ID.String())
		e.renewals = append(e.renewals, x)
	case *invoice.Invoice:
		*list = append(*list, x.ID.String())
	default:
		*list = append(*list, "?")
	}
	return nil
}

func (e *lifecycleEvents) OnSubscriptionCanceled(_ context.Context, sub interface{}) error {
	return e.note(&e.canceled, sub)
}

func (e *lifecycleEvents) OnSubscriptionCancelScheduled(_ context.Context, sub interface{}) error {
	if x, ok := sub.(*subscription.Subscription); ok {
		e.mu.Lock()
		e.scheduledSubs = append(e.scheduledSubs, x)
		e.mu.Unlock()
	}
	return e.note(&e.scheduled, sub)
}

func (e *lifecycleEvents) OnSubscriptionTrialEnded(_ context.Context, sub interface{}) error {
	return e.note(&e.trials, sub)
}

func (e *lifecycleEvents) OnSubscriptionRenewed(_ context.Context, renewal interface{}) error {
	return e.note(&e.renewed, renewal)
}

func (e *lifecycleEvents) OnInvoicePastDue(_ context.Context, inv interface{}) error {
	return e.note(&e.pastDue, inv)
}

func (e *lifecycleEvents) count(list *[]string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(*list)
}

func at(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func ptr(t time.Time) *time.Time { return &t }

// lifecycleFixture is an engine over a memory store with the event recorder
// registered, and an active monthly plan in app_1.
func lifecycleFixture(t *testing.T) (*ledger.Ledger, *memory.Store, *lifecycleEvents, *plan.Plan) {
	t.Helper()
	s := memory.New()
	ev := &lifecycleEvents{}
	l := ledger.New(s, ledger.WithPlugin(ev))
	return l, s, ev, activePlan(t, l, "pro", "app_1", 0)
}

// planBilled creates and activates a plan in app_1 billed every period.
func planBilled(t *testing.T, l *ledger.Ledger, slug string, period plan.Period) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Name: slug, Slug: slug, Currency: "usd", AppID: "app_1",
		Pricing: &plan.Pricing{BaseAmount: types.USD(4900), BillingPeriod: period},
	}
	if err := l.CreatePlan(context.Background(), p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := l.ActivatePlan(context.Background(), p.ID); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	return p
}

// seedSub writes an active subscription for January 2026, created on
// 1 January 2026, straight to the store, after edit has put it in whatever
// state the test needs. The creation date matters to ForPeriod, which refuses
// a period that ended before the subscription existed.
func seedSub(t *testing.T, s *memory.Store, p *plan.Plan, edit func(*subscription.Subscription)) *subscription.Subscription {
	t.Helper()
	sub := &subscription.Subscription{
		Entity: types.Entity{CreatedAt: at(2026, 1, 1), UpdatedAt: at(2026, 1, 1)}, ID: id.NewSubscriptionID(),
		TenantID: "t_" + id.NewSubscriptionID().String(), PlanID: p.ID, AppID: p.AppID,
		Status:             subscription.StatusActive,
		CurrentPeriodStart: at(2026, 1, 1), CurrentPeriodEnd: at(2026, 2, 1),
	}
	edit(sub)
	if err := s.CreateSubscription(context.Background(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	return sub
}

func reload(t *testing.T, l *ledger.Ledger, sub *subscription.Subscription) *subscription.Subscription {
	t.Helper()
	got, err := l.GetSubscription(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	return got
}

func samePeriod(t *testing.T, got *subscription.Subscription, start, end time.Time) {
	t.Helper()
	if !got.CurrentPeriodStart.Equal(start) || !got.CurrentPeriodEnd.Equal(end) {
		t.Errorf("period %v to %v, want %v to %v", got.CurrentPeriodStart, got.CurrentPeriodEnd, start, end)
	}
}

func TestAdvanceCatchesAPeriodUpToNowInOneRun(t *testing.T) {
	ctx := context.Background()
	l, s, ev, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 1, 31), at(2026, 2, 28)
	})

	report, err := l.Advance(ctx, at(2026, 5, 15))
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.PeriodsAdvanced) != 1 || report.PeriodsAdvanced[0].String() != sub.ID.String() {
		t.Errorf("advanced %v, want only %s", report.PeriodsAdvanced, sub.ID)
	}
	samePeriod(t, reload(t, l, sub), at(2026, 4, 30), at(2026, 5, 31))
	if n := ev.count(&ev.renewed); n != 1 {
		t.Fatalf("OnSubscriptionRenewed fired %d times, want once for the whole catch-up", n)
	}
	wantEnded := []subscription.Period{
		{Start: at(2026, 1, 31), End: at(2026, 2, 28)},
		{Start: at(2026, 2, 28), End: at(2026, 3, 31)},
		{Start: at(2026, 3, 31), End: at(2026, 4, 30)},
	}
	if got := ev.renewals[0].Ended; !reflect.DeepEqual(got, wantEnded) {
		t.Errorf("the renewal lists ended periods %v, want %v", got, wantEnded)
	}
	if !ev.renewals[0].Subscription.CurrentPeriodEnd.Equal(at(2026, 5, 31)) {
		t.Errorf("the renewal's subscription ends %v, want the new period", ev.renewals[0].Subscription.CurrentPeriodEnd)
	}

	again, err := l.Advance(ctx, at(2026, 5, 15))
	if err != nil || !again.Empty() {
		t.Errorf("a second run at the same moment: %+v, %v; want nothing to do", again, err)
	}
}

func TestAdvanceRollsAYearlyPlanOnItsAnchor(t *testing.T) {
	l, s, _, _ := lifecycleFixture(t)
	yearly := planBilled(t, l, "annual", plan.PeriodYearly)
	sub := seedSub(t, s, yearly, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2024, 2, 29), at(2025, 2, 28)
	})
	if _, err := l.Advance(context.Background(), at(2026, 3, 1)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	samePeriod(t, reload(t, l, sub), at(2026, 2, 28), at(2027, 2, 28))
}

func TestAdvanceLeavesPausedSubscriptionsAndOneOffPlansAlone(t *testing.T) {
	l, s, _, p := lifecycleFixture(t)
	once := planBilled(t, l, "once", plan.PeriodNone)
	paused := seedSub(t, s, p, func(x *subscription.Subscription) { x.Status = subscription.StatusPaused })
	oneOff := seedSub(t, s, once, func(*subscription.Subscription) {})

	report, err := l.Advance(context.Background(), at(2026, 6, 1))
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.PeriodsAdvanced) != 0 {
		t.Errorf("advanced %v, want nothing", report.PeriodsAdvanced)
	}
	samePeriod(t, reload(t, l, paused), at(2026, 1, 1), at(2026, 2, 1))
	samePeriod(t, reload(t, l, oneOff), at(2026, 1, 1), at(2026, 2, 1))
}

func TestAdvanceEnactsADueCancelOnce(t *testing.T) {
	ctx := context.Background()
	l, s, ev, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) { x.CancelAt = ptr(at(2026, 2, 1)) })

	report, err := l.Advance(ctx, at(2026, 2, 1)) // exactly on the date
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.CancelsEnacted) != 1 || len(report.PeriodsAdvanced) != 0 {
		t.Errorf("report %+v, want one cancel and no advance: the period ends where the subscription does", report)
	}
	got := reload(t, l, sub)
	if got.Status != subscription.StatusCanceled || got.CanceledAt == nil || !got.CanceledAt.Equal(at(2026, 2, 1)) {
		t.Errorf("status %q canceled_at %v, want canceled at 1 February", got.Status, got.CanceledAt)
	}
	if got.EndedAt != nil {
		t.Errorf("ended_at %v, want it left unset", got.EndedAt)
	}
	samePeriod(t, got, at(2026, 1, 1), at(2026, 2, 1))
	if _, err := l.GetActiveSubscription(ctx, sub.TenantID, sub.AppID); !errors.Is(err, ledger.ErrNoActiveSubscription) {
		t.Errorf("GetActiveSubscription after the cancel: %v, want ErrNoActiveSubscription", err)
	}

	if _, err := l.Advance(ctx, at(2026, 3, 1)); err != nil {
		t.Fatalf("second Advance: %v", err)
	}
	if n := ev.count(&ev.canceled); n != 1 {
		t.Errorf("OnSubscriptionCanceled fired %d times, want once", n)
	}
}

func TestAdvanceStopsAMissedPeriodAtTheCancelDate(t *testing.T) {
	l, s, _, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) { x.CancelAt = ptr(at(2026, 3, 1)) })

	report, err := l.Advance(context.Background(), at(2026, 6, 1))
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.PeriodsAdvanced) != 1 || len(report.CancelsEnacted) != 1 {
		t.Errorf("report %+v, want one advance and one cancel", report)
	}
	got := reload(t, l, sub)
	samePeriod(t, got, at(2026, 2, 1), at(2026, 3, 1))
	if got.Status != subscription.StatusCanceled {
		t.Errorf("status %q, want canceled", got.Status)
	}
}

func TestAdvanceEndsADueTrial(t *testing.T) {
	l, s, ev, p := lifecycleFixture(t)
	due := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusTrialing
		x.TrialStart, x.TrialEnd = ptr(at(2026, 1, 1)), ptr(at(2026, 1, 15))
	})
	later := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusTrialing
		x.TrialStart, x.TrialEnd = ptr(at(2026, 1, 1)), ptr(at(2026, 1, 20))
	})
	paused := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusPaused
		x.TrialStart, x.TrialEnd = ptr(at(2026, 1, 1)), ptr(at(2026, 1, 10))
	})

	if _, err := l.Advance(context.Background(), at(2026, 1, 15)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	for _, c := range []struct {
		sub  *subscription.Subscription
		want subscription.Status
	}{{due, subscription.StatusActive}, {later, subscription.StatusTrialing}, {paused, subscription.StatusPaused}} {
		if got := reload(t, l, c.sub); got.Status != c.want {
			t.Errorf("%s: status %q, want %q", c.sub.ID, got.Status, c.want)
		}
	}
	if n := ev.count(&ev.trials); n != 1 {
		t.Errorf("OnSubscriptionTrialEnded fired %d times, want 1", n)
	}
}

func TestAdvanceCancelsATrialWhoseCancelAlsoPassed(t *testing.T) {
	l, s, ev, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusTrialing
		x.TrialStart, x.TrialEnd = ptr(at(2026, 1, 1)), ptr(at(2026, 1, 15))
		x.CancelAt = ptr(at(2026, 1, 10))
	})
	if _, err := l.Advance(context.Background(), at(2026, 1, 20)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got := reload(t, l, sub); got.Status != subscription.StatusCanceled {
		t.Errorf("status %q, want canceled", got.Status)
	}
	if n := ev.count(&ev.trials); n != 0 {
		t.Errorf("OnSubscriptionTrialEnded fired %d times for a canceled trial, want 0", n)
	}
}

func TestCancelSubscriptionAnnouncesWhatItDid(t *testing.T) {
	ctx := context.Background()
	l, _, ev, p := lifecycleFixture(t)

	later := &subscription.Subscription{TenantID: "t_later", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, later); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err := l.CancelSubscription(ctx, later.ID, false); err != nil {
		t.Fatalf("CancelSubscription at period end: %v", err)
	}
	if ev.count(&ev.scheduled) != 1 || ev.count(&ev.canceled) != 0 {
		t.Errorf("a period-end cancel: %d scheduled, %d canceled; want 1 and 0", ev.count(&ev.scheduled), ev.count(&ev.canceled))
	}
	stored := reload(t, l, later)
	if _, err := l.Advance(ctx, *stored.CancelAt); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if ev.count(&ev.canceled) != 1 {
		t.Errorf("the clock's cancel fired OnSubscriptionCanceled %d times, want 1", ev.count(&ev.canceled))
	}

	now := &subscription.Subscription{TenantID: "t_now", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, now); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err := l.CancelSubscription(ctx, now.ID, true); err != nil {
		t.Fatalf("CancelSubscription now: %v", err)
	}
	if ev.count(&ev.canceled) != 2 || ev.count(&ev.scheduled) != 1 {
		t.Errorf("an immediate cancel: %d canceled, %d scheduled; want 2 and 1", ev.count(&ev.canceled), ev.count(&ev.scheduled))
	}
}

// A scheduled cancel on a period that has already ended copies that ended
// period's end into cancel_at, leaves the subscription running and announces
// the schedule. The clock then enacts it as of that end, without first
// renewing the subscription into a period it never reaches.
func TestAdvanceEnactsAScheduledCancelOfAnEndedPeriod(t *testing.T) {
	ctx := context.Background()
	l, s, ev, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(*subscription.Subscription) {}) // January 2026, long over

	if err := l.CancelSubscription(ctx, sub.ID, false); err != nil {
		t.Fatalf("CancelSubscription: %v", err)
	}
	if ev.count(&ev.scheduled) != 1 || ev.count(&ev.canceled) != 0 {
		t.Errorf("%d scheduled, %d canceled; want 1 and 0: a scheduled cancel never stops a subscription at once", ev.count(&ev.scheduled), ev.count(&ev.canceled))
	}
	if got := ev.scheduledSubs[0].CancelAt; got == nil || !got.Equal(at(2026, 2, 1)) {
		t.Errorf("the scheduled event carries cancel_at %v, want the stored %v", got, at(2026, 2, 1))
	}
	if got := reload(t, l, sub); got.Status != subscription.StatusActive {
		t.Errorf("status %q before the clock runs, want active", got.Status)
	}

	report, err := l.Advance(ctx, at(2026, 6, 1))
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.CancelsEnacted) != 1 || len(report.PeriodsAdvanced) != 0 {
		t.Errorf("report %+v, want one cancel and no advance", report)
	}
	got := reload(t, l, sub)
	if got.Status != subscription.StatusCanceled || got.CanceledAt == nil || !got.CanceledAt.Equal(at(2026, 2, 1)) {
		t.Errorf("status %q canceled_at %v, want canceled at 1 February", got.Status, got.CanceledAt)
	}
	samePeriod(t, got, at(2026, 1, 1), at(2026, 2, 1))
	if ev.count(&ev.canceled) != 1 || ev.count(&ev.renewed) != 0 {
		t.Errorf("%d canceled, %d renewed; want 1 and 0", ev.count(&ev.canceled), ev.count(&ev.renewed))
	}
}

func TestAdvanceMarksOverdueInvoicesAndLeavesTheSubscription(t *testing.T) {
	ctx := context.Background()
	l, _, ev, _ := lifecycleFixture(t)
	yearly := planBilled(t, l, "annual", plan.PeriodYearly)
	sub := &subscription.Subscription{TenantID: "t1", PlanID: yearly.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if err = l.FinalizeInvoice(ctx, inv.ID); err != nil {
		t.Fatalf("FinalizeInvoice: %v", err)
	}
	pending, err := l.Store().GetInvoice(ctx, inv.ID)
	if err != nil {
		t.Fatalf("GetInvoice: %v", err)
	}
	due := *pending.DueDate

	onTheDay, err := l.Advance(ctx, due)
	if err != nil || len(onTheDay.InvoicesPastDue) != 0 {
		t.Errorf("on the due date: %+v, %v; want nothing past due yet", onTheDay, err)
	}
	report, err := l.Advance(ctx, due.Add(time.Second))
	if err != nil || len(report.InvoicesPastDue) != 1 {
		t.Fatalf("after the due date: %+v, %v; want one invoice past due", report, err)
	}
	got, err := l.Store().GetInvoice(ctx, inv.ID)
	if err != nil || got.Status != invoice.StatusPastDue {
		t.Errorf("invoice %+v, %v; want past_due", got, err)
	}
	if s := reload(t, l, sub); s.Status != subscription.StatusActive {
		t.Errorf("subscription status %q, want it left active", s.Status)
	}
	if ev.count(&ev.pastDue) != 1 {
		t.Errorf("OnInvoicePastDue fired %d times, want 1", ev.count(&ev.pastDue))
	}
	if err := l.MarkInvoicePaid(ctx, inv.ID, time.Now().UTC(), "late"); err != nil {
		t.Errorf("paying a past-due invoice: %v", err)
	}
}

func TestAdvanceFromTwoEnginesAppliesEachTransitionOnce(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	ev := &lifecycleEvents{}
	a := ledger.New(s, ledger.WithPlugin(ev))
	b := ledger.New(s, ledger.WithPlugin(ev))
	p := activePlan(t, a, "pro", "app_1", 0)
	for i := range 20 {
		seedSub(t, s, p, func(x *subscription.Subscription) {
			if i%2 == 0 {
				x.CancelAt = ptr(at(2026, 2, 1))
			}
		})
	}

	var wg sync.WaitGroup
	reports := make([]ledger.LifecycleReport, 2)
	errs := make([]error, 2)
	for i, eng := range []*ledger.Ledger{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reports[i], errs[i] = eng.Advance(ctx, at(2026, 3, 15))
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	cancels := len(reports[0].CancelsEnacted) + len(reports[1].CancelsEnacted)
	renewals := len(reports[0].PeriodsAdvanced) + len(reports[1].PeriodsAdvanced)
	if cancels != 10 || renewals != 10 {
		t.Errorf("two engines enacted %d cancels and %d renewals, want 10 and 10", cancels, renewals)
	}
	if ev.count(&ev.canceled) != 10 || ev.count(&ev.renewed) != 10 {
		t.Errorf("hooks fired %d canceled and %d renewed, want 10 and 10", ev.count(&ev.canceled), ev.count(&ev.renewed))
	}
}

func TestCreateSubscriptionStartsOnThePlansBillingPeriod(t *testing.T) {
	ctx := context.Background()
	l, _, _, monthly := lifecycleFixture(t)
	yearly := planBilled(t, l, "annual", plan.PeriodYearly)
	once := planBilled(t, l, "once", plan.PeriodNone)
	for _, c := range []struct {
		name  string
		p     *plan.Plan
		start time.Time
		want  time.Time
	}{
		{"monthly from the 31st", monthly, at(2026, 1, 31), at(2026, 2, 28)},
		{"yearly", yearly, at(2026, 3, 10), at(2027, 3, 10)},
		{"none keeps a month", once, at(2026, 3, 10), at(2026, 4, 10)},
	} {
		sub := &subscription.Subscription{TenantID: "t_" + c.name, PlanID: c.p.ID, AppID: "app_1", CurrentPeriodStart: c.start}
		if err := l.CreateSubscription(ctx, sub); err != nil {
			t.Fatalf("%s: CreateSubscription: %v", c.name, err)
		}
		if !sub.CurrentPeriodEnd.Equal(c.want) {
			t.Errorf("%s: first period ends %v, want %v", c.name, sub.CurrentPeriodEnd, c.want)
		}
	}
}

// A plan whose billing period the engine does not know is reported and
// skipped, and the rest of the run carries on.
func TestAdvanceReportsAnUnknownBillingPeriodAndCarriesOn(t *testing.T) {
	l, s, _, p := lifecycleFixture(t)
	weekly := planBilled(t, l, "weekly", plan.Period("weekly"))
	odd := seedSub(t, s, weekly, func(*subscription.Subscription) {})
	fine := seedSub(t, s, p, func(*subscription.Subscription) {})

	report, err := l.Advance(context.Background(), at(2026, 2, 15))
	if err == nil || !strings.Contains(err.Error(), `unknown billing period "weekly"`) {
		t.Errorf("Advance error %v, want the unknown period reported", err)
	}
	if len(report.PeriodsAdvanced) != 1 || report.PeriodsAdvanced[0].String() != fine.ID.String() {
		t.Errorf("advanced %v, want only %s", report.PeriodsAdvanced, fine.ID)
	}
	samePeriod(t, reload(t, l, odd), at(2026, 1, 1), at(2026, 2, 1))
	samePeriod(t, reload(t, l, fine), at(2026, 2, 1), at(2026, 3, 1))
}

// Rows a step cannot move stay due. The step pages past them, so more of them
// than fit in one batch never hide a row it can move.
func TestAdvancePagesPastRowsThatCannotMove(t *testing.T) {
	l, s, _, p := lifecycleFixture(t)
	once := planBilled(t, l, "once", plan.PeriodNone)
	for range 150 {
		seedSub(t, s, once, func(*subscription.Subscription) {})
	}
	// Due last: its period ended after every stuck one.
	last := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 1, 2), at(2026, 2, 2)
	})

	report, err := l.Advance(context.Background(), at(2026, 2, 15))
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.PeriodsAdvanced) != 1 || report.PeriodsAdvanced[0].String() != last.ID.String() {
		t.Errorf("advanced %v, want only %s", report.PeriodsAdvanced, last.ID)
	}
	samePeriod(t, reload(t, l, last), at(2026, 2, 2), at(2026, 3, 2))
}
