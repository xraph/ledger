package contract

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// importingProvider is a payment provider whose Import methods answer from
// builders keyed by provider id, and refuse any other id in their own words.
// Nothing else in provider.Provider is reached.
type importingProvider struct {
	baseProvider
	plans    map[string]func() *plan.Plan
	features map[string]func() *feature.Feature
	subs     map[string]func() *subscription.Subscription
	invoices map[string]func() *invoice.Invoice
}

func (p *importingProvider) Name() string                { return "fake" }
func (p *importingProvider) Provider() provider.Provider { return p }

func providerAnswer[T any](builders map[string]func() *T, noun, pid string) (*T, error) {
	if build, ok := builders[pid]; ok {
		return build(), nil
	}
	return nil, fmt.Errorf("no such %s: %s", noun, pid)
}

func (p *importingProvider) ImportPlan(_ context.Context, pid string) (*plan.Plan, error) {
	return providerAnswer(p.plans, "plan", pid)
}

func (p *importingProvider) ImportFeature(_ context.Context, pid string) (*feature.Feature, error) {
	return providerAnswer(p.features, "feature", pid)
}

func (p *importingProvider) ImportSubscription(_ context.Context, pid string) (*subscription.Subscription, error) {
	return providerAnswer(p.subs, "subscription", pid)
}

func (p *importingProvider) ImportInvoice(_ context.Context, pid string) (*invoice.Invoice, error) {
	return providerAnswer(p.invoices, "invoice", pid)
}

// withImports swaps the harness's engine for one with the provider
// registered, over the same store.
func (h *harness) withImports(p *importingProvider) {
	eng := ledger.New(h.store, ledger.WithPlugin(p))
	h.eng = eng
	h.deps = Deps{Engine: func() *ledger.Ledger { return eng }}
}

func messageOf(err error) string {
	var ce *dash.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return ""
}

func providerPlan(slug, app string) func() *plan.Plan {
	return func() *plan.Plan {
		return &plan.Plan{Name: "Plan " + slug, Slug: slug, Currency: "usd", Status: plan.StatusActive, AppID: app}
	}
}

func TestImportManifest(t *testing.T) {
	assertIntents(t, map[string]string{
		"plans.importFromProvider": "command", "features.importFromProvider": "command",
		"subscriptions.importFromProvider": "command", "invoices.importFromProvider": "command",
	})
	assertInvalidates(t, map[string][]string{
		"plans.importFromProvider":         {"plans.list", "overview.stats"},
		"features.importFromProvider":      {"features.list"},
		"subscriptions.importFromProvider": {"subscriptions.list", "overview.stats", "entitlements.check", "paymentMethods.list"},
		"invoices.importFromProvider":      {"invoices.list", "invoices.pending", "subscriptions.detail", "overview.stats", "overview.recentInvoices"},
	})
}

