package ledger_test

import (
	"context"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

// A cancel dated beyond a period that has not caught up (an imported
// subscription's provider cancel_at, say) catches the period up before it is
// enacted, even when the cancel step runs on its own, as another replica's can
// between this one's period list and its write. The periods before the cancel
// are announced, and the subscription ends in the period the cancel falls in.
func TestEnactCancelsCatchesThePeriodUpFirst(t *testing.T) {
	ctx := context.Background()
	l, s, ev, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CancelAt = ptr(at(2026, 3, 15))
	})

	enacted, err := l.EnactCancels(ctx, at(2026, 4, 1))
	if err != nil || len(enacted) != 1 {
		t.Fatalf("EnactCancels: %v, %v", enacted, err)
	}
	if len(ev.renewals) != 1 {
		t.Fatalf("renewals %+v, want one", ev.renewals)
	}
	ended := ev.renewals[0].Ended
	if len(ended) != 2 || !ended[0].Start.Equal(at(2026, 1, 1)) || !ended[1].End.Equal(at(2026, 3, 1)) {
		t.Errorf("ended %+v, want January and February", ended)
	}
	got := reload(t, l, sub)
	samePeriod(t, got, at(2026, 3, 1), at(2026, 4, 1))
	if got.Status != subscription.StatusCanceled || got.CanceledAt == nil || !got.CanceledAt.Equal(at(2026, 3, 15)) {
		t.Errorf("status %s canceled_at %v, want canceled on 15 March", got.Status, got.CanceledAt)
	}
}

// operatorBetween is a memory store that runs an operator's write just before
// each of the clock's period advances and trial ends, after the clock listed
// the row.
type operatorBetween struct {
	*memory.Store
	operator func(id.SubscriptionID)
}

func (s *operatorBetween) AdvanceSubscriptionPeriod(ctx context.Context, subID id.SubscriptionID, from, start, end, now time.Time) (bool, error) {
	s.operator(subID)
	return s.Store.AdvanceSubscriptionPeriod(ctx, subID, from, start, end, now)
}

func (s *operatorBetween) EndSubscriptionTrial(ctx context.Context, subID id.SubscriptionID, now time.Time) (bool, error) {
	s.operator(subID)
	return s.Store.EndSubscriptionTrial(ctx, subID, now)
}

// The clock's hooks announce the row as stored after their write, so a plan
// change landing between the list and the write shows in the payload.
func TestLifecycleHooksAnnounceTheStoredRow(t *testing.T) {
	ctx := context.Background()
	st := &operatorBetween{Store: memory.New()}
	ev := &lifecycleEvents{}
	l := ledger.New(st, ledger.WithPlugin(ev))
	p := activePlan(t, l, "pro", "app_1", 0)
	next := activePlan(t, l, "max", "app_1", 0)
	st.operator = func(subID id.SubscriptionID) {
		if _, err := st.ChangeSubscriptionPlan(ctx, subID, next.ID, nil); err != nil {
			t.Fatalf("ChangeSubscriptionPlan: %v", err)
		}
	}
	renewing := seedSub(t, st.Store, p, func(*subscription.Subscription) {})
	trialing := seedSub(t, st.Store, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusTrialing
		x.CurrentPeriodEnd = at(2026, 6, 1)
		x.TrialEnd = ptr(at(2026, 2, 1))
	})
	var trialSubs []*subscription.Subscription
	trialHook := &trialRecorder{got: &trialSubs}
	l = ledger.New(st, ledger.WithPlugin(ev), ledger.WithPlugin(trialHook))

	if _, err := l.Advance(ctx, at(2026, 3, 1)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(ev.renewals) != 1 || ev.renewals[0].Subscription.ID.String() != renewing.ID.String() ||
		ev.renewals[0].Subscription.PlanID.String() != next.ID.String() {
		t.Errorf("renewal announced %+v, want the stored row on the new plan", ev.renewals)
	}
	if len(trialSubs) != 1 || trialSubs[0].ID.String() != trialing.ID.String() ||
		trialSubs[0].PlanID.String() != next.ID.String() || trialSubs[0].Status != subscription.StatusActive {
		t.Errorf("trial end announced %+v, want the stored active row on the new plan", trialSubs)
	}
}

type trialRecorder struct{ got *[]*subscription.Subscription }

func (r *trialRecorder) Name() string { return "trial-recorder" }

func (r *trialRecorder) OnSubscriptionTrialEnded(_ context.Context, sub interface{}) error {
	*r.got = append(*r.got, sub.(*subscription.Subscription))
	return nil
}

// A scheduled cancel that lands after the clock advanced the period announces
// the stored row: its cancel_at and its current period end agree.
func TestCancelScheduledAnnouncesTheStoredRow(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	next := past.AddDate(0, 1, 0)
	st := &clockBetween{Store: memory.New()}
	ev := &lifecycleEvents{}
	l := ledger.New(st, ledger.WithPlugin(ev))
	p := activePlan(t, l, "clock", "app_1", 0)
	sub := &subscription.Subscription{
		ID: id.NewSubscriptionID(), TenantID: "t1", PlanID: p.ID, AppID: "app_1",
		Status: subscription.StatusActive, CurrentPeriodStart: past.AddDate(0, -1, 0), CurrentPeriodEnd: past,
	}
	if err := st.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	st.clock = func() {
		if ok, err := st.AdvanceSubscriptionPeriod(ctx, sub.ID, past, past, next, now); err != nil || !ok {
			t.Fatalf("AdvanceSubscriptionPeriod: %v, %v", ok, err)
		}
	}
	if err := l.CancelSubscription(ctx, sub.ID, false); err != nil {
		t.Fatalf("CancelSubscription: %v", err)
	}
	if len(ev.scheduledSubs) != 1 {
		t.Fatalf("scheduled %d, want 1", len(ev.scheduledSubs))
	}
	got := ev.scheduledSubs[0]
	if got.CancelAt == nil || !got.CancelAt.Equal(next) || !got.CurrentPeriodEnd.Equal(next) {
		t.Errorf("announced cancel_at %v with period end %v, want both %v", got.CancelAt, got.CurrentPeriodEnd, next)
	}
}

// CreateSubscription cuts its stamps to the millisecond, the precision every
// backend keeps, so the caller's struct names the same period the store holds.
func TestCreateSubscriptionStampsToTheMillisecond(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	now := time.Date(2026, 3, 1, 10, 0, 0, 123456789, time.UTC)
	l := ledger.New(s, ledger.WithClock(func() time.Time { return now }))
	p := activePlan(t, l, "pro", "app_1", 7)
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	want := time.Date(2026, 3, 1, 10, 0, 0, 123000000, time.UTC)
	if !sub.CreatedAt.Equal(want) || !sub.CurrentPeriodStart.Equal(want) || !sub.TrialStart.Equal(want) {
		t.Errorf("created %v, period from %v, trial from %v; want all %v", sub.CreatedAt, sub.CurrentPeriodStart, sub.TrialStart, want)
	}
}
