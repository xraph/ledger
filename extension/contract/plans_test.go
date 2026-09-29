package contract

import (
	"context"
	"errors"
	"testing"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/types"
)

func TestPlansManifest(t *testing.T) {
	assertIntents(t, map[string]string{
		"plans.list": "query", "plans.detail": "query",
		"plans.create": "command", "plans.update": "command", "plans.archive": "command",
		"plans.activate": "command", "plans.delete": "command", "plans.syncToProvider": "command",
	})
	assertInvalidates(t, map[string][]string{
		"plans.create":         {"plans.list", "overview.stats"},
		"plans.update":         {"plans.list", "plans.detail", "subscriptions.detail", "subscriptions.usage"},
		"plans.archive":        {"plans.list", "plans.detail", "overview.stats"},
		"plans.activate":       {"plans.list", "plans.detail", "overview.stats"},
		"plans.delete":         {"plans.list", "overview.stats"},
		"plans.syncToProvider": {"plans.detail"},
	})
}

func TestPlansListPagesWithinTheApp(t *testing.T) {
	h := newHarness(t)
	for _, slug := range []string{"a", "b", "c"} {
		h.activePlan("app_a", slug)
	}
	h.activePlan("app_b", "other")

	first := mustCall(h, "app_a", plansList, PlansListInput{PageInput: PageInput{Limit: 2}})
	if len(first.Items) != 2 || !first.HasMore {
		t.Errorf("first page: %d items, has_more %v; want 2 and true", len(first.Items), first.HasMore)
	}
	second := mustCall(h, "app_a", plansList, PlansListInput{PageInput: PageInput{Limit: 2, Offset: 2}})
	if len(second.Items) != 1 || second.HasMore {
		t.Errorf("second page: %d items, has_more %v; want 1 and false", len(second.Items), second.HasMore)
	}
	past := mustCall(h, "app_a", plansList, PlansListInput{PageInput: PageInput{Offset: 50}})
	if past.Items == nil || len(past.Items) != 0 || past.HasMore {
		t.Errorf("past the end: %+v, want empty non-nil items", past)
	}
	for _, p := range append(first.Items, second.Items...) {
		if p.AppID != "app_a" {
			t.Errorf("app_a listed plan %s from %s", p.ID, p.AppID)
		}
	}
	if b := mustCall(h, "app_b", plansList, PlansListInput{}); len(b.Items) != 1 {
		t.Errorf("app_b sees %d plans, want 1", len(b.Items))
	}
}

