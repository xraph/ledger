package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

type quotaSpy struct{ exceeded, checked int }

func (q *quotaSpy) Name() string { return "quota-spy" }
func (q *quotaSpy) OnQuotaExceeded(_ context.Context, _, _ string, _, _ int64) error {
	q.exceeded++
	return nil
}

func (q *quotaSpy) OnEntitlementChecked(_ context.Context, _ interface{}) error {
	q.checked++
	return nil
}

var (
	_ plugin.OnQuotaExceeded      = (*quotaSpy)(nil)
	_ plugin.OnEntitlementChecked = (*quotaSpy)(nil)
)

func TestInspectEntitlementHasNoSideEffects(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	spy := &quotaSpy{}
	l := ledger.New(s, ledger.WithPlugin(spy))
	p := activePlan(t, l, "ent", "app_1", 0)

	sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{{ID: id.NewUsageEventID(), TenantID: "t1", AppID: "app_1", FeatureKey: "api_calls", Quantity: 1500, Timestamp: time.Now().UTC()}}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := l.InspectEntitlement(ctx, "t1", "app_1", "api_calls")
	if err != nil {
		t.Fatalf("InspectEntitlement: %v", err)
	}
	if got.Allowed || got.Used != 1500 || got.Limit != 1000 || got.Reason != "quota exceeded" {
		t.Errorf("got %+v, want not allowed, used 1500 of 1000, quota exceeded", got)
	}
	if spy.exceeded != 0 {
		t.Errorf("OnQuotaExceeded fired %d times; inspection must not fire plugin events", spy.exceeded)
	}
	if _, err := s.GetCached(ctx, "t1", "app_1", "api_calls"); err == nil {
		t.Error("inspection must not write the entitlement cache")
	}
}

func TestInspectEntitlementWithoutASubscription(t *testing.T) {
	l := ledger.New(memory.New())
	got, err := l.InspectEntitlement(context.Background(), "nobody", "app_1", "api_calls")
	if err != nil {
		t.Fatalf("InspectEntitlement: %v", err)
	}
	if got.Allowed || got.Reason != "no active subscription" {
		t.Errorf("got %+v", got)
	}
}

func TestInspectEntitlementRefusesAnEmptyTenant(t *testing.T) {
	l := ledger.New(memory.New())
	if _, err := l.InspectEntitlement(context.Background(), "", "app_1", "api_calls"); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("empty tenant: got %v, want ErrInvalidInput", err)
	}
}

type stubFormatter struct{ format string }

func (f *stubFormatter) Name() string   { return "stub-" + f.format }
func (f *stubFormatter) Format() string { return f.format }
func (f *stubFormatter) Render(_ context.Context, _ interface{}, w interface{}) error {
	out, ok := w.(io.Writer)
	if !ok {
		return errors.New("writer is not an io.Writer")
	}
	_, err := fmt.Fprintf(out, "invoice rendered as %s", f.format)
	return err
}

var _ plugin.InvoiceFormatter = (*stubFormatter)(nil)

func TestInvoiceFormatsAndExport(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New(), ledger.WithPlugin(&stubFormatter{format: "pdf"}), ledger.WithPlugin(&stubFormatter{format: "csv"}))
	if got := l.InvoiceFormats(); len(got) != 2 || got[0] != "csv" || got[1] != "pdf" {
		t.Errorf("formats = %v, want [csv pdf]", got)
	}

	p := activePlan(t, l, "exp", "app_1", 0)
	sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	body, err := l.ExportInvoice(ctx, inv.ID, "pdf")
	if err != nil || string(body) != "invoice rendered as pdf" {
		t.Errorf("export pdf: %q, %v", body, err)
	}
	if _, err := l.ExportInvoice(ctx, inv.ID, "docx"); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("unknown format: got %v, want ErrInvalidInput", err)
	}
}

func TestNoFormatsOrProvidersIsEmptyNotNil(t *testing.T) {
	l := ledger.New(memory.New())
	if got := l.InvoiceFormats(); got == nil || len(got) != 0 {
		t.Errorf("formats = %#v, want an empty non-nil slice", got)
	}
	if got := l.ProviderNames(); got == nil || len(got) != 0 {
		t.Errorf("providers = %#v, want an empty non-nil slice", got)
	}
}

