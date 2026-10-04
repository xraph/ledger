package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

// pauseFixture is an engine on a clock the test moves, with the event
// recorder, over the given store.
func pauseFixture(s ledgerstore.Store) (*ledger.Ledger, *lifecycleEvents, func(time.Time)) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	ev := &lifecycleEvents{}
	l := ledger.New(s, ledger.WithPlugin(ev), ledger.WithClock(func() time.Time { return now }))
	return l, ev, func(t time.Time) { now = t }
}

// tick runs the clock at now, as the worker would.
func tick(t *testing.T, l *ledger.Ledger, setNow func(time.Time), now time.Time) {
	t.Helper()
	setNow(now)
	if _, err := l.Advance(context.Background(), now); err != nil {
		t.Fatalf("Advance at %v: %v", now, err)
	}
}

func pauseAt(t *testing.T, l *ledger.Ledger, setNow func(time.Time), sub *subscription.Subscription, at time.Time) {
	t.Helper()
	setNow(at)
	if _, err := l.PauseSubscription(context.Background(), sub.ID); err != nil {
		t.Fatalf("pause at %v: %v", at, err)
	}
}

func resumeAt(t *testing.T, l *ledger.Ledger, setNow func(time.Time), sub *subscription.Subscription, at time.Time) *subscription.Subscription {
	t.Helper()
	setNow(at)
	got, err := l.ResumeSubscription(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("resume at %v: %v", at, err)
	}
	return got
}

// Paused on 5 March and resumed on 10 March, a period of 1 March to 1 April
// becomes 1 March to 6 April: one period with one base fee, announced once
// when its stretched end passes, and the periods after it renew on the 6th.
// No two invoices overlap, and the paused period cannot be billed while it is
// still paused.
func TestResumeStretchesThePeriodByThePause(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l, ev, setNow := pauseFixture(s)
	p := activePlan(t, l, "pro", "app_1", 0)
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	pauseAt(t, l, setNow, sub, at(2026, 3, 5))
	setNow(at(2026, 3, 7))
	_, pausedErr := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(pausedErr, ledger.ErrInvalidInput) || !strings.Contains(pausedErr.Error(), "is paused; its period is billed when it ends") {
		t.Errorf("invoicing a paused period: got %v, want ErrInvalidInput saying it is billed when it ends", pausedErr)
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 3, 1), at(2026, 4, 1))); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("naming the paused current period: got %v, want ErrInvalidInput", err)
	}

	resumed := resumeAt(t, l, setNow, sub, at(2026, 3, 10))
	samePeriod(t, resumed, at(2026, 3, 1), at(2026, 4, 6))
	if resumed.Status != subscription.StatusActive || resumed.PausedAt != nil {
		t.Errorf("resumed as %s with paused_at %v", resumed.Status, resumed.PausedAt)
	}

	for d := at(2026, 3, 11); !d.After(at(2026, 5, 7)); d = d.AddDate(0, 0, 1) {
		tick(t, l, setNow, d)
	}
	if len(ev.renewals) != 2 {
		t.Fatalf("renewals %d, want two", len(ev.renewals))
	}
	first, second := ev.renewals[0].Ended, ev.renewals[1].Ended
	if len(first) != 1 || !first[0].Start.Equal(at(2026, 3, 1)) || !first[0].End.Equal(at(2026, 4, 6)) {
		t.Errorf("first renewal ended %+v, want 1 March to 6 April", first)
	}
	if len(second) != 1 || !second[0].Start.Equal(at(2026, 4, 6)) || !second[0].End.Equal(at(2026, 5, 6)) {
		t.Errorf("second renewal ended %+v, want 6 April to 6 May", second)
	}

	invoices := make([]*invoice.Invoice, 0, len(first)+len(second)+1)
	for _, period := range append(first, second...) {
		inv, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(period.Start, period.End))
		if err != nil {
			t.Fatalf("GenerateInvoice for %v to %v: %v", period.Start, period.End, err)
		}
		invoices = append(invoices, inv)
	}
	current, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice for the current period: %v", err)
	}
	invoices = append(invoices, current)
	for i, inv := range invoices {
		if base := lineItemsOfType(inv, invoice.LineItemBase); len(base) != 1 {
			t.Errorf("invoice %d bills %d base fees, want one", i, len(base))
		}
		if i > 0 && !inv.PeriodStart.Equal(invoices[i-1].PeriodEnd) {
			t.Errorf("invoice %d starts %v, the one before ends %v: they must meet, neither overlapping nor leaving a gap",
				i, inv.PeriodStart, invoices[i-1].PeriodEnd)
		}
	}
}

