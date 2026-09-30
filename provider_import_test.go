package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// providerAPI is provider.Provider under another name, so embedding it does
// not clash with the Provider method a payment provider plugin needs.
type providerAPI = provider.Provider

// importSource is a payment provider whose Import methods answer from builders
// keyed by provider id, and refuse any other id in their own words. Nothing
// else in provider.Provider is reached. calls counts every Import call.
type importSource struct {
	providerAPI
	plans    map[string]func() *plan.Plan
	features map[string]func() *feature.Feature
	subs     map[string]func() *subscription.Subscription
	invoices map[string]func() *invoice.Invoice
	calls    int
}

func (s *importSource) Name() string                { return "fake" }
func (s *importSource) Provider() provider.Provider { return s }

func importAnswer[T any](s *importSource, builders map[string]func() *T, noun, pid string) (*T, error) {
	s.calls++
	if build, ok := builders[pid]; ok {
		return build(), nil
	}
	return nil, fmt.Errorf("no such %s: %s", noun, pid)
}

func (s *importSource) ImportPlan(_ context.Context, pid string) (*plan.Plan, error) {
	return importAnswer(s, s.plans, "plan", pid)
}

func (s *importSource) ImportFeature(_ context.Context, pid string) (*feature.Feature, error) {
	return importAnswer(s, s.features, "feature", pid)
}

func (s *importSource) ImportSubscription(_ context.Context, pid string) (*subscription.Subscription, error) {
	return importAnswer(s, s.subs, "subscription", pid)
}

func (s *importSource) ImportInvoice(_ context.Context, pid string) (*invoice.Invoice, error) {
	return importAnswer(s, s.invoices, "invoice", pid)
}

func newImportLedger(src *importSource) (*ledger.Ledger, *memory.Store) {
	st := memory.New()
	return ledger.New(st, ledger.WithPlugin(src)), st
}

func TestImportIntoFilesThePlanUnderTheCallersApp(t *testing.T) {
	ctx := context.Background()
	src := &importSource{plans: map[string]func() *plan.Plan{
		"prod_1": func() *plan.Plan { return validPlan("growth", "") },
	}}
	l, _ := newImportLedger(src)

	p, err := l.ImportPlanFromProvider(ctx, "", "prod_1", ledger.ImportInto("app_1"))
	if err != nil {
		t.Fatalf("ImportPlanFromProvider: %v", err)
	}
	if p.AppID != "app_1" || p.ProviderID != "prod_1" || p.ProviderName != "fake" || p.ID.IsNil() {
		t.Errorf("imported %+v; want app_1, prod_1 from fake, with a local id", p)
	}
	stored, err := l.GetPlan(ctx, p.ID)
	if err != nil || stored.AppID != "app_1" {
		t.Errorf("stored plan %+v, err %v; want it in app_1", stored, err)
	}
}

func TestImportWithoutAnAppKeepsTheProvidersApp(t *testing.T) {
	src := &importSource{plans: map[string]func() *plan.Plan{
		"prod_1": func() *plan.Plan { return validPlan("growth", "app_9") },
	}}
	l, _ := newImportLedger(src)
	p, err := l.ImportPlanFromProvider(context.Background(), "fake", "prod_1")
	if err != nil {
		t.Fatalf("ImportPlanFromProvider: %v", err)
	}
	if p.AppID != "app_9" {
		t.Errorf("app = %q, want the provider's app_9", p.AppID)
	}
}

func TestImportRefusesARecordTheProviderFilesUnderAnotherApp(t *testing.T) {
	ctx := context.Background()
	src := &importSource{plans: map[string]func() *plan.Plan{
		"prod_1": func() *plan.Plan { return validPlan("theirs", "app_2") },
	}}
	l, st := newImportLedger(src)

	_, err := l.ImportPlanFromProvider(ctx, "", "prod_1", ledger.ImportInto("app_1"))
	if !errors.Is(err, ledger.ErrPlanNotFound) {
		t.Fatalf("got %v, want ErrPlanNotFound", err)
	}
	for _, app := range []string{"app_1", "app_2"} {
		if rows, _ := st.ListPlans(ctx, app, plan.ListOpts{}); len(rows) != 0 {
			t.Errorf("%s holds %d plans after a refused import, want 0", app, len(rows))
		}
	}
}

