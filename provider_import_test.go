package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/store"
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
	mu       sync.Mutex
	calls    int
}

func (s *importSource) Name() string                { return "fake" }
func (s *importSource) Provider() provider.Provider { return s }

func importAnswer[T any](s *importSource, builders map[string]func() *T, noun, pid string) (*T, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
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
	paidAt := start.AddDate(0, 0, 2)
	bill := func(tenant string) func() *invoice.Invoice {
		return func() *invoice.Invoice {
			return &invoice.Invoice{
				TenantID: tenant, SubscriptionID: sub.ID, Status: invoice.StatusPaid, PaidAt: &paidAt, Currency: "usd",
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

func TestImportedFeaturesAreShapeChecked(t *testing.T) {
	ctx := context.Background()
	src := &importSource{features: map[string]func() *feature.Feature{
		"mtr_type": func() *feature.Feature { return &feature.Feature{Key: "a", Name: "A", Type: "bogus"} },
		"mtr_period": func() *feature.Feature {
			return &feature.Feature{Key: "b", Name: "B", Type: feature.FeatureBoolean, Period: "hourly"}
		},
		"mtr_limit": func() *feature.Feature {
			return &feature.Feature{Key: "c", Name: "C", Type: feature.FeatureMetered, DefaultLimit: -5}
		},
		"mtr_draft": func() *feature.Feature {
			return &feature.Feature{Key: "d", Name: "D", Type: feature.FeatureBoolean, Status: feature.StatusDraft}
		},
		"mtr_archived": func() *feature.Feature {
			return &feature.Feature{Key: "e", Name: "E", Type: feature.FeatureBoolean, Status: feature.StatusArchived}
		},
		"mtr_theirs": func() *feature.Feature {
			return &feature.Feature{Key: "f", Name: "F", Type: feature.FeatureBoolean, AppID: "app_2"}
		},
	}}
	l, st := newImportLedger(src)

	for _, pid := range []string{"mtr_type", "mtr_period", "mtr_limit", "mtr_draft"} {
		if _, err := l.ImportFeatureFromProvider(ctx, "", pid, ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Errorf("%s: got %v, want ErrInvalidInput", pid, err)
		}
	}
	if _, err := l.ImportFeatureFromProvider(ctx, "", "mtr_theirs", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrFeatureNotFound) {
		t.Errorf("a feature filed under another app: got %v, want ErrFeatureNotFound", err)
	}
	if rows, _ := st.ListFeatures(ctx, "app_1", feature.ListOpts{}); len(rows) != 0 {
		t.Errorf("app_1 holds %d features after refused imports, want 0", len(rows))
	}
	f, err := l.ImportFeatureFromProvider(ctx, "", "mtr_archived", ledger.ImportInto("app_1"))
	if err != nil || f.Status != feature.StatusArchived {
		t.Errorf("an archived feature: %+v, %v; want it kept archived", f, err)
	}
}

func TestImportedSubscriptionNeedsAnActivePlanAndNamesIt(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, st := newImportLedger(src)
	draft := validPlan("starter", "app_1")
	if err := l.CreatePlan(ctx, draft); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	src.subs = map[string]func() *subscription.Subscription{
		"sub_1": func() *subscription.Subscription {
			return &subscription.Subscription{TenantID: "acme", PlanID: draft.ID}
		},
	}

	_, err := l.ImportSubscriptionFromProvider(ctx, "", "sub_1", ledger.ImportInto("app_1"))
	if !errors.Is(err, ledger.ErrInvalidInput) || !strings.Contains(err.Error(), "activate plan starter before importing its subscriptions") {
		t.Fatalf("got %v, want ErrInvalidInput telling the operator to activate plan starter", err)
	}
	if rows, _ := st.ListSubscriptions(ctx, "acme", "app_1", subscription.ListOpts{}); len(rows) != 0 {
		t.Errorf("%d subscriptions stored after a refused import, want 0", len(rows))
	}
}

func TestImportedSubscriptionRefusesAMissingAndAForeignPlanInTheSameWords(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, st := newImportLedger(src)
	theirs := activePlanIn(t, l, "pro", "app_2")
	missing := id.NewPlanID()
	on := func(pid id.PlanID) func() *subscription.Subscription {
		return func() *subscription.Subscription { return &subscription.Subscription{TenantID: "acme", PlanID: pid} }
	}
	src.subs = map[string]func() *subscription.Subscription{"sub_missing": on(missing), "sub_foreign": on(theirs.ID)}

	_, errMissing := l.ImportSubscriptionFromProvider(ctx, "", "sub_missing", ledger.ImportInto("app_1"))
	_, errForeign := l.ImportSubscriptionFromProvider(ctx, "", "sub_foreign", ledger.ImportInto("app_1"))
	if !errors.Is(errMissing, ledger.ErrInvalidInput) || !errors.Is(errForeign, ledger.ErrInvalidInput) {
		t.Fatalf("got %v and %v, want ErrInvalidInput for both", errMissing, errForeign)
	}
	blind := func(err error, pid id.PlanID) string { return strings.ReplaceAll(err.Error(), pid.String(), "<plan>") }
	if blind(errMissing, missing) != blind(errForeign, theirs.ID) {
		t.Errorf("a missing plan says %q and a foreign plan says %q; want the same words", errMissing, errForeign)
	}
	if rows, _ := st.ListSubscriptions(ctx, "acme", "app_1", subscription.ListOpts{}); len(rows) != 0 {
		t.Errorf("%d subscriptions stored after refused imports, want 0", len(rows))
	}
}

func TestImportedSubscriptionRulesHoldForEveryEntityAcrossApps(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, _ := newImportLedger(src)
	p := activePlanIn(t, l, "pro", "app_1")
	src.subs = map[string]func() *subscription.Subscription{
		"sub_theirs": func() *subscription.Subscription {
			return &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_2"}
		},
		"sub_padded": func() *subscription.Subscription {
			return &subscription.Subscription{TenantID: "  acme ", PlanID: p.ID}
		},
		"sub_blank": func() *subscription.Subscription {
			return &subscription.Subscription{TenantID: "   ", PlanID: p.ID}
		},
	}
	if _, err := l.ImportSubscriptionFromProvider(ctx, "", "sub_theirs", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrSubscriptionNotFound) {
		t.Errorf("a subscription filed under another app: got %v, want ErrSubscriptionNotFound", err)
	}
	if _, err := l.ImportSubscriptionFromProvider(ctx, "", "sub_blank", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("a blank tenant: got %v, want ErrInvalidInput", err)
	}
	s, err := l.ImportSubscriptionFromProvider(ctx, "", "sub_padded", ledger.ImportInto("app_1"))
	if err != nil || s.TenantID != "acme" {
		t.Errorf("a padded tenant: %+v, %v; want it trimmed to acme", s, err)
	}
}

func TestImportedSubscriptionDuplicateCheckStaysInItsApp(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, _ := newImportLedger(src)
	inApp := activePlanIn(t, l, "pro", "app_1")
	noApp := activePlanIn(t, l, "team", "")
	src.subs = map[string]func() *subscription.Subscription{
		"sub_1": func() *subscription.Subscription {
			return &subscription.Subscription{TenantID: "acme", PlanID: inApp.ID}
		},
	}
	if _, err := l.ImportSubscriptionFromProvider(ctx, "", "sub_1", ledger.ImportInto("app_1")); err != nil {
		t.Fatalf("import into app_1: %v", err)
	}
	src.subs["sub_1"] = func() *subscription.Subscription {
		return &subscription.Subscription{TenantID: "acme", PlanID: noApp.ID}
	}
	if _, err := l.ImportSubscriptionFromProvider(ctx, "", "sub_1", ledger.ImportInto("")); err != nil {
		t.Errorf("the no-app scope must not see app_1's copy as a duplicate: %v", err)
	}
}

// barrierStore holds every subscription listing at a barrier once it is armed,
// until want callers are inside or a short timeout passes. The subscription
// import's duplicate scan is that listing, so armed with want imports in
// flight, every goroutine is past its duplicate check before any of them
// writes. Without the engine's import lock that always stores several rows.
// With the lock only one goroutine is inside at a time, the barrier times out
// once, and then lets everything through, so the test stays fast.
type barrierStore struct {
	store.Store
	mu      sync.Mutex
	want    int
	arrived int
	release chan struct{}
	once    sync.Once
}

func (b *barrierStore) arm(want int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.want, b.arrived, b.release = want, 0, make(chan struct{})
	b.once = sync.Once{}
}

func (b *barrierStore) wait() {
	b.mu.Lock()
	if b.release == nil {
		b.mu.Unlock()
		return
	}
	release := b.release
	b.arrived++
	if b.arrived >= b.want {
		b.once.Do(func() { close(release) })
	}
	b.mu.Unlock()

	select {
	case <-release:
	case <-time.After(200 * time.Millisecond):
		b.once.Do(func() { close(release) })
	}
}

func (b *barrierStore) ListSubscriptions(ctx context.Context, tenantID, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error) {
	rows, err := b.Store.ListSubscriptions(ctx, tenantID, appID, opts)
	b.wait()
	return rows, err
}

func TestConcurrentImportsOfOneSubscriptionStoreOneRow(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	bs := &barrierStore{Store: memory.New()}
	l := ledger.New(bs, ledger.WithPlugin(src))
	p := activePlanIn(t, l, "pro", "app_1")
	src.subs = map[string]func() *subscription.Subscription{
		"sub_1": func() *subscription.Subscription { return &subscription.Subscription{TenantID: "acme", PlanID: p.ID} },
	}

	const n = 8
	bs.arm(n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := l.ImportSubscriptionFromProvider(ctx, "", "sub_1", ledger.ImportInto("app_1"))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	var stored, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			stored++
		case errors.Is(err, ledger.ErrAlreadyExists):
			conflicts++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if stored != 1 || conflicts != n-1 {
		t.Errorf("%d stored and %d conflicts from %d imports; want 1 and %d", stored, conflicts, n, n-1)
	}
	if rows, _ := bs.ListSubscriptions(ctx, "acme", "app_1", subscription.ListOpts{}); len(rows) != 1 {
		t.Errorf("%d subscription rows, want exactly 1", len(rows))
	}
}

func TestImportedInvoiceDuplicateCheckStaysInItsApp(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, _ := newImportLedger(src)
	inApp := activePlanIn(t, l, "pro", "app_1")
	noApp := activePlanIn(t, l, "team", "")
	subIn := &subscription.Subscription{TenantID: "acme", PlanID: inApp.ID, AppID: "app_1"}
	subOut := &subscription.Subscription{TenantID: "acme", PlanID: noApp.ID}
	for _, s := range []*subscription.Subscription{subIn, subOut} {
		if err := l.CreateSubscription(ctx, s); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}
	}
	src.invoices = map[string]func() *invoice.Invoice{
		"in_1": func() *invoice.Invoice { return importedBill("acme", subIn.ID) },
	}
	if _, err := l.ImportInvoiceFromProvider(ctx, "", "in_1", ledger.ImportInto("app_1")); err != nil {
		t.Fatalf("import into app_1: %v", err)
	}

	// The same provider id, for the same tenant and period, in the no-app
	// scope: the store lists every app's invoices for an empty app, so only
	// the engine's own filter keeps app_1's copy from blocking this one.
	src.invoices["in_1"] = func() *invoice.Invoice { return importedBill("acme", subOut.ID) }
	inv, err := l.ImportInvoiceFromProvider(ctx, "", "in_1", ledger.ImportInto(""))
	if err != nil {
		t.Fatalf("the no-app scope must not see app_1's invoice as a duplicate: %v", err)
	}
	if inv.AppID != "" || inv.SubscriptionID != subOut.ID {
		t.Errorf("imported %+v; want it in the no-app scope on its own subscription", inv)
	}
}

// importedBill is a valid paid invoice for sub: $49.00 of base fee, no
// discount, no tax.
func importedBill(tenant string, sub id.SubscriptionID) *invoice.Invoice {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	paid := start.AddDate(0, 0, 2)
	return &invoice.Invoice{
		TenantID: tenant, SubscriptionID: sub, Status: invoice.StatusPaid, PaidAt: &paid, Currency: "usd",
		Subtotal: types.USD(4900), Total: types.USD(4900), TaxAmount: types.Zero("usd"), DiscountAmount: types.Zero("usd"),
		PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0),
		LineItems: []invoice.LineItem{{Description: "Pro plan", Quantity: 1, UnitAmount: types.USD(4900), Amount: types.USD(4900), Type: invoice.LineItemBase}},
	}
}

func TestImportedInvoicesAreCheckedLikeGeneratedOnes(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, st := newImportLedger(src)
	p := activePlanIn(t, l, "pro", "app_1")
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	due := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	bad := map[string]func(*invoice.Invoice){
		"a total that does not add up":      func(i *invoice.Invoice) { i.Total = types.USD(4800) },
		"a currency that is not the plan's": func(i *invoice.Invoice) { i.Currency = "eur" },
		"a currency in capitals":            func(i *invoice.Invoice) { i.Currency = "USD" },
		"a money field in another currency": func(i *invoice.Invoice) { i.TaxAmount = types.EUR(0) },
		"a line item in another currency": func(i *invoice.Invoice) {
			i.LineItems[0].Amount = types.EUR(4900)
		},
		"a negative subtotal": func(i *invoice.Invoice) { i.Subtotal = types.USD(-4900) },
		"line items that miss the subtotal": func(i *invoice.Invoice) {
			i.LineItems[0].Amount = types.USD(100)
		},
		"a tax amount with tax lines that disagree": func(i *invoice.Invoice) {
			i.TaxAmount, i.Total = types.USD(500), types.USD(5400)
			i.LineItems = append(i.LineItems, invoice.LineItem{Description: "Tax", Quantity: 1, UnitAmount: types.USD(400), Amount: types.USD(400), Type: invoice.LineItemTax})
		},
		"a period that ends before it starts": func(i *invoice.Invoice) { i.PeriodEnd = i.PeriodStart.Add(-time.Hour) },
		"no period start":                     func(i *invoice.Invoice) { i.PeriodStart = time.Time{} },
		"paid with no paid-at time":           func(i *invoice.Invoice) { i.PaidAt = nil },
		"pending with no due date":            func(i *invoice.Invoice) { i.Status, i.PaidAt = invoice.StatusPending, nil },
		"past due with no due date":           func(i *invoice.Invoice) { i.Status, i.PaidAt = invoice.StatusPastDue, nil },
	}
	src.invoices = map[string]func() *invoice.Invoice{}
	for name, mutate := range bad {
		src.invoices[name] = func() *invoice.Invoice {
			inv := importedBill("acme", sub.ID)
			mutate(inv)
			return inv
		}
		if _, err := l.ImportInvoiceFromProvider(ctx, "", name, ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Errorf("%s: got %v, want ErrInvalidInput", name, err)
		}
	}
	if rows, _ := st.ListInvoices(ctx, "acme", "app_1", invoice.ListOpts{}); len(rows) != 0 {
		t.Errorf("%d invoices stored after refused imports, want 0", len(rows))
	}

	// What GenerateInvoice itself writes must import: a discount larger than
	// the bill clamps the net at zero before tax, and a pending invoice with
	// a due date is fine.
	src.invoices["free"] = func() *invoice.Invoice {
		inv := importedBill("acme", sub.ID)
		inv.Status, inv.PaidAt, inv.DueDate = invoice.StatusPending, nil, &due
		inv.DiscountAmount, inv.Total = types.USD(6000), types.Zero("usd")
		inv.LineItems = append(inv.LineItems, invoice.LineItem{Description: "Discount", Quantity: 1, UnitAmount: types.USD(-6000), Amount: types.USD(-6000), Type: invoice.LineItemDiscount})
		return inv
	}
	if _, err := l.ImportInvoiceFromProvider(ctx, "", "free", ledger.ImportInto("app_1")); err != nil {
		t.Errorf("an invoice the engine could have generated: %v", err)
	}
}

func TestImportedInvoiceRulesAcrossAppsAndTenants(t *testing.T) {
	ctx := context.Background()
	src := &importSource{}
	l, st := newImportLedger(src)
	ours := activePlanIn(t, l, "pro", "app_1")
	theirs := activePlanIn(t, l, "pro", "app_2")
	mine := &subscription.Subscription{TenantID: "acme", PlanID: ours.ID, AppID: "app_1"}
	foreign := &subscription.Subscription{TenantID: "acme", PlanID: theirs.ID, AppID: "app_2"}
	for _, s := range []*subscription.Subscription{mine, foreign} {
		if err := l.CreateSubscription(ctx, s); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}
	}
	src.invoices = map[string]func() *invoice.Invoice{
		"in_foreign_sub": func() *invoice.Invoice { return importedBill("acme", foreign.ID) },
		"in_theirs": func() *invoice.Invoice {
			inv := importedBill("acme", mine.ID)
			inv.AppID = "app_2"
			return inv
		},
		"in_padded": func() *invoice.Invoice { return importedBill(" acme  ", mine.ID) },
	}

	if _, err := l.ImportInvoiceFromProvider(ctx, "", "in_foreign_sub", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("a subscription in another app: got %v, want ErrInvalidInput", err)
	}
	if _, err := l.ImportInvoiceFromProvider(ctx, "", "in_theirs", ledger.ImportInto("app_1")); !errors.Is(err, ledger.ErrInvoiceNotFound) {
		t.Errorf("an invoice filed under another app: got %v, want ErrInvoiceNotFound", err)
	}
	for _, app := range []string{"app_1", "app_2"} {
		if rows, _ := st.ListInvoices(ctx, "acme", app, invoice.ListOpts{}); len(rows) != 0 {
			t.Errorf("%s holds %d invoices after refused imports, want 0", app, len(rows))
		}
	}
	inv, err := l.ImportInvoiceFromProvider(ctx, "", "in_padded", ledger.ImportInto("app_1"))
	if err != nil || inv.TenantID != "acme" {
		t.Errorf("a padded tenant: %+v, %v; want it trimmed to acme", inv, err)
	}
}
