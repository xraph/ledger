package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// summingAggregator adds up the quantities it is handed, the way a real
// aggregator would, and counts how often it is called.
type summingAggregator struct {
	calls int
}

func (a *summingAggregator) Name() string           { return "sum-agg" }
func (a *summingAggregator) AggregatorName() string { return "sum-agg" }
func (a *summingAggregator) Aggregate(_ context.Context, events []interface{}) (int64, error) {
	a.calls++
	var total int64
	for _, e := range events {
		if ev, ok := e.(*meter.UsageEvent); ok {
			total += ev.Quantity
		}
	}
	return total, nil
}

// useSumAggregator names sum-agg on the fixture's metered feature and drops
// its allowance to zero, so any usage the aggregator sees is billed.
func useSumAggregator(p *plan.Plan) {
	p.Features[0].Limit = 0
	setFeatureMeta("aggregator", "sum-agg")(p)
}

// storeSubscriptionLike writes, straight to the store, a subscription on
// like's plan and period under the given tenant and app. It goes around
// Ledger.CreateSubscription on purpose: that refuses an empty tenant, and
// these tests need rows a direct store write, an import or an older binary
// could still leave behind.
func storeSubscriptionLike(t *testing.T, s *memory.Store, like *subscription.Subscription, tenantID, appID string) *subscription.Subscription {
	t.Helper()
	sub := &subscription.Subscription{
		Entity:             types.NewEntity(),
		ID:                 id.NewSubscriptionID(),
		TenantID:           tenantID,
		PlanID:             like.PlanID,
		Status:             subscription.StatusActive,
		CurrentPeriodStart: like.CurrentPeriodStart,
		CurrentPeriodEnd:   like.CurrentPeriodEnd,
		AppID:              appID,
	}
	if err := s.CreateSubscription(context.Background(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	return sub
}

// The final review's probe: a subscription with no tenant, on a feature a
// plugin aggregator totals, with usage recorded under two real tenants. The
// store reads an empty tenant as "every tenant", so before the fix the
// invoice billed 10 + 20 = 30 calls from other customers.
func TestGenerateInvoiceRefusesAnEmptyTenantWithAPluginAggregator(t *testing.T) {
	agg := &summingAggregator{}
	l, s, sub := hookFixture(t, useSumAggregator, agg)

	ingest(t, s, &subscription.Subscription{TenantID: "tenant_a", AppID: sub.AppID}, "api_calls", 10)
	ingest(t, s, &subscription.Subscription{TenantID: "tenant_b", AppID: sub.AppID}, "api_calls", 20)

	orphan := storeSubscriptionLike(t, s, sub, "", sub.AppID)

	inv, err := l.GenerateInvoice(context.Background(), orphan.ID)
	if !errors.Is(err, ledger.ErrInvalidInput) {
		var total types.Money
		if inv != nil {
			total = inv.Total
		}
		t.Fatalf("got invoice total %v, err %v, want an error wrapping ErrInvalidInput", total, err)
	}
	if inv != nil {
		t.Errorf("got an invoice alongside the error: %+v", inv)
	}
	if agg.calls != 0 {
		t.Errorf("aggregator was called %d times, want 0: nothing may be read for an empty tenant", agg.calls)
	}
	requireNoInvoiceStored(t, s, orphan)
}

func TestGenerateInvoiceRefusesAnEmptyTenantWithoutAPluginAggregator(t *testing.T) {
	l, s, sub := billingFixture(t)
	orphan := storeSubscriptionLike(t, s, sub, "", sub.AppID)

	inv, err := l.GenerateInvoice(context.Background(), orphan.ID)
	if !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("got invoice %v, err %v, want an error wrapping ErrInvalidInput", inv, err)
	}
	requireNoInvoiceStored(t, s, orphan)
}

func TestCreateSubscriptionRefusesAnEmptyTenant(t *testing.T) {
	ctx := context.Background()
	l, s, subID := fixture(t)

	existing, err := s.GetSubscription(ctx, subID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}

	sub := &subscription.Subscription{
		ID:     id.NewSubscriptionID(),
		PlanID: existing.PlanID,
		Status: subscription.StatusActive,
		AppID:  existing.AppID,
	}
	if err := l.CreateSubscription(ctx, sub); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("got %v, want an error wrapping ErrInvalidInput", err)
	}
	if _, err := s.GetSubscription(ctx, sub.ID); err == nil {
		t.Error("the refused subscription was stored anyway")
	}
}

// A deployment that never set an app id (the extension's Config.AppID
// defaults to "") must keep invoicing: store.Aggregate matches the app
// exactly, so an empty app id leaks nothing on the store path.
func TestGenerateInvoiceBillsTheBaseFeeWithAnEmptyAppID(t *testing.T) {
	l, s, sub := billingFixture(t)
	noApp := storeSubscriptionLike(t, s, sub, "tenant_2", "")

	inv, err := l.GenerateInvoice(context.Background(), noApp.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	base := lineItemsOfType(inv, invoice.LineItemBase)
	if len(base) != 1 || !base[0].Amount.Equal(types.USD(4900)) {
		t.Fatalf("got base line items %+v, want one of $49.00", base)
	}
	if !inv.Total.Equal(types.USD(4900)) {
		t.Errorf("got total %v, want $49.00", inv.Total)
	}
}

// The plugin path reads events through QueryUsage, which drops the app
// filter for an empty app id, so there an empty app id would total every
// app's usage for the tenant.
func TestGenerateInvoiceRefusesAnEmptyAppIDWithAPluginAggregator(t *testing.T) {
	agg := &summingAggregator{}
	l, s, sub := hookFixture(t, useSumAggregator, agg)

	ingest(t, s, &subscription.Subscription{TenantID: "tenant_2", AppID: "app_other"}, "api_calls", 10)
	noApp := storeSubscriptionLike(t, s, sub, "tenant_2", "")

	inv, err := l.GenerateInvoice(context.Background(), noApp.ID)
	if !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("got invoice %v, err %v, want an error wrapping ErrInvalidInput", inv, err)
	}
	if agg.calls != 0 {
		t.Errorf("aggregator was called %d times, want 0", agg.calls)
	}
	requireNoInvoiceStored(t, s, noApp)
}