func TestImportRefusesBeforeAskingAndReportsTheProvidersWords(t *testing.T) {
	ctx := context.Background()
	src := &importSource{plans: map[string]func() *plan.Plan{
		"prod_empty": func() *plan.Plan { return nil },
	}}
	l, _ := newImportLedger(src)

	if _, err := l.ImportPlanFromProvider(ctx, "", "  ", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("blank provider id: got %v, want ErrInvalidInput", err)
	}
	if src.calls != 0 {
		t.Errorf("the provider was asked %d times for a blank id, want 0", src.calls)
	}

	_, err := l.ImportPlanFromProvider(ctx, "", "prod_missing", ledger.ImportInto("app_1"))
	if !errors.Is(err, ledger.ErrProviderSync) || !strings.Contains(err.Error(), "no such plan: prod_missing") {
		t.Errorf("unknown id: got %v, want ErrProviderSync carrying the provider's words", err)
	}
	if _, err := l.ImportPlanFromProvider(ctx, "", "prod_empty", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrProviderSync) {
		t.Errorf("an empty answer: got %v, want ErrProviderSync", err)
	}
	if _, err := l.ImportPlanFromProvider(ctx, "paypal", "prod_empty", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrProviderNotFound) {
		t.Errorf("unknown provider name: got %v, want ErrProviderNotFound", err)
	}
	bare := ledger.New(memory.New())
	if _, err := bare.ImportPlanFromProvider(ctx, "", "prod_1", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrProviderNotConfigured) {
		t.Errorf("no provider: got %v, want ErrProviderNotConfigured", err)
	}
}

func TestImportedPlansAreValidatedAndASecondImportConflicts(t *testing.T) {
	ctx := context.Background()
	src := &importSource{plans: map[string]func() *plan.Plan{
		"prod_1": func() *plan.Plan { return validPlan("growth", "") },
		"prod_nameless": func() *plan.Plan {
			p := validPlan("nameless", "")
			p.Name = ""
			return p
		},
	}}
	l, _ := newImportLedger(src)

	if _, err := l.ImportPlanFromProvider(ctx, "", "prod_1", ledger.ImportInto("app_1")); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := l.ImportPlanFromProvider(ctx, "", "prod_1", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("second import: got %v, want ErrAlreadyExists", err)
	}
	if _, err := l.ImportPlanFromProvider(ctx, "", "prod_1", ledger.ImportInto("app_2")); err != nil {
		t.Errorf("another app may import the same plan: %v", err)
	}
	if _, err := l.ImportPlanFromProvider(ctx, "", "prod_nameless", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("a plan with no name: got %v, want ErrInvalidInput", err)
	}
}

func TestImportedFeaturesGoToTheCatalogTheCallerNames(t *testing.T) {
	ctx := context.Background()
	src := &importSource{features: map[string]func() *feature.Feature{
		"mtr_1": func() *feature.Feature {
			return &feature.Feature{Key: "exports", Name: "Exports", Type: feature.FeatureMetered, DefaultLimit: 100, Period: feature.PeriodMonthly}
		},
		"mtr_blank": func() *feature.Feature {
			return &feature.Feature{Key: "  ", Name: "Blank", Type: feature.FeatureBoolean}
		},
	}}
	l, _ := newImportLedger(src)

	own, err := l.ImportFeatureFromProvider(ctx, "", "mtr_1", ledger.ImportInto("app_1"))
	if err != nil || own.AppID != "app_1" || own.Status != feature.StatusActive {
		t.Fatalf("app import: %+v, %v; want an active feature in app_1", own, err)
	}
	shared, err := l.ImportFeatureFromProvider(ctx, "", "mtr_1", ledger.ImportInto(""))
	if err != nil || shared.AppID != "" {
		t.Fatalf("shared import: %+v, %v; want a global feature", shared, err)
	}
	if _, err := l.ImportFeatureFromProvider(ctx, "", "mtr_1", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("a key the app uses: got %v, want ErrAlreadyExists", err)
	}
	if _, err := l.ImportFeatureFromProvider(ctx, "", "mtr_blank", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("a feature with no key: got %v, want ErrInvalidInput", err)
	}
}

