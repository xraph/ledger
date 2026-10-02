package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

// pauseFixture is an engine on a clock the test moves, with the event
// recorder, over the given memory store.
func pauseFixture(s ledgerstore.Store) (*ledger.Ledger, *lifecycleEvents, func(time.Time)) {
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
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

// Three months paused: no tick in or after them lists a paused month, the
// period restarts at the resume and renews on that day, and ForPeriod refuses
// every paused month however it is named.
func TestAPauseFreezesTheBillingCycle(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l, ev, setNow := pauseFixture(s)
	p := activePlan(t, l, "pro", "app_1", 0)
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	pausedAt := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	setNow(pausedAt)
	paused, err := l.PauseSubscription(ctx, sub.ID)
	if err != nil || paused.PausedAt == nil || !paused.PausedAt.Equal(pausedAt) {
		t.Fatalf("pause: %+v, %v; want paused_at %v", paused, err, pausedAt)
	}
	for _, at := range []time.Time{at(2026, 4, 2), at(2026, 5, 2), at(2026, 6, 2)} {
		tick(t, l, setNow, at)
	}
	if n := ev.count(&ev.renewed); n != 0 {
		t.Fatalf("%d renewals while paused, want none", n)
	}

	resumedAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	setNow(resumedAt)
	resumed, err := l.ResumeSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("ResumeSubscription: %v", err)
	}
	if resumed.Status != subscription.StatusActive || resumed.PausedAt != nil ||
		resumed.ResumedAt == nil || !resumed.ResumedAt.Equal(resumedAt) {
		t.Errorf("resumed: status %s paused_at %v resumed_at %v", resumed.Status, resumed.PausedAt, resumed.ResumedAt)
	}
	samePeriod(t, resumed, resumedAt, time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC))

	tick(t, l, setNow, time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC))
	if n := ev.count(&ev.renewed); n != 0 {
		t.Fatalf("%d renewals the day after the resume, want none", n)
	}

	for _, month := range []subscription.Period{
		{Start: time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC), End: resumedAt},                                   // on the new anchor
		{Start: time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)}, // on the old one
		{Start: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)}, // running at the pause
	} {
		if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(month.Start, month.End)); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Errorf("ForPeriod %v to %v: got %v, want ErrInvalidInput", month.Start, month.End, err)
		}
	}

	tick(t, l, setNow, time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC))
	if len(ev.renewals) != 1 {
		t.Fatalf("renewals %+v, want one", ev.renewals)
	}
	ended := ev.renewals[0].Ended
	if len(ended) != 1 || !ended[0].Start.Equal(resumedAt) || !ended[0].End.Equal(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("ended %+v, want only 15 June to 15 July", ended)
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(ended[0].Start, ended[0].End)); err != nil {
		t.Errorf("ForPeriod of the first period after the resume: %v", err)
	}
}

