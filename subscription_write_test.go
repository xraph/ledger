package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// activePlan creates an active plan with one metered and one seat feature.
func activePlan(t *testing.T, l *ledger.Ledger, slug, app string, trialDays int) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Name: slug, Slug: slug, Currency: "usd", AppID: app, TrialDays: trialDays,
		Features: []plan.Feature{
			{Key: "api_calls", Name: "API calls", Type: plan.FeatureMetered, Limit: 1000, Period: plan.PeriodMonthly},
			{Key: "seats", Name: "Seats", Type: plan.FeatureSeat, Limit: 0, Period: plan.PeriodNone},
		},
		Pricing: &plan.Pricing{BaseAmount: types.USD(4900), BillingPeriod: plan.PeriodMonthly},
	}
	ctx := context.Background()
	if err := l.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := l.ActivatePlan(ctx, p.ID); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	return p
}

func TestCreateSubscriptionChecksThePlan(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	own := activePlan(t, l, "own", "app_1", 0)
	foreign := activePlan(t, l, "foreign", "app_2", 0)

	draft := &plan.Plan{Name: "draft", Slug: "draft", Currency: "usd", AppID: "app_1"}
	if err := l.CreatePlan(ctx, draft); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	cases := []struct {
		name   string
		planID id.PlanID
		want   error
	}{
		{"no plan", id.Nil, ledger.ErrInvalidInput},
		{"unknown plan", id.NewPlanID(), ledger.ErrPlanNotFound},
		{"another app's plan", foreign.ID, ledger.ErrInvalidInput},
		{"draft plan", draft.ID, ledger.ErrInvalidInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub := &subscription.Subscription{TenantID: "t1", PlanID: c.planID, AppID: "app_1"}
			if err := l.CreateSubscription(ctx, sub); !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}

	ok := &subscription.Subscription{TenantID: "t1", PlanID: own.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, ok); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if ok.Status != subscription.StatusActive {
		t.Errorf("status = %q, want active", ok.Status)
	}
}

func TestCreateSubscriptionStartsATrial(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	p := activePlan(t, l, "trial", "app_1", 14)

	sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if sub.Status != subscription.StatusTrialing || sub.TrialStart == nil || sub.TrialEnd == nil {
		t.Fatalf("got status %q trial %v..%v, want trialing with both ends", sub.Status, sub.TrialStart, sub.TrialEnd)
	}
	if got := sub.TrialEnd.Sub(*sub.TrialStart).Hours(); got != 14*24 {
		t.Errorf("trial length = %v hours, want %d", got, 14*24)
	}
}

func TestCreateSubscriptionValidatesQuantity(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	p := activePlan(t, l, "q", "app_1", 0)

	for name, q := range map[string]map[string]int64{
		"negative":        {"seats": -1},
		"not a seat":      {"api_calls": 5},
		"unknown feature": {"ghosts": 5},
	} {
		sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1", Quantity: q}
		if err := l.CreateSubscription(ctx, sub); !errors.Is(err, ledger.ErrInvalidInput) && !errors.Is(err, ledger.ErrInvalidQuantity) {
			t.Errorf("%s: got %v, want ErrInvalidInput or ErrInvalidQuantity", name, err)
		}
	}
}

func TestChangePlan(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	from := activePlan(t, l, "from", "app_1", 0)
	to := activePlan(t, l, "to", "app_1", 0)
	foreign := activePlan(t, l, "foreign", "app_2", 0)

	sub := &subscription.Subscription{TenantID: "t1", PlanID: from.ID, AppID: "app_1", Quantity: map[string]int64{"seats": 3}}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	got, err := l.ChangePlan(ctx, sub.ID, to.ID, nil)
	if err != nil {
		t.Fatalf("ChangePlan: %v", err)
	}
	if got.PlanID.String() != to.ID.String() || got.Quantity["seats"] != 3 {
		t.Errorf("got plan %s seats %d, want %s and the seats kept", got.PlanID, got.Quantity["seats"], to.ID)
	}

	got, err = l.ChangePlan(ctx, sub.ID, to.ID, map[string]int64{"seats": 8})
	if err != nil || got.Quantity["seats"] != 8 {
		t.Errorf("quantity update: got %v, %v", got, err)
	}

	if _, err := l.ChangePlan(ctx, sub.ID, foreign.ID, nil); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("another app's plan: got %v, want ErrInvalidInput", err)
	}

	if err := l.CancelSubscription(ctx, sub.ID, true); err != nil {
		t.Fatalf("CancelSubscription: %v", err)
	}
	if _, err := l.ChangePlan(ctx, sub.ID, from.ID, nil); !errors.Is(err, ledger.ErrSubscriptionCanceled) {
		t.Errorf("canceled subscription: got %v, want ErrSubscriptionCanceled", err)
	}
}

