package ledger_test

import (
	"context"
	"errors"
	"fmt"
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
func probePeriods(t *testing.T, l *ledger.Ledger, sub *subscription.Subscription, had []subscription.Period, provable int) {
	t.Helper()
	ctx := context.Background()
	for _, n := range neighbours(had) {
		ok, err := accepted(ctx, l, sub, n)
		if err != nil {
			t.Fatalf("probe %v to %v: %v", n.Start, n.End, err)
		}
		if ok {
			t.Errorf("phantom %v to %v accepted", n.Start, n.End)
		}
	}
	for i, r := range had[:len(had)-1] { // the current period is billed as the current one
		ok, err := accepted(ctx, l, sub, r)
		if err != nil {
			t.Fatalf("probe %v to %v: %v", r.Start, r.End, err)
		}
		if want := i >= provable; ok != want {
			t.Errorf("real period %d, %v to %v: accepted %v, want %v", i, r.Start, r.End, ok, want)
		}
	}
}

// pauseScenario runs a subscription through daily ticks with pauses and
// resumes on the given days, and returns the periods it really had: every
// one the clock announced, oldest first, then the current one. It also checks
// that no period was announced twice.
func pauseScenario(t *testing.T, period plan.Period, created, until time.Time, clockOff [2]time.Time, events map[time.Time]string) (*ledger.Ledger, *subscription.Subscription, []subscription.Period) {
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
		if d.Before(clockOff[0]) || !d.Before(clockOff[1]) {
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
	never := [2]time.Time{}
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
			[2]time.Time{at(2026, 1, 20), at(2026, 2, 15)},
			map[time.Time]string{at(2026, 2, 5): "pause", at(2026, 2, 10): "resume"})
		if !had[0].End.Equal(at(2026, 2, 6)) {
			t.Errorf("first period ends %v, want 1 February stretched by five days", had[0].End)
		}
		probePeriods(t, l, sub, had, 0)
	})
	t.Run("yearly from 29 February", func(t *testing.T) {
		l, sub, had := pauseScenario(t, plan.PeriodYearly, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), at(2029, 6, 1), never,
			map[time.Time]string{at(2025, 6, 1): "pause", at(2025, 7, 1): "resume"})
		probePeriods(t, l, sub, had, 0)
	})
}