// A paused subscription's ended periods are finished and stay billable by name.
func TestAPausedSubscriptionsEndedPeriodsStayBillable(t *testing.T) {
	ctx := context.Background()
	l, s, _, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 2, 1), at(2026, 3, 1)
	})
	if _, err := l.PauseSubscription(ctx, sub.ID); err != nil {
		t.Fatalf("PauseSubscription: %v", err)
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 1, 1), at(2026, 2, 1))); err != nil {
		t.Errorf("an ended period of a paused subscription: %v", err)
	}
}

// Two pauses in one period stretch it by both, and a resume of a trial moves
// its end on by the pause too.
func TestTwoPausesInOnePeriodAndATrial(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l, ev, setNow := pauseFixture(s)
	p := activePlan(t, l, "trial", "app_1", 14) // trial to 15 March
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	pauseAt(t, l, setNow, sub, at(2026, 3, 5)) // ten days of trial left
	first := resumeAt(t, l, setNow, sub, at(2026, 3, 8))
	if first.Status != subscription.StatusTrialing || !first.TrialEnd.Equal(at(2026, 3, 18)) {
		t.Errorf("after the first pause: %s, trial end %v; want trialing to 18 March", first.Status, first.TrialEnd)
	}
	samePeriod(t, first, at(2026, 3, 1), at(2026, 4, 4))

	pauseAt(t, l, setNow, sub, at(2026, 3, 12))
	tick(t, l, setNow, at(2026, 3, 20)) // the moved trial end passes while paused again
	if n := ev.count(&ev.trials); n != 0 {
		t.Fatalf("a paused trial ended")
	}
	second := resumeAt(t, l, setNow, sub, at(2026, 3, 25))
	samePeriod(t, second, at(2026, 3, 1), at(2026, 4, 17))
	if second.Status != subscription.StatusTrialing || !second.TrialEnd.Equal(at(2026, 3, 31)) {
		t.Errorf("after the second pause: %s, trial end %v; want trialing to 31 March", second.Status, second.TrialEnd)
	}

	tick(t, l, setNow, at(2026, 3, 30))
	if n := ev.count(&ev.trials); n != 0 {
		t.Fatalf("the trial ended before its moved end")
	}
	tick(t, l, setNow, at(2026, 3, 31))
	if n := ev.count(&ev.trials); n != 1 {
		t.Fatalf("%d trial ends at the moved end, want one", n)
	}
	tick(t, l, setNow, at(2026, 4, 18))
	if len(ev.renewals) != 1 || len(ev.renewals[0].Ended) != 1 ||
		!ev.renewals[0].Ended[0].Start.Equal(at(2026, 3, 1)) || !ev.renewals[0].Ended[0].End.Equal(at(2026, 4, 17)) {
		t.Errorf("renewals %+v, want one listing 1 March to 17 April", ev.renewals)
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 3, 1), at(2026, 4, 17))); err != nil {
		t.Errorf("the period stretched twice: %v", err)
	}
}

// A trial over before the pause stays over: the subscription resumes active and
// its trial end does not move.
func TestResumeLeavesATrialThatEndedBeforeThePause(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l, _, setNow := pauseFixture(s)
	p := activePlan(t, l, "pro", "app_1", 0)
	trialEnd := at(2026, 2, 1)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusPaused
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 2, 1), at(2026, 3, 1)
		x.TrialEnd = &trialEnd
		x.PausedAt = ptr(at(2026, 2, 10))
	})
	setNow(at(2026, 2, 20))
	got, err := l.ResumeSubscription(ctx, sub.ID)
	if err != nil || got.Status != subscription.StatusActive || !got.TrialEnd.Equal(trialEnd) {
		t.Fatalf("resume: %+v, %v; want active with the trial end untouched", got, err)
	}
	samePeriod(t, got, at(2026, 2, 1), at(2026, 3, 11))
}