func TestPauseAndResume(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	p := activePlan(t, l, "pr", "app_1", 0)
	sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	if _, err := l.ResumeSubscription(ctx, sub.ID); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("resume an active subscription: got %v, want ErrInvalidInput", err)
	}
	got, err := l.PauseSubscription(ctx, sub.ID)
	if err != nil || got.Status != subscription.StatusPaused {
		t.Fatalf("pause: %v, %v", got, err)
	}
	if _, err = l.PauseSubscription(ctx, sub.ID); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("pause twice: got %v, want ErrInvalidInput", err)
	}
	got, err = l.ResumeSubscription(ctx, sub.ID)
	if err != nil || got.Status != subscription.StatusActive {
		t.Errorf("resume: %v, %v", got, err)
	}
}

func TestGenerateInvoiceRefusesASecondInvoiceForThePeriod(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	p := activePlan(t, l, "gen", "app_1", 0)
	sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	first, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Fatalf("second: got %v, want ErrAlreadyExists", err)
	}

	if err := l.MarkInvoiceVoided(ctx, first.ID, "wrong amount"); err != nil {
		t.Fatalf("void: %v", err)
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID); err != nil {
		t.Errorf("after voiding the first, regeneration must be allowed: %v", err)
	}
	// The regenerated invoice now covers the period, whatever order the store
	// returns the voided one in.
	if _, err := l.GenerateInvoice(ctx, sub.ID); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("third: got %v, want ErrAlreadyExists", err)
	}
}

// Two subscriptions for one tenant in one app can share a billing period, for
// example when an SDK caller aligns every period to the calendar month. Each
// one is billed on its own: the first invoice must not block the second.
func TestGenerateInvoiceBillsTwoSubscriptionsSharingAPeriod(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)
	p := activePlan(t, l, "shared", "app_1", 0)

	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	subs := make([]*subscription.Subscription, 2)
	for i := range subs {
		subs[i] = &subscription.Subscription{
			Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
			TenantID: "t1", PlanID: p.ID, AppID: "app_1",
			Status:             subscription.StatusActive,
			CurrentPeriodStart: start, CurrentPeriodEnd: end,
		}
		if err := s.CreateSubscription(ctx, subs[i]); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}
	}

	for i, sub := range subs {
		inv, err := l.GenerateInvoice(ctx, sub.ID)
		if err != nil {
			t.Fatalf("subscription %d: %v", i, err)
		}
		if inv.SubscriptionID.String() != sub.ID.String() {
			t.Errorf("subscription %d: invoice belongs to %s", i, inv.SubscriptionID)
		}
	}
	for i, sub := range subs {
		if _, err := l.GenerateInvoice(ctx, sub.ID); !errors.Is(err, ledger.ErrAlreadyExists) {
			t.Errorf("second generate on subscription %d: got %v, want ErrAlreadyExists", i, err)
		}
	}
}

func TestCancelSubscriptionRefusesAnEndedSubscription(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)
	p := activePlan(t, l, "ended", "app_1", 0)

	t.Run("canceled", func(t *testing.T) {
		sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
		if err := l.CreateSubscription(ctx, sub); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}
		if err := l.CancelSubscription(ctx, sub.ID, true); err != nil {
			t.Fatalf("first cancel: %v", err)
		}
		before, err := s.GetSubscription(ctx, sub.ID)
		if err != nil {
			t.Fatalf("GetSubscription: %v", err)
		}

		for _, immediately := range []bool{false, true} {
			if err = l.CancelSubscription(ctx, sub.ID, immediately); !errors.Is(err, ledger.ErrSubscriptionCanceled) {
				t.Errorf("cancel again (immediately=%v): got %v, want ErrSubscriptionCanceled", immediately, err)
			}
		}

		after, err := s.GetSubscription(ctx, sub.ID)
		if err != nil {
			t.Fatalf("GetSubscription: %v", err)
		}
		if after.Status != subscription.StatusCanceled || after.CancelAt == nil || !after.CancelAt.Equal(*before.CancelAt) {
			t.Errorf("a refused cancel changed the row: status %q cancel_at %v, want canceled and %v",
				after.Status, after.CancelAt, before.CancelAt)
		}
	})

	t.Run("expired", func(t *testing.T) {
		sub := &subscription.Subscription{
			Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
			TenantID: "t2", PlanID: p.ID, AppID: "app_1", Status: subscription.StatusExpired,
		}
		if err := s.CreateSubscription(ctx, sub); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}
		if err := l.CancelSubscription(ctx, sub.ID, false); !errors.Is(err, ledger.ErrSubscriptionExpired) {
			t.Errorf("got %v, want ErrSubscriptionExpired", err)
		}
		after, err := s.GetSubscription(ctx, sub.ID)
		if err != nil {
			t.Fatalf("GetSubscription: %v", err)
		}
		if after.CancelAt != nil {
			t.Errorf("a refused cancel set cancel_at to %v", after.CancelAt)
		}
	})
}