// entitledFixture is an over-quota tenant: 1500 of 1000 api_calls used, on a
// plan that also carries a boolean "sso" feature.
func entitledFixture(t *testing.T) (context.Context, *ledger.Ledger, *memory.Store, *quotaSpy) {
	t.Helper()
	ctx := context.Background()
	s := memory.New()
	spy := &quotaSpy{}
	l := ledger.New(s, ledger.WithPlugin(spy))

	p := &plan.Plan{
		Name: "ent", Slug: "ent", Currency: "usd", AppID: "app_1",
		Features: []plan.Feature{
			{Key: "api_calls", Name: "API calls", Type: plan.FeatureMetered, Limit: 1000, Period: plan.PeriodMonthly},
			{Key: "sso", Name: "SSO", Type: plan.FeatureBoolean, Limit: 1},
		},
		Pricing: &plan.Pricing{BaseAmount: types.USD(4900), BillingPeriod: plan.PeriodMonthly},
	}
	if err := l.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := l.ActivatePlan(ctx, p.ID); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{{ID: id.NewUsageEventID(), TenantID: "t1", AppID: "app_1", FeatureKey: "api_calls", Quantity: 1500, Timestamp: time.Now().UTC()}}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	//nolint:staticcheck,revive // Entitled reads these string keys today
	ctx = context.WithValue(ctx, "tenant_id", "t1")
	//nolint:staticcheck,revive // Entitled reads these string keys today
	ctx = context.WithValue(ctx, "app_id", "app_1")
	return ctx, l, s, spy
}

func TestEntitledMeteredOverQuotaCachesAndEmits(t *testing.T) {
	ctx, l, s, spy := entitledFixture(t)

	got, err := l.Entitled(ctx, "api_calls")
	if err != nil {
		t.Fatalf("Entitled: %v", err)
	}
	if got.Allowed || got.Used != 1500 || got.Limit != 1000 || got.Reason != "quota exceeded" {
		t.Errorf("got %+v, want not allowed, used 1500 of 1000, quota exceeded", got)
	}
	if spy.exceeded != 1 {
		t.Errorf("OnQuotaExceeded fired %d times, want 1", spy.exceeded)
	}
	if spy.checked != 1 {
		t.Errorf("OnEntitlementChecked fired %d times, want 1", spy.checked)
	}
	cached, err := s.GetCached(ctx, "t1", "app_1", "api_calls")
	if err != nil {
		t.Fatalf("Entitled must write the entitlement cache: %v", err)
	}
	if cached.Used != 1500 || cached.Reason != "quota exceeded" {
		t.Errorf("cached %+v, want the computed result", cached)
	}

	// A second call is answered from the cache: same result, no new events.
	again, err := l.Entitled(ctx, "api_calls")
	if err != nil {
		t.Fatalf("Entitled (second): %v", err)
	}
	if again.Allowed || again.Used != 1500 || again.Reason != "quota exceeded" {
		t.Errorf("second call got %+v, want the cached result", again)
	}
	if spy.exceeded != 1 || spy.checked != 1 {
		t.Errorf("second call fired events: exceeded=%d checked=%d, want 1 and 1", spy.exceeded, spy.checked)
	}
}

func TestEntitledBooleanCachesWithoutEvents(t *testing.T) {
	ctx, l, s, spy := entitledFixture(t)

	got, err := l.Entitled(ctx, "sso")
	if err != nil {
		t.Fatalf("Entitled: %v", err)
	}
	if !got.Allowed || got.Limit != 1 {
		t.Errorf("got %+v, want allowed with limit 1", got)
	}
	if spy.exceeded != 0 || spy.checked != 0 {
		t.Errorf("boolean feature fired events: exceeded=%d checked=%d", spy.exceeded, spy.checked)
	}
	if _, err := s.GetCached(ctx, "t1", "app_1", "sso"); err != nil {
		t.Errorf("Entitled must cache a boolean result: %v", err)
	}
}

func TestEntitledWithoutASubscriptionCachesNothing(t *testing.T) {
	ctx, l, s, spy := entitledFixture(t)
	//nolint:staticcheck,revive // Entitled reads these string keys today
	ctx = context.WithValue(ctx, "tenant_id", "nobody")

	got, err := l.Entitled(ctx, "api_calls")
	if err != nil {
		t.Fatalf("Entitled: %v", err)
	}
	if got.Allowed || got.Reason != "no active subscription" {
		t.Errorf("got %+v", got)
	}
	if spy.exceeded != 0 || spy.checked != 0 {
		t.Errorf("no-access result fired events: exceeded=%d checked=%d", spy.exceeded, spy.checked)
	}
	if _, err := s.GetCached(ctx, "nobody", "app_1", "api_calls"); err == nil {
		t.Error("Entitled must not cache a no-access result")
	}
}