// A trial still running at the pause resumes as a trial whose end has moved on
// by the length of the pause. The clock ends it through the ordinary trial step
// once that later date passes, and not before.
func TestResumeMovesATrialEndOnByThePause(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l, ev, setNow := pauseFixture(s)
	p := activePlan(t, l, "trial", "app_1", 14)
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if sub.TrialEnd == nil || !sub.TrialEnd.Equal(time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("trial end %v, want 15 March 10:00", sub.TrialEnd)
	}

	setNow(time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)) // ten days of trial left
	if _, err := l.PauseSubscription(ctx, sub.ID); err != nil {
		t.Fatalf("PauseSubscription: %v", err)
	}
	tick(t, l, setNow, at(2026, 3, 20)) // the old trial end passes while paused
	if n := ev.count(&ev.trials); n != 0 {
		t.Fatalf("%d trial ends while paused, want none", n)
	}

	setNow(time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC))
	resumed, err := l.ResumeSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("ResumeSubscription: %v", err)
	}
	wantEnd := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	if resumed.Status != subscription.StatusTrialing || resumed.TrialEnd == nil || !resumed.TrialEnd.Equal(wantEnd) {
		t.Fatalf("resumed as %s with trial end %v, want trialing until %v", resumed.Status, resumed.TrialEnd, wantEnd)
	}

	tick(t, l, setNow, wantEnd.Add(-time.Second))
	if n := ev.count(&ev.trials); n != 0 {
		t.Fatalf("the trial ended before its moved end")
	}
	tick(t, l, setNow, wantEnd)
	if n := ev.count(&ev.trials); n != 1 {
		t.Fatalf("%d trial ends at the moved end, want one", n)
	}
	if got := reload(t, l, sub); got.Status != subscription.StatusActive {
		t.Errorf("status %s after the trial, want active", got.Status)
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
		x.TrialEnd = &trialEnd
		x.PausedAt = ptr(at(2026, 2, 10))
	})
	setNow(at(2026, 3, 10))
	got, err := l.ResumeSubscription(ctx, sub.ID)
	if err != nil || got.Status != subscription.StatusActive || !got.TrialEnd.Equal(trialEnd) {
		t.Errorf("resume: %+v, %v; want active with the trial end untouched", got, err)
	}
}

// A subscription paused before Ledger recorded paused_at has no pause start.
// Its period still restarts, but its trial end stays put, and it resumes as a
// trial only while that end is ahead.
func TestResumeOfAPauseWithNoPausedAt(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l, _, setNow := pauseFixture(s)
	p := activePlan(t, l, "pro", "app_1", 0)
	trialEnd := at(2026, 3, 20)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusPaused
		x.TrialEnd = &trialEnd
	})
	now := at(2026, 3, 10)
	setNow(now)
	got, err := l.ResumeSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("ResumeSubscription: %v", err)
	}
	if got.Status != subscription.StatusTrialing || !got.TrialEnd.Equal(trialEnd) {
		t.Errorf("status %s trial end %v, want trialing until the unmoved %v", got.Status, got.TrialEnd, trialEnd)
	}
	samePeriod(t, got, now, at(2026, 4, 10))
}

// A resume and a second pause landing between the engine's read and its write
// make the write miss; the engine starts again from the new pause.
func TestResumeStartsAgainWhenPausedAgainMeanwhile(t *testing.T) {
	ctx := context.Background()
	st := &clockBetween{Store: memory.New()}
	l, _, setNow := pauseFixture(st)
	p := activePlan(t, l, "trial", "app_1", 30)
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"} // trial to 31 March
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if _, err := st.Store.PauseSubscription(ctx, sub.ID, at(2026, 3, 11)); err != nil {
		t.Fatalf("pause: %v", err)
	}
	secondPause := time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC)
	st.clock = func() {
		read, _ := st.Store.GetSubscription(ctx, sub.ID)
		r := subscription.Resume{
			PausedAt: read.PausedAt, At: at(2026, 3, 20), Status: subscription.StatusTrialing,
			PeriodStart: at(2026, 3, 20), PeriodEnd: at(2026, 4, 20), TrialEnd: ptr(time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)),
		}
		if ok, err := st.Store.ResumeSubscription(ctx, sub.ID, r); err != nil || !ok {
			t.Fatalf("the other resume: %v, %v", ok, err)
		}
		if ok, err := st.Store.PauseSubscription(ctx, sub.ID, secondPause); err != nil || !ok {
			t.Fatalf("the second pause: %v, %v", ok, err)
		}
	}

	now := time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC)
	setNow(now)
	got, err := l.ResumeSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("ResumeSubscription: %v", err)
	}
	// 9 April 10:00 moved on by the ten days of the second pause.
	if want := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC); got.TrialEnd == nil || !got.TrialEnd.Equal(want) {
		t.Errorf("trial end %v, want %v", got.TrialEnd, want)
	}
	if got.ResumedAt == nil || !got.ResumedAt.Equal(now) {
		t.Errorf("resumed_at %v, want %v", got.ResumedAt, now)
	}
}
