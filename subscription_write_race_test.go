package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

// clockBetween is a memory store that runs the lifecycle clock's write in the
// gap between an operator's read and its write: inside the store call that
// does the write, just before it. That is the interleaving a replica running
// the clock can produce, made certain.
type clockBetween struct {
	*memory.Store
	clock func()
}

func (c *clockBetween) run() {
	if c.clock != nil {
		c.clock()
		c.clock = nil
	}
}

func (c *clockBetween) CancelSubscription(ctx context.Context, subID id.SubscriptionID, cancelAt time.Time) error {
	c.run()
	return c.Store.CancelSubscription(ctx, subID, cancelAt)
}

func (c *clockBetween) PauseSubscription(ctx context.Context, subID id.SubscriptionID) (bool, error) {
	c.run()
	return c.Store.PauseSubscription(ctx, subID)
}

func (c *clockBetween) ResumeSubscription(ctx context.Context, subID id.SubscriptionID) (bool, error) {
	c.run()
	return c.Store.ResumeSubscription(ctx, subID)
}

func (c *clockBetween) ChangeSubscriptionPlan(ctx context.Context, subID id.SubscriptionID, planID id.PlanID, quantity map[string]int64) (bool, error) {
	c.run()
	return c.Store.ChangeSubscriptionPlan(ctx, subID, planID, quantity)
}

// clockFixture is an engine over a clockBetween store with one subscription
// on an active plan, shaped by edit and written straight to the store.
func clockFixture(t *testing.T, edit func(*subscription.Subscription)) (*ledger.Ledger, *clockBetween, *subscription.Subscription) {
	t.Helper()
	st := &clockBetween{Store: memory.New()}
	l := ledger.New(st)
	p := activePlan(t, l, "clock", "app_1", 0)
	now := time.Now().UTC()
	sub := &subscription.Subscription{
		ID: id.NewSubscriptionID(), TenantID: "t1", PlanID: p.ID, AppID: "app_1",
		Status: subscription.StatusActive, CurrentPeriodStart: now, CurrentPeriodEnd: now.AddDate(0, 1, 0),
	}
	edit(sub)
	if err := st.CreateSubscription(context.Background(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	return l, st, sub
}

func storedSub(t *testing.T, st *clockBetween, subID id.SubscriptionID) *subscription.Subscription {
	t.Helper()
	got, err := st.GetSubscription(context.Background(), subID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	return got
}

func TestResumeDoesNotReviveASubscriptionTheClockCanceled(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	l, st, sub := clockFixture(t, func(x *subscription.Subscription) {
		x.Status = subscription.StatusPaused
		x.CancelAt = &past
	})
	st.clock = func() {
		if ok, err := st.EnactSubscriptionCancel(ctx, sub.ID, now); err != nil || !ok {
			t.Fatalf("EnactSubscriptionCancel: %v, %v", ok, err)
		}
	}

	if _, err := l.ResumeSubscription(ctx, sub.ID); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("resume after the clock canceled it: got %v, want ErrInvalidInput", err)
	}
	if got := storedSub(t, st, sub.ID); got.Status != subscription.StatusCanceled {
		t.Errorf("the resume revived it: status %q, want canceled", got.Status)
	}
}

func TestPauseKeepsAPeriodTheClockAdvanced(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	next := past.AddDate(0, 1, 0)
	l, st, sub := clockFixture(t, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = past.AddDate(0, -1, 0), past
	})
	st.clock = func() {
		if ok, err := st.AdvanceSubscriptionPeriod(ctx, sub.ID, past, next, now); err != nil || !ok {
			t.Fatalf("AdvanceSubscriptionPeriod: %v, %v", ok, err)
		}
	}

	got, err := l.PauseSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("PauseSubscription: %v", err)
	}
	stored := storedSub(t, st, sub.ID)
	if stored.Status != subscription.StatusPaused || !stored.CurrentPeriodEnd.Equal(next) {
		t.Errorf("stored status %q period end %v, want paused and the advanced end %v", stored.Status, stored.CurrentPeriodEnd, next)
	}
	if !got.CurrentPeriodEnd.Equal(next) {
		t.Errorf("PauseSubscription answered period end %v, want the advanced %v", got.CurrentPeriodEnd, next)
	}
}

func TestChangePlanKeepsAPeriodTheClockAdvanced(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	next := past.AddDate(0, 1, 0)
	l, st, sub := clockFixture(t, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = past.AddDate(0, -1, 0), past
	})
	to := activePlan(t, l, "to", "app_1", 0)
	st.clock = func() {
		if ok, err := st.AdvanceSubscriptionPeriod(ctx, sub.ID, past, next, now); err != nil || !ok {
			t.Fatalf("AdvanceSubscriptionPeriod: %v, %v", ok, err)
		}
	}

	got, err := l.ChangePlan(ctx, sub.ID, to.ID, map[string]int64{"seats": 4})
	if err != nil {
		t.Fatalf("ChangePlan: %v", err)
	}
	stored := storedSub(t, st, sub.ID)
	if stored.PlanID.String() != to.ID.String() || stored.Quantity["seats"] != 4 || !stored.CurrentPeriodEnd.Equal(next) {
		t.Errorf("stored plan %s seats %d period end %v, want %s, 4 and the advanced end %v",
			stored.PlanID, stored.Quantity["seats"], stored.CurrentPeriodEnd, to.ID, next)
	}
	if !got.CurrentPeriodEnd.Equal(next) || got.PlanID.String() != to.ID.String() {
		t.Errorf("ChangePlan answered plan %s period end %v", got.PlanID, got.CurrentPeriodEnd)
	}
}

func TestChangePlanRefusesASubscriptionTheClockCanceled(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	l, st, sub := clockFixture(t, func(x *subscription.Subscription) { x.CancelAt = &past })
	to := activePlan(t, l, "to", "app_1", 0)
	st.clock = func() {
		if ok, err := st.EnactSubscriptionCancel(ctx, sub.ID, now); err != nil || !ok {
			t.Fatalf("EnactSubscriptionCancel: %v, %v", ok, err)
		}
	}

	if _, err := l.ChangePlan(ctx, sub.ID, to.ID, nil); !errors.Is(err, ledger.ErrSubscriptionCanceled) {
		t.Errorf("change the plan after the clock canceled it: got %v, want ErrSubscriptionCanceled", err)
	}
	if got := storedSub(t, st, sub.ID); got.PlanID.String() != sub.PlanID.String() {
		t.Errorf("a canceled subscription moved to plan %s", got.PlanID)
	}
}

func TestCancelRefusesASubscriptionTheClockCanceled(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	l, st, sub := clockFixture(t, func(x *subscription.Subscription) { x.CancelAt = &past })
	st.clock = func() {
		if ok, err := st.EnactSubscriptionCancel(ctx, sub.ID, now); err != nil || !ok {
			t.Fatalf("EnactSubscriptionCancel: %v, %v", ok, err)
		}
	}

	if err := l.CancelSubscription(ctx, sub.ID, true); !errors.Is(err, ledger.ErrSubscriptionCanceled) {
		t.Errorf("cancel after the clock canceled it: got %v, want ErrSubscriptionCanceled", err)
	}
	got := storedSub(t, st, sub.ID)
	if got.Status != subscription.StatusCanceled || got.CanceledAt == nil || !got.CanceledAt.Equal(past) {
		t.Errorf("status %q canceled_at %v, want canceled at the clock's %v", got.Status, got.CanceledAt, past)
	}
}