// A subscription paused before Ledger recorded paused_at has no pause start.
// Its period and its trial end stay put, and it resumes as a trial only while
// that end is ahead.
func TestResumeOfAPauseWithNoPausedAt(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l, _, setNow := pauseFixture(s)
	p := activePlan(t, l, "pro", "app_1", 0)
	trialEnd := at(2026, 3, 20)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusPaused
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 3, 1), at(2026, 4, 1)
		x.TrialEnd = &trialEnd
	})
	setNow(at(2026, 3, 10))
	got, err := l.ResumeSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("ResumeSubscription: %v", err)
	}
	if got.Status != subscription.StatusTrialing || !got.TrialEnd.Equal(trialEnd) || got.Stretch != nil {
		t.Errorf("status %s trial end %v stretch %+v, want trialing until the unmoved %v and no stretch", got.Status, got.TrialEnd, got.Stretch, trialEnd)
	}
	samePeriod(t, got, at(2026, 3, 1), at(2026, 4, 1))
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 2, 1), at(2026, 3, 1))); err != nil {
		t.Errorf("the period before, on the unchanged cadence: %v", err)
	}
}

// A resume and a second pause landing between the engine's read and its write
// make the write miss; the engine starts again from the new pause.
func TestResumeStartsAgainWhenPausedAgainMeanwhile(t *testing.T) {
	ctx := context.Background()
	st := &clockBetween{Store: memory.New()}
	l, _, setNow := pauseFixture(st)
	p := activePlan(t, l, "pro", "app_1", 0)
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"} // 1 March to 1 April
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if _, err := st.Store.PauseSubscription(ctx, sub.ID, at(2026, 3, 5)); err != nil {
		t.Fatalf("pause: %v", err)
	}
	st.clock = func() {
		read, _ := st.Store.GetSubscription(ctx, sub.ID)
		end := at(2026, 4, 3) // a two-day pause
		r := subscription.Resume{
			PausedAt: read.PausedAt, Status: subscription.StatusActive, PeriodEnd: &end,
			Stretch: &subscription.Stretch{Start: at(2026, 3, 1), End: end, OriginalEnd: at(2026, 4, 1)},
		}
		if ok, err := st.Store.ResumeSubscription(ctx, sub.ID, r); err != nil || !ok {
			t.Fatalf("the other resume: %v, %v", ok, err)
		}
		if ok, err := st.Store.PauseSubscription(ctx, sub.ID, at(2026, 3, 20)); err != nil || !ok {
			t.Fatalf("the second pause: %v, %v", ok, err)
		}
	}

	got := resumeAt(t, l, setNow, sub, at(2026, 3, 30))
	// 3 April moved on by the ten days of the second pause.
	samePeriod(t, got, at(2026, 3, 1), at(2026, 4, 13))
	if got.Stretch == nil || !got.Stretch.OriginalEnd.Equal(at(2026, 4, 1)) {
		t.Errorf("stretch %+v, want the original end of 1 April kept", got.Stretch)
	}
}

// accepted says whether GenerateInvoice took the period as one the
// subscription had: it billed it, or it was already billed.
func accepted(ctx context.Context, l *ledger.Ledger, sub *subscription.Subscription, period subscription.Period) (bool, error) {
	_, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(period.Start, period.End))
	switch {
	case err == nil, errors.Is(err, ledger.ErrAlreadyExists):
		return true, nil
	case errors.Is(err, ledger.ErrInvalidInput):
		return false, nil
	default:
		return false, err
	}
}

// neighbours are periods a real one could be mistaken for: either bound moved
// by a nanosecond, a day or a month, both moved together, and a period
// spanning two real ones.
func neighbours(had []subscription.Period) []subscription.Period {
	var out []subscription.Period
	for i, r := range had {
		for _, d := range []func(time.Time, int) time.Time{
			func(t time.Time, k int) time.Time { return t.Add(time.Duration(k)) },
			func(t time.Time, k int) time.Time { return t.AddDate(0, 0, k) },
			func(t time.Time, k int) time.Time { return t.AddDate(0, k, 0) },
		} {
			for _, k := range []int{-1, 1} {
				out = append(out,
					subscription.Period{Start: d(r.Start, k), End: r.End},
					subscription.Period{Start: r.Start, End: d(r.End, k)},
					subscription.Period{Start: d(r.Start, k), End: d(r.End, k)},
				)
			}
		}
		if i+1 < len(had) {
			out = append(out, subscription.Period{Start: r.Start, End: had[i+1].End})
		}
	}
	var phantoms []subscription.Period
	for _, n := range out {
		isReal := false
		for _, r := range had {
			if n.Start.Equal(r.Start) && n.End.Equal(r.End) {
				isReal = true
			}
		}
		if !isReal {
			phantoms = append(phantoms, n)
		}
	}
	return phantoms
}