func TestPlansImportFromProvider(t *testing.T) {
	src := func() *importingProvider {
		return &importingProvider{plans: map[string]func() *plan.Plan{
			"prod_growth": providerPlan("growth", ""),
			"prod_theirs": providerPlan("theirs", "app_b"),
		}}
	}

	t.Run("no provider configured is unavailable", func(t *testing.T) {
		h := newHarness(t)
		if _, err := call(h, "app_a", plansImport, ImportInput{ProviderID: "prod_growth"}); codeOf(err) != dash.CodeUnavailable {
			t.Errorf("got %v, want UNAVAILABLE", err)
		}
	})

	t.Run("a blank provider id is a bad request", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		_, err := call(h, "app_a", plansImport, ImportInput{ProviderID: "  "})
		if codeOf(err) != dash.CodeBadRequest || messageOf(err) != "provider_id is required" {
			t.Errorf("got %v, want BAD_REQUEST \"provider_id is required\"", err)
		}
	})

	t.Run("an unknown provider name is unavailable", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		if _, err := call(h, "app_a", plansImport, ImportInput{ProviderName: "paypal", ProviderID: "prod_growth"}); codeOf(err) != dash.CodeUnavailable {
			t.Errorf("got %v, want UNAVAILABLE", err)
		}
	})

	t.Run("the provider's refusal is unavailable, in its own words", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		_, err := call(h, "app_a", plansImport, ImportInput{ProviderID: "prod_missing"})
		if codeOf(err) != dash.CodeUnavailable || !strings.Contains(messageOf(err), "no such plan: prod_missing") {
			t.Errorf("got %v, want UNAVAILABLE carrying the provider's message", err)
		}
	})

	t.Run("the plan lands in the caller's app, in the detail shape", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		got := mustCall(h, "app_a", plansImport, ImportInput{ProviderName: "fake", ProviderID: " prod_growth "})
		if got.AppID != "app_a" || got.ProviderID != "prod_growth" || got.ProviderName != "fake" || got.Slug != "growth" {
			t.Errorf("imported %+v", got)
		}
		assertEmptyFeatures(t, "plans.importFromProvider", wireJSON(t, got))
		detail := mustCall(h, "app_a", plansDetail, IDInput{ID: got.ID.String()})
		if !reflect.DeepEqual(wireKeys(t, got), wireKeys(t, detail)) {
			t.Errorf("import keys %v, detail keys %v; want the same shape", wireKeys(t, got), wireKeys(t, detail))
		}
		if _, err := call(h, "app_b", plansDetail, IDInput{ID: got.ID.String()}); codeOf(err) != dash.CodeNotFound {
			t.Errorf("app_b reading the import: got %v, want NOT_FOUND", err)
		}
	})

	t.Run("importing it again is a conflict", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		mustCall(h, "app_a", plansImport, ImportInput{ProviderID: "prod_growth"})
		if _, err := call(h, "app_a", plansImport, ImportInput{ProviderID: "prod_growth"}); codeOf(err) != dash.CodeConflict {
			t.Errorf("got %v, want CONFLICT", err)
		}
	})

	t.Run("a plan the provider files under another app is not found, and nothing is stored", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		if _, err := call(h, "app_a", plansImport, ImportInput{ProviderID: "prod_theirs"}); codeOf(err) != dash.CodeNotFound {
			t.Errorf("got %v, want NOT_FOUND", err)
		}
		for _, app := range []string{"app_a", "app_b"} {
			if l := mustCall(h, app, plansList, PlansListInput{}); len(l.Items) != 0 {
				t.Errorf("%s lists %d plans after a refused import, want 0", app, len(l.Items))
			}
		}
	})

	t.Run("the empty scope is refused", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		if _, err := call(h, "", plansImport, ImportInput{ProviderID: "prod_growth"}); codeOf(err) != dash.CodePermissionDenied {
			t.Errorf("got %v, want PERMISSION_DENIED", err)
		}
	})
}

func TestFeaturesImportFromProvider(t *testing.T) {
	src := func() *importingProvider {
		return &importingProvider{features: map[string]func() *feature.Feature{
			"mtr_exports": func() *feature.Feature {
				return &feature.Feature{Key: "exports", Name: "Exports", Type: feature.FeatureMetered, DefaultLimit: 100, Period: feature.PeriodMonthly}
			},
		}}
	}

	t.Run("from an app it joins that app's catalog", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		got := mustCallPlatform(h, "app_a", featuresImport, ImportInput{ProviderID: "mtr_exports"})
		if got.AppID != "app_a" || got.Status != feature.StatusActive || got.ProviderID != "mtr_exports" {
			t.Errorf("imported %+v", got)
		}
		mustCallPlatform(h, "app_a", featuresDetail, IDInput{ID: got.ID.String()})
		if _, err := callPlatform(h, "app_a", featuresImport, ImportInput{ProviderID: "mtr_exports"}); codeOf(err) != dash.CodeConflict {
			t.Errorf("a key the app uses: got %v, want CONFLICT", err)
		}
	})

	t.Run("from the empty scope it joins the shared catalog", func(t *testing.T) {
		h := newHarness(t)
		h.withImports(src())
		got := mustCallPlatform(h, "", featuresImport, ImportInput{ProviderID: "mtr_exports"})
		if got.AppID != "" {
			t.Errorf("app = %q, want the shared catalog", got.AppID)
		}
	})

	t.Run("no provider configured is unavailable", func(t *testing.T) {
		h := newHarness(t)
		if _, err := callPlatform(h, "app_a", featuresImport, ImportInput{ProviderID: "mtr_exports"}); codeOf(err) != dash.CodeUnavailable {
			t.Errorf("got %v, want UNAVAILABLE", err)
		}
	})
}