func TestPlansListRejectsAnUnknownStatus(t *testing.T) {
	h := newHarness(t)
	if _, err := call(h, "app_a", plansList, PlansListInput{Status: "retired"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("got %v, want BAD_REQUEST", err)
	}
}

func TestPlansDetailScopingAndWireShape(t *testing.T) {
	h := newHarness(t)
	own := h.activePlan("app_a", "own")
	foreign := h.activePlan("app_b", "foreign")

	got := mustCall(h, "app_a", plansDetail, IDInput{ID: own.ID.String()})
	for _, key := range []string{"id", "name", "slug", "currency", "status", "trial_days", "features", "pricing", "app_id", "created_at", "updated_at"} {
		if !wireKeys(t, got)[key] {
			t.Errorf("plan on the wire lacks %q", key)
		}
	}

	if _, err := call(h, "app_a", plansDetail, IDInput{ID: foreign.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("another app's plan: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_a", plansDetail, IDInput{ID: id.NewPlanID().String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("unknown plan: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_a", plansDetail, IDInput{ID: "garbage"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("malformed id: got %v, want BAD_REQUEST", err)
	}
}

func TestPlansCreate(t *testing.T) {
	h := newHarness(t)
	in := PlanCreateInput{
		Name: "Starter", Slug: "starter", Currency: "USD",
		Features: []plan.Feature{{Key: "api_calls", Name: "API calls", Type: plan.FeatureMetered, Limit: 100, Period: plan.PeriodMonthly}},
		Pricing:  &plan.Pricing{BaseAmount: types.USD(900)},
	}
	got := mustCall(h, "app_a", plansCreate, in)
	if got.AppID != "app_a" || got.Status != plan.StatusDraft || got.Currency != "usd" {
		t.Errorf("got app %q status %q currency %q; want app_a, draft, usd", got.AppID, got.Status, got.Currency)
	}
	if _, err := call(h, "app_a", plansCreate, in); codeOf(err) != dash.CodeConflict {
		t.Errorf("duplicate slug: got %v, want CONFLICT", err)
	}

	bad := in
	bad.Slug = "bad"
	bad.Pricing = &plan.Pricing{BaseAmount: types.USD(900), Tiers: []plan.PriceTier{
		{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: -1, UnitAmount: types.USD(3)},
		{FeatureKey: "api_calls", Type: plan.TierFlat, UpTo: 50, FlatAmount: types.USD(100)},
	}}
	if _, err := call(h, "app_a", plansCreate, bad); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("mixed-type ladder: got %v, want BAD_REQUEST", err)
	}
}

func TestPlansUpdateLeavesOmittedFieldsAlone(t *testing.T) {
	h := newHarness(t)
	p := h.activePlan("app_a", "keep")
	p.Description = "original"
	if err := h.eng.UpdatePlan(ctxBackground(), p); err != nil {
		t.Fatalf("seed description: %v", err)
	}

	name := "Renamed"
	got := mustCall(h, "app_a", plansUpdate, PlanUpdateInput{ID: p.ID.String(), Name: &name})
	if got.Name != "Renamed" || got.Description != "original" || len(got.Features) != 2 {
		t.Errorf("got name %q description %q features %d; the omitted fields must be unchanged", got.Name, got.Description, len(got.Features))
	}

	eur := "eur"
	if _, err := call(h, "app_a", plansUpdate, PlanUpdateInput{ID: p.ID.String(), Currency: &eur}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("currency change: got %v, want BAD_REQUEST", err)
	}

	foreign := h.activePlan("app_b", "foreign")
	if _, err := call(h, "app_a", plansUpdate, PlanUpdateInput{ID: foreign.ID.String(), Name: &name}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("another app's plan: got %v, want NOT_FOUND", err)
	}
	if stored, _ := h.eng.GetPlan(ctxBackground(), foreign.ID); stored.Name == "Renamed" {
		t.Error("a refused cross-app update changed the other app's plan")
	}
}

func TestPlansLifecycleCommands(t *testing.T) {
	h := newHarness(t)
	p := h.activePlan("app_a", "life")

	mustCall(h, "app_a", plansArchive, IDInput{ID: p.ID.String()})
	if got, _ := h.eng.GetPlan(ctxBackground(), p.ID); got.Status != plan.StatusArchived {
		t.Errorf("after archive: %q", got.Status)
	}
	mustCall(h, "app_a", plansActivate, IDInput{ID: p.ID.String()})
	if got, _ := h.eng.GetPlan(ctxBackground(), p.ID); got.Status != plan.StatusActive {
		t.Errorf("after activate: %q", got.Status)
	}

	foreign := h.activePlan("app_b", "f")
	for name, fn := range map[string]handlerFn[IDInput, Ack]{"archive": plansArchive, "activate": plansActivate, "delete": plansDelete} {
		if _, err := call(h, "app_a", fn, IDInput{ID: foreign.ID.String()}); codeOf(err) != dash.CodeNotFound {
			t.Errorf("%s another app's plan: got %v, want NOT_FOUND", name, err)
		}
	}

	mustCall(h, "app_a", plansDelete, IDInput{ID: p.ID.String()})
	if _, err := call(h, "app_a", plansDetail, IDInput{ID: p.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("after delete: got %v, want NOT_FOUND", err)
	}
}

// baseProvider is provider.Provider under another name, so embedding it does not
// clash with the Provider method the plugin interface requires.
type baseProvider = provider.Provider

// fakeProvider is a payment provider plugin whose SyncPlan answers with a fixed
// id and error. The rest of provider.Provider is left nil: plans.syncToProvider
// reaches nothing else.
type fakeProvider struct {
	baseProvider
	syncID  string
	syncErr error
}

func (f *fakeProvider) Name() string                { return "fake" }
func (f *fakeProvider) Provider() provider.Provider { return f }
func (f *fakeProvider) SyncPlan(context.Context, *plan.Plan) (string, error) {
	return f.syncID, f.syncErr
}

// withProvider swaps the harness's engine for one with a payment provider
// registered, over the same store.
func (h *harness) withProvider(fp *fakeProvider) {
	eng := ledger.New(h.store, ledger.WithPlugin(fp))
	h.eng = eng
	h.deps = Deps{Engine: func() *ledger.Ledger { return eng }}
}

func TestPlansSyncToProvider(t *testing.T) {
	t.Run("no provider configured is unavailable", func(t *testing.T) {
		h := newHarness(t)
		p := h.activePlan("app_a", "nosync")
		if _, err := call(h, "app_a", plansSync, IDInput{ID: p.ID.String()}); codeOf(err) != dash.CodeUnavailable {
			t.Errorf("got %v, want UNAVAILABLE", err)
		}
	})

	t.Run("a refusal is an answer, not an internal error", func(t *testing.T) {
		h := newHarness(t)
		h.withProvider(&fakeProvider{syncErr: errors.New("card network says no")})
		p := h.activePlan("app_a", "refused")

		got, err := call(h, "app_a", plansSync, IDInput{ID: p.ID.String()})
		if err != nil {
			t.Fatalf("a provider refusal must not be an error, got %v", err)
		}
		if got == nil || got.Success || got.Error != "card network says no" || got.EntityID != p.ID.String() {
			t.Errorf("got %+v, want an unsuccessful result carrying the provider's message", got)
		}
	})

	t.Run("success records the provider's id", func(t *testing.T) {
		h := newHarness(t)
		h.withProvider(&fakeProvider{syncID: "prod_123"})
		p := h.activePlan("app_a", "synced")

		got := mustCall(h, "app_a", plansSync, IDInput{ID: p.ID.String()})
		if !got.Success || got.ProviderID != "prod_123" || got.ProviderName != "fake" {
			t.Errorf("got %+v, want a successful result for prod_123", got)
		}
		if stored, _ := h.eng.GetPlan(ctxBackground(), p.ID); stored.ProviderID != "prod_123" {
			t.Errorf("stored provider id %q, want prod_123", stored.ProviderID)
		}
	})

	t.Run("another app's plan is not found and nothing is pushed", func(t *testing.T) {
		h := newHarness(t)
		fp := &fakeProvider{syncID: "prod_x"}
		h.withProvider(fp)
		foreign := h.activePlan("app_b", "foreign")
		if _, err := call(h, "app_a", plansSync, IDInput{ID: foreign.ID.String()}); codeOf(err) != dash.CodeNotFound {
			t.Errorf("got %v, want NOT_FOUND", err)
		}
		if stored, _ := h.eng.GetPlan(ctxBackground(), foreign.ID); stored.ProviderID != "" {
			t.Errorf("a refused cross-app sync stamped provider id %q on the other app's plan", stored.ProviderID)
		}
	})
}