// probePeriods checks ForPeriod against every real period, listed oldest
// first and ending with the current one: each from provable on is accepted,
// each before it refused, and every neighbour refused.
func probePeriods(t *testing.T, l *ledger.Ledger, sub *subscription.Subscription, had []subscription.Period, provable int) (checks int) {
	t.Helper()
	ctx := context.Background()
	for _, n := range neighbours(had) {
		checks++
		ok, err := accepted(ctx, l, sub, n)
		if err != nil {
			t.Fatalf("probe %v to %v: %v", n.Start, n.End, err)
		}
		if ok {
			t.Errorf("phantom %v to %v accepted", n.Start, n.End)
		}
	}
	for i, r := range had[:len(had)-1] { // the current period is billed as the current one
		checks++
		ok, err := accepted(ctx, l, sub, r)
		if err != nil {
			t.Fatalf("probe %v to %v: %v", r.Start, r.End, err)
		}
		if want := i >= provable; ok != want {
			t.Errorf("real period %d, %v to %v: accepted %v, want %v", i, r.Start, r.End, ok, want)
		}
	}
	return checks
}

// invoiceEnded bills every period the clock announced, the way a billing
// plugin does from OnSubscriptionRenewed: each must come through ForPeriod and
// become a new invoice. had lists the periods oldest first, ending with the
// current one, which is not an ended period.
func invoiceEnded(t *testing.T, l *ledger.Ledger, sub *subscription.Subscription, had []subscription.Period) {
	t.Helper()
	for _, r := range had[:len(had)-1] {
		if _, err := l.GenerateInvoice(context.Background(), sub.ID, ledger.ForPeriod(r.Start, r.End)); err != nil {
			t.Errorf("the clock announced %v to %v, but ForPeriod refused it: %v", r.Start, r.End, err)
		}
	}
}

// refused asserts ForPeriod takes the period for none the subscription had.
func refused(t *testing.T, l *ledger.Ledger, sub *subscription.Subscription, start, end time.Time) {
	t.Helper()
	ok, err := accepted(context.Background(), l, sub, subscription.Period{Start: start, End: end})
	if err != nil {
		t.Fatalf("ForPeriod %v to %v: %v", start, end, err)
	}
	if ok {
		t.Errorf("ForPeriod accepted %v to %v, which the clock never produced", start, end)
	}
}

// pauseScenario runs a subscription through daily ticks with pauses and
// resumes on the given days, and returns the periods it really had: every
// one the clock announced, oldest first, then the current one. It also checks
// that no period was announced twice.
func pauseScenario(t *testing.T, period plan.Period, created, until time.Time, clockOff func(time.Time) bool, events map[time.Time]string) (*ledger.Ledger, *subscription.Subscription, []subscription.Period) {
	t.Helper()
	ctx := context.Background()
	s := memory.New()
	l, ev, setNow := pauseFixture(s)
	p := planBilled(t, l, "plan-"+string(period), period)
	setNow(created)
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	for d := created.AddDate(0, 0, 1); !d.After(until); d = d.AddDate(0, 0, 1) {
		switch events[d] {
		case "pause":
			pauseAt(t, l, setNow, sub, d)
		case "resume":
			resumeAt(t, l, setNow, sub, d)
		}
		if clockOff == nil || !clockOff(d) {
			tick(t, l, setNow, d)
		}
	}
	var had []subscription.Period
	seen := map[string]bool{}
	for _, r := range ev.renewals {
		for _, e := range r.Ended {
			key := fmt.Sprint(e.Start, e.End)
			if seen[key] {
				t.Errorf("%v to %v announced twice", e.Start, e.End)
			}
			seen[key] = true
			had = append(had, e)
		}
	}
	cur := reload(t, l, sub)
	had = append(had, subscription.Period{Start: cur.CurrentPeriodStart, End: cur.CurrentPeriodEnd})
	for i := 1; i < len(had); i++ {
		if !had[i].Start.Equal(had[i-1].End) {
			t.Fatalf("periods %v and %v do not meet", had[i-1], had[i])
		}
	}
	return l, reload(t, l, sub), had
}