func TestSubscriptionsImportFromProvider(t *testing.T) {
	h := newHarness(t)
	ours := h.activePlan("app_a", "pro")
	theirs := h.activePlan("app_b", "other")
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	onPlan := func(planID id.PlanID) func() *subscription.Subscription {
		return func() *subscription.Subscription {
			return &subscription.Subscription{
				TenantID: "acme", PlanID: planID, Status: subscription.StatusActive,
				CurrentPeriodStart: start, CurrentPeriodEnd: start.AddDate(0, 1, 0),
			}
		}
	}
	h.withImports(&importingProvider{subs: map[string]func() *subscription.Subscription{
		"sub_1acme":    onPlan(ours.ID),
		"sub_1foreign": onPlan(theirs.ID),
	}})

	got := mustCall(h, "app_a", subscriptionsImport, ImportInput{ProviderID: "sub_1acme"})
	if got.Subscription.AppID != "app_a" || got.Subscription.ProviderID != "sub_1acme" || got.Plan.ID != ours.ID {
		t.Errorf("imported %+v", got)
	}
	keys := wireKeys(t, got)
	if !keys["subscription"] || !keys["plan"] || !keys["applied_coupons"] {
		t.Errorf("keys %v; want the subscriptions.detail shape", keys)
	}
	if !strings.Contains(wireJSON(t, got), `"applied_coupons":[]`) {
		t.Errorf("applied_coupons must be [] on the wire: %s", wireJSON(t, got))
	}

	if _, err := call(h, "app_a", subscriptionsImport, ImportInput{ProviderID: "sub_1acme"}); codeOf(err) != dash.CodeConflict {
		t.Errorf("second import: got %v, want CONFLICT", err)
	}
	_, err := call(h, "app_a", subscriptionsImport, ImportInput{ProviderID: "sub_1foreign"})
	if codeOf(err) != dash.CodeBadRequest || strings.Contains(messageOf(err), "app_b") {
		t.Errorf("another app's plan: got %v, want BAD_REQUEST that names no other app", err)
	}
	if l := mustCall(h, "app_a", subscriptionsList, SubscriptionsListInput{}); len(l.Items) != 1 {
		t.Errorf("app_a lists %d subscriptions, want 1", len(l.Items))
	}
}

func TestInvoicesImportFromProvider(t *testing.T) {
	h := newHarness(t)
	acme := h.subscribe("app_a", "acme", h.activePlan("app_a", "pro"))
	foreign := h.subscribe("app_b", "acme", h.activePlan("app_b", "other"))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	paidAt := start.AddDate(0, 0, 3)
	bill := func(subID id.SubscriptionID) func() *invoice.Invoice {
		return func() *invoice.Invoice {
			return &invoice.Invoice{
				TenantID: "acme", SubscriptionID: subID, Status: invoice.StatusPaid, PaidAt: &paidAt, Currency: "usd",
				Subtotal: types.USD(4900), Total: types.USD(4900), TaxAmount: types.Zero("usd"), DiscountAmount: types.Zero("usd"),
				PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0),
				LineItems: []invoice.LineItem{{Description: "Pro plan", Quantity: 1, UnitAmount: types.USD(4900), Amount: types.USD(4900), Type: invoice.LineItemBase}},
			}
		}
	}
	h.withImports(&importingProvider{invoices: map[string]func() *invoice.Invoice{
		"in_1": bill(acme.ID), "in_2": bill(acme.ID), "in_foreign": bill(foreign.ID),
	}})

	got := mustCall(h, "app_a", invoicesImport, ImportInput{ProviderID: "in_1"})
	if got.Invoice.AppID != "app_a" || got.Invoice.ProviderID != "in_1" || got.Subscription.ID != acme.ID {
		t.Errorf("imported %+v", got)
	}
	keys := wireKeys(t, got)
	if !keys["invoice"] || !keys["subscription"] || !keys["export_formats"] {
		t.Errorf("keys %v; want the invoices.detail shape", keys)
	}
	if li := got.Invoice.LineItems; len(li) != 1 || li[0].ID.IsNil() || li[0].InvoiceID != got.Invoice.ID {
		t.Errorf("line items %+v; want an id and this invoice's id", li)
	}

	if _, err := call(h, "app_a", invoicesImport, ImportInput{ProviderID: "in_1"}); codeOf(err) != dash.CodeConflict {
		t.Errorf("second import: got %v, want CONFLICT", err)
	}
	if _, err := call(h, "app_a", invoicesImport, ImportInput{ProviderID: "in_2"}); codeOf(err) != dash.CodeConflict {
		t.Errorf("a second live invoice for the period: got %v, want CONFLICT", err)
	}
	if _, err := call(h, "app_a", invoicesImport, ImportInput{ProviderID: "in_foreign"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("another app's subscription: got %v, want BAD_REQUEST", err)
	}
}