// activePlanIn creates and activates a plan through the engine.
func activePlanIn(t *testing.T, l *ledger.Ledger, slug, app string) *plan.Plan {
	t.Helper()
	p := validPlan(slug, app)
	if err := l.CreatePlan(context.Background(), p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := l.ActivatePlan(context.Background(), p.ID); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	return p
}

func TestImportedSubscriptionsNeedTheirPlanInTheApp(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, _ := newImportLedger(src)
	ours := activePlanIn(t, l, "pro", "app_1")
	theirs := activePlanIn(t, l, "pro", "app_2")
	onPlan := func(p *plan.Plan) func() *subscription.Subscription {
		return func() *subscription.Subscription {
			return &subscription.Subscription{TenantID: "acme", PlanID: p.ID, Status: subscription.StatusActive}
		}
	}
	src.subs = map[string]func() *subscription.Subscription{"sub_1": onPlan(ours), "sub_2": onPlan(theirs)}

	s, err := l.ImportSubscriptionFromProvider(ctx, "", "sub_1", ledger.ImportInto("app_1"))
	if err != nil || s.AppID != "app_1" || s.PlanID != ours.ID || s.ProviderID != "sub_1" {
		t.Fatalf("import: %+v, %v", s, err)
	}
	if _, err = l.ImportSubscriptionFromProvider(ctx, "", "sub_1", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("second import: got %v, want ErrAlreadyExists", err)
	}
	_, err = l.ImportSubscriptionFromProvider(ctx, "", "sub_2", ledger.ImportInto("app_1"))
	if !errors.Is(err, ledger.ErrInvalidInput) || strings.Contains(err.Error(), "app_2") {
		t.Errorf("another app's plan: got %v, want ErrInvalidInput that names no other app", err)
	}
}

func TestImportedInvoicesNeedTheirSubscriptionAndGetLineItemIDs(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, _ := newImportLedger(src)
	p := activePlanIn(t, l, "pro", "app_1")
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bill := func(tenant string) func() *invoice.Invoice {
		return func() *invoice.Invoice {
			return &invoice.Invoice{
				TenantID: tenant, SubscriptionID: sub.ID, Status: invoice.StatusPaid, Currency: "usd",
				Subtotal: types.USD(4900), Total: types.USD(4900), TaxAmount: types.Zero("usd"), DiscountAmount: types.Zero("usd"),
				PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0),
				LineItems: []invoice.LineItem{{Description: "Pro plan", Quantity: 1, UnitAmount: types.USD(4900), Amount: types.USD(4900), Type: invoice.LineItemBase}},
			}
		}
	}
	src.invoices = map[string]func() *invoice.Invoice{"in_1": bill("acme"), "in_2": bill("acme"), "in_globex": bill("globex")}

	inv, err := l.ImportInvoiceFromProvider(ctx, "", "in_1", ledger.ImportInto("app_1"))
	if err != nil || inv.AppID != "app_1" || inv.ProviderID != "in_1" {
		t.Fatalf("import: %+v, %v", inv, err)
	}
	if len(inv.LineItems) != 1 || inv.LineItems[0].ID.IsNil() || inv.LineItems[0].InvoiceID != inv.ID {
		t.Errorf("line items %+v; want an id and this invoice's id on each", inv.LineItems)
	}
	if _, err := l.ImportInvoiceFromProvider(ctx, "", "in_1", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("second import: got %v, want ErrAlreadyExists", err)
	}
	if _, err := l.ImportInvoiceFromProvider(ctx, "", "in_2", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("a second live invoice for the period: got %v, want ErrAlreadyExists", err)
	}
	if _, err := l.ImportInvoiceFromProvider(ctx, "", "in_globex", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("another tenant's subscription: got %v, want ErrInvalidInput", err)
	}
}