// Every real period around a pause is accepted and every neighbour refused:
// one pause, two pauses in different periods (the older stretch is forgotten,
// so the periods before it are refused rather than guessed), a pause across a
// period end with the clock off, and a yearly plan from 29 February.
func TestForPeriodAcrossPauses(t *testing.T) {
	var never func(time.Time) bool
	t.Run("one pause", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodMonthly, at(2026, 1, 31), at(2026, 9, 1), never,
			map[time.Time]string{at(2026, 3, 5): "pause", at(2026, 3, 10): "resume"})
		probePeriods(t, l, sub, had, 0)
	})
	t.Run("two pauses in one period", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodMonthly, at(2026, 1, 15), at(2026, 8, 1), never,
			map[time.Time]string{at(2026, 2, 1): "pause", at(2026, 2, 3): "resume", at(2026, 2, 5): "pause", at(2026, 2, 9): "resume"})
		probePeriods(t, l, sub, had, 0)
	})
	t.Run("two pauses in different periods", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodMonthly, at(2026, 1, 1), at(2026, 9, 1), never,
			map[time.Time]string{at(2026, 2, 5): "pause", at(2026, 2, 8): "resume", at(2026, 5, 20): "pause", at(2026, 6, 1): "resume"})
		// The second stretch's cadence began at the first stretch's end, so
		// January and the first stretched period are no longer provable.
		probePeriods(t, l, sub, had, 2)
	})
	t.Run("a pause across a period end with the clock off", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodMonthly, at(2026, 1, 1), at(2026, 7, 1),
			func(d time.Time) bool { return !d.Before(at(2026, 1, 20)) && d.Before(at(2026, 2, 15)) },
			map[time.Time]string{at(2026, 2, 5): "pause", at(2026, 2, 10): "resume"})
		if !had[0].End.Equal(at(2026, 2, 6)) {
			t.Errorf("first period ends %v, want 1 February stretched by five days", had[0].End)
		}
		probePeriods(t, l, sub, had, 0)
	})
	// A yearly stretch whose end lands on 29 February: the clock's anchor
	// settles on the 28th after it, and walking back from the 28th would
	// miss the real 29 February start and accept the 28th beside it.
	t.Run("yearly stretched to end on 29 February", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodYearly, at(2026, 2, 10), at(2032, 6, 1), never,
			map[time.Time]string{at(2027, 6, 1): "pause", at(2027, 6, 20): "resume"})
		if !had[1].End.Equal(at(2028, 2, 29)) {
			t.Fatalf("the stretched period ends %v, want 29 February 2028", had[1].End)
		}
		if !had[2].Start.Equal(at(2028, 2, 29)) || !had[2].End.Equal(at(2029, 2, 28)) {
			t.Fatalf("the period after it is %v, want 29 February 2028 to 28 February 2029", had[2])
		}
		invoiceEnded(t, l, sub, had)
		refused(t, l, sub, at(2028, 2, 28), at(2029, 2, 28)) // the phantom a walk back from the 28th lands on
		probePeriods(t, l, sub, had, 0)
	})
	t.Run("yearly from 29 February, stretched by a day to 29 February", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodYearly, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), at(2032, 6, 1), never,
			map[time.Time]string{at(2027, 6, 1): "pause", at(2027, 6, 2): "resume"})
		if !had[3].End.Equal(at(2028, 2, 29)) {
			t.Fatalf("the stretched period ends %v, want 29 February 2028", had[3].End)
		}
		invoiceEnded(t, l, sub, had)
		refused(t, l, sub, at(2028, 2, 28), at(2029, 2, 28))
		probePeriods(t, l, sub, had, 0)
	})
	t.Run("a later stretch whose floor is 29 February", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodYearly, at(2026, 2, 10), at(2033, 6, 1), never,
			map[time.Time]string{
				at(2027, 6, 1): "pause", at(2027, 6, 20): "resume", // ends 29 February 2028
				at(2030, 6, 1): "pause", at(2030, 6, 5): "resume",
			})
		floor := reload(t, l, sub).Stretch.Floor
		if floor == nil || !floor.Equal(at(2028, 2, 29)) {
			t.Fatalf("floor %v, want 29 February 2028", floor)
		}
		if !had[2].Start.Equal(at(2028, 2, 29)) || !had[2].End.Equal(at(2029, 2, 28)) {
			t.Fatalf("the period from the floor is %v, want 29 February 2028 to 28 February 2029", had[2])
		}
		// The real period the floor starts is provable, and its 28 February
		// look-alike is not.
		if _, err := l.GenerateInvoice(context.Background(), sub.ID, ledger.ForPeriod(had[2].Start, had[2].End)); err != nil {
			t.Errorf("the period from the floor: %v", err)
		}
		refused(t, l, sub, at(2028, 2, 28), at(2029, 2, 28))
		probePeriods(t, l, sub, had, 2)
	})
	t.Run("yearly from 29 February", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodYearly, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), at(2029, 6, 1), never,
			map[time.Time]string{at(2025, 6, 1): "pause", at(2025, 7, 1): "resume"})
		probePeriods(t, l, sub, had, 0)
	})
}

