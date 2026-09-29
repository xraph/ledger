package ledger_test

import (
	"context"
	"errors"
	"testing"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

func validPlan(slug, app string) *plan.Plan {
	return &plan.Plan{
		Name: "Pro " + slug, Slug: slug, Currency: "USD", AppID: app,
		Features: []plan.Feature{{Key: "api_calls", Name: "API calls", Type: plan.FeatureMetered, Limit: 1000, Period: plan.PeriodMonthly}},
		Pricing: &plan.Pricing{
			BaseAmount: types.USD(4900), BillingPeriod: plan.PeriodMonthly,
			Tiers: []plan.PriceTier{{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: -1, UnitAmount: types.USD(3)}},
		},
	}
}

func TestCreatePlanNormalisesAndDefaults(t *testing.T) {
	l := ledger.New(memory.New())
	p := validPlan("pro", "app_1")
	if err := l.CreatePlan(context.Background(), p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if p.Currency != "usd" {
		t.Errorf("currency = %q, want usd", p.Currency)
	}
	if p.Status != plan.StatusDraft {
		t.Errorf("status = %q, want draft", p.Status)
	}
	if p.Features[0].ID.IsNil() || p.Pricing.ID.IsNil() {
		t.Error("feature and pricing ids must be assigned")
	}
	if p.Pricing.PlanID.String() != p.ID.String() {
		t.Error("pricing must point at its plan")
	}
}

func TestCreatePlanRejectsInvalidPlans(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*plan.Plan)
		want   error
	}{
		{"no name", func(p *plan.Plan) { p.Name = " " }, ledger.ErrInvalidInput},
		{"no slug", func(p *plan.Plan) { p.Slug = "" }, ledger.ErrInvalidInput},
		{"no currency", func(p *plan.Plan) { p.Currency = "" }, ledger.ErrInvalidInput},
		{"negative trial", func(p *plan.Plan) { p.TrialDays = -1 }, ledger.ErrInvalidInput},
		{"unknown status", func(p *plan.Plan) { p.Status = "retired" }, ledger.ErrInvalidInput},
		{"duplicate feature key", func(p *plan.Plan) { p.Features = append(p.Features, p.Features[0]) }, ledger.ErrDuplicateFeature},
		{"feature without key", func(p *plan.Plan) { p.Features[0].Key = "" }, ledger.ErrInvalidInput},
		{"unknown feature type", func(p *plan.Plan) { p.Features[0].Type = "tiered" }, ledger.ErrInvalidInput},
		{"limit below -1", func(p *plan.Plan) { p.Features[0].Limit = -2 }, ledger.ErrInvalidInput},
		{"negative base price", func(p *plan.Plan) { p.Pricing.BaseAmount = types.USD(-1) }, ledger.ErrInvalidPricing},
		{"base price in another currency", func(p *plan.Plan) { p.Pricing.BaseAmount = types.EUR(4900) }, ledger.ErrInvalidPricing},
		{"tier for an unknown feature", func(p *plan.Plan) { p.Pricing.Tiers[0].FeatureKey = "ghost" }, ledger.ErrInvalidPricing},
		{"invalid ladder", func(p *plan.Plan) {
			p.Pricing.Tiers = append(p.Pricing.Tiers, plan.PriceTier{FeatureKey: "api_calls", Type: plan.TierFlat, UpTo: 500, FlatAmount: types.USD(100)})
		}, invoice.ErrInvalidTiers},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := ledger.New(memory.New())
			p := validPlan("pro", "app_1")
			c.mutate(p)
			if err := l.CreatePlan(context.Background(), p); !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestCreatePlanRejectsATakenSlug(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	if err := l.CreatePlan(ctx, validPlan("pro", "app_1")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := l.CreatePlan(ctx, validPlan("pro", "app_1")); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("same app: got %v, want ErrAlreadyExists", err)
	}
	if err := l.CreatePlan(ctx, validPlan("pro", "app_2")); err != nil {
		t.Errorf("another app may reuse the slug: %v", err)
	}
}

func TestUpdatePlan(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	p := validPlan("pro", "app_1")
	if err := l.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	created := p.CreatedAt

	edit := *p
	edit.Name = "Pro Plus"
	if err := l.UpdatePlan(ctx, &edit); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	got, _ := l.GetPlan(ctx, p.ID)
	if got.Name != "Pro Plus" || !got.CreatedAt.Equal(created) {
		t.Errorf("got name %q created %v, want Pro Plus and the original creation time", got.Name, got.CreatedAt)
	}

	for _, m := range []struct {
		name   string
		mutate func(*plan.Plan)
	}{
		{"app", func(x *plan.Plan) { x.AppID = "app_2" }},
		{"currency", func(x *plan.Plan) {
			x.Currency = "eur"
			x.Pricing.BaseAmount = types.EUR(4900)
			x.Pricing.Tiers[0].UnitAmount = types.EUR(3)
		}},
	} {
		bad := *got
		m.mutate(&bad)
		if err := l.UpdatePlan(ctx, &bad); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Errorf("changing %s: got %v, want ErrInvalidInput", m.name, err)
		}
	}
}

func TestARefusedUpdateLeavesTheStoredPlanUnchanged(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)
	p := validPlan("pro", "app_1")
	if err := l.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	loaded, err := l.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	loaded.Currency = "eur"
	loaded.Pricing.BaseAmount = types.EUR(1)
	loaded.Features[0].Limit = 5
	if err := l.UpdatePlan(ctx, loaded); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("currency change: got %v, want ErrInvalidInput", err)
	}

	stored, _ := s.GetPlan(ctx, p.ID)
	if stored.Currency != "usd" || stored.Pricing.BaseAmount.Amount != 4900 || stored.Features[0].Limit != 1000 {
		t.Errorf("a refused update changed the stored plan: %+v", stored)
	}
}

func TestArchiveAndActivatePlan(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	p := validPlan("pro", "app_1")
	if err := l.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := l.ActivatePlan(ctx, p.ID); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	if got, _ := l.GetPlan(ctx, p.ID); got.Status != plan.StatusActive {
		t.Errorf("after activate: %q", got.Status)
	}
	if err := l.ArchivePlan(ctx, p.ID); err != nil {
		t.Fatalf("ArchivePlan: %v", err)
	}
	if err := l.ArchivePlan(ctx, p.ID); err != nil {
		t.Errorf("archiving twice must be a no-op, got %v", err)
	}
	if got, _ := l.GetPlan(ctx, p.ID); got.Status != plan.StatusArchived {
		t.Errorf("after archive: %q", got.Status)
	}
}

func TestDeletePlanRefusesWhileInUse(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)
	p := validPlan("pro", "app_1")
	if err := l.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	sub := &subscription.Subscription{ID: id.NewSubscriptionID(), TenantID: "t1", PlanID: p.ID, Status: subscription.StatusCanceled, AppID: "app_1"}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err := l.DeletePlan(ctx, p.ID); !errors.Is(err, ledger.ErrPlanInUse) {
		t.Errorf("in use (even by a canceled subscription): got %v, want ErrPlanInUse", err)
	}

	unused := validPlan("spare", "app_1")
	if err := l.CreatePlan(ctx, unused); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := l.DeletePlan(ctx, unused.ID); err != nil {
		t.Errorf("unused plan: %v", err)
	}
	if _, err := l.GetPlan(ctx, unused.ID); !errors.Is(err, ledger.ErrPlanNotFound) {
		t.Errorf("after delete: got %v, want ErrPlanNotFound", err)
	}
}