// A seeded random sweep: monthly and yearly plans created on the 28th to the
// 31st (and on 29 February), up to three pauses, and stretches of days with
// the clock off. Every period the clock announced is accepted, down to the
// floor of the last stretch, everything before the floor is refused, and
// every neighbour is refused.
func TestForPeriodRandomPauses(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261002, 1))
	scenarios, checks := 0, 0
	for scenarios < 160 {
		period := plan.PeriodMonthly
		years, maxPause := 0, 25
		if scenarios%2 == 1 {
			period, years, maxPause = plan.PeriodYearly, 6, 70
		}
		day := 28 + rng.IntN(4)
		month := time.Month(1 + rng.IntN(12))
		year := 2024 + rng.IntN(3)
		if period == plan.PeriodYearly && scenarios%6 == 5 {
			year, month = 2026, time.January
		}
		if scenarios%10 == 9 {
			year, month, day = 2024, time.February, 29
		}
		created := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if created.Day() != day {
			continue // no such day that month
		}
		until := created.AddDate(years, 30, 0)
		if period == plan.PeriodYearly {
			until = created.AddDate(years, 0, 0)
		}
		span := int(until.Sub(created).Hours() / 24)

		events := map[time.Time]string{}
		if period == plan.PeriodYearly && scenarios%6 == 5 && created.Year() == 2026 && created.Month() == time.January {
			// Aim the stretch at 29 February 2028: pause inside the period
			// that ends in January 2028 for exactly as long as it takes.
			end2028 := time.Date(2028, time.January, created.Day(), 0, 0, 0, 0, time.UTC)
			length := int(at(2028, 2, 29).Sub(end2028).Hours() / 24)
			pause := at(2027, 6, 1)
			events[pause] = "pause"
			events[pause.AddDate(0, 0, length)] = "resume"
		}
		cursor := 2
		if len(events) > 0 {
			cursor = int(at(2027, 8, 1).Sub(created).Hours() / 24)
		}
		for range 1 + rng.IntN(3) {
			start := cursor + rng.IntN(span/3)
			length := 1 + rng.IntN(maxPause)
			if start+length >= span-1 {
				break
			}
			events[created.AddDate(0, 0, start)] = "pause"
			events[created.AddDate(0, 0, start+length)] = "resume"
			cursor = start + length + 1
		}
		windows := rng.IntN(3)
		off := make([][2]time.Time, 0, windows)
		for range windows {
			from := created.AddDate(0, 0, 1+rng.IntN(span-2))
			off = append(off, [2]time.Time{from, from.AddDate(0, 0, 5+rng.IntN(85))})
		}
		clockOff := func(d time.Time) bool {
			for _, w := range off {
				if !d.Before(w[0]) && d.Before(w[1]) {
					return true
				}
			}
			return false
		}
		scenarios++

		name := fmt.Sprintf("%s from %s, %d events, %d clock-off windows", period, created.Format(time.DateOnly), len(events), len(off))
		t.Run(name, func(t *testing.T) {
			l, sub, had := pauseScenario(t, period, created, until, clockOff, events)
			provable := 0
			if st := sub.Stretch; st != nil && st.Floor != nil {
				for provable < len(had) && had[provable].Start.Before(*st.Floor) {
					provable++
				}
			}
			checks += probePeriods(t, l, sub, had, provable)
		})
	}
	t.Logf("%d scenarios, %d ForPeriod checks", scenarios, checks)
}
