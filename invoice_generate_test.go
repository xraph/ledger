package ledger_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/xraph/go-utils/log"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// billingFixture builds a ledger whose single plan charges $49.00 a month,
// includes 1000 api_calls, and prices overage at 3c a call on a graduated
// ladder with no upper bound.
func billingFixture(t *testing.T) (*ledger.Ledger, *memory.Store, *subscription.Subscription) {
	t.Helper()
	ctx := context.Background()

	s := memory.New()
	l := ledger.New(s)

	p := &plan.Plan{
		Entity:   types.NewEntity(),
		ID:       id.NewPlanID(),
		Name:     "Pro",
		Slug:     "pro",
		Currency: "usd",
		Status:   plan.StatusActive,
		AppID:    "app_1",
		Features: []plan.Feature{
			{
				ID: id.NewFeatureID(), Key: "api_calls", Name: "API calls",
				Type: plan.FeatureMetered, Limit: 1000, Period: plan.PeriodMonthly,
			},
		},
		Pricing: &plan.Pricing{
			ID:            id.NewPriceID(),
			BaseAmount:    types.USD(4900),
			BillingPeriod: plan.PeriodMonthly,
			Tiers: []plan.PriceTier{
				{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: 0, UnitAmount: types.USD(3)},
			},
		},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	sub := &subscription.Subscription{
		Entity:             types.NewEntity(),
		ID:                 id.NewSubscriptionID(),
		TenantID:           "tenant_1",
		PlanID:             p.ID,
		Status:             subscription.StatusActive,
		CurrentPeriodStart: time.Now().UTC().Add(-24 * time.Hour),
		CurrentPeriodEnd:   time.Now().UTC().Add(24 * time.Hour),
		AppID:              "app_1",
	}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	return l, s, sub
}

// ingestAPICalls records qty api_calls for sub's tenant and app.
func ingestAPICalls(t *testing.T, s *memory.Store, sub *subscription.Subscription, qty int64) {
	t.Helper()
	err := s.IngestBatch(context.Background(), []*meter.UsageEvent{{
		ID: id.NewUsageEventID(), TenantID: sub.TenantID, AppID: sub.AppID,
		FeatureKey: "api_calls", Quantity: qty, Timestamp: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}
}

func lineItemsOfType(inv *invoice.Invoice, kind invoice.LineItemType) []invoice.LineItem {
	var out []invoice.LineItem
	for _, li := range inv.LineItems {
		if li.Type == kind {
			out = append(out, li)
		}
	}
	return out
}

func TestGenerateInvoiceChargesTheBaseFee(t *testing.T) {
	l, _, sub := billingFixture(t)

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	base := lineItemsOfType(inv, invoice.LineItemBase)
	if len(base) != 1 {
		t.Fatalf("got %d base line items, want 1", len(base))
	}
	if !base[0].Amount.Equal(types.USD(4900)) {
		t.Errorf("got base %v, want $49.00", base[0].Amount)
	}
	if !inv.Total.Equal(types.USD(4900)) {
		t.Errorf("got total %v, want $49.00", inv.Total)
	}
}

func TestGenerateInvoicePricesOverageFromTiers(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	// 1500 calls against a 1000 allowance: 500 billable at 3c = $15.00.
	ingestAPICalls(t, s, sub, 1500)

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	over := lineItemsOfType(inv, invoice.LineItemOverage)
	if len(over) != 1 {
		t.Fatalf("got %d overage line items, want 1", len(over))
	}
	if !over[0].Amount.Equal(types.USD(1500)) {
		t.Errorf("got overage %v, want $15.00 (this is the bug this task exists to fix: it used to be zero)", over[0].Amount)
	}
	if over[0].Quantity != 500 {
		t.Errorf("got overage quantity %d, want 500", over[0].Quantity)
	}
	if !inv.Total.Equal(types.USD(4900 + 1500)) {
		t.Errorf("got total %v, want $64.00", inv.Total)
	}
}

func TestGenerateInvoiceChargesSeats(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	p.Features = append(p.Features, plan.Feature{
		ID: id.NewFeatureID(), Key: "seats", Name: "Team members",
		Type: plan.FeatureSeat, Limit: 0, Period: plan.PeriodNone,
	})
	p.Pricing.Tiers = append(p.Pricing.Tiers, plan.PriceTier{
		FeatureKey: "seats", Type: plan.TierGraduated, UpTo: 0, UnitAmount: types.USD(800),
	})
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	sub.Quantity = map[string]int64{"seats": 5}
	if err = s.UpdateSubscription(ctx, sub); err != nil {
		t.Fatalf("UpdateSubscription: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	seats := lineItemsOfType(inv, invoice.LineItemSeat)
	if len(seats) != 1 {
		t.Fatalf("got %d seat line items, want 1", len(seats))
	}
	if seats[0].Quantity != 5 {
		t.Errorf("got seat quantity %d, want 5", seats[0].Quantity)
	}
	if !seats[0].Amount.Equal(types.USD(4000)) {
		t.Errorf("got seat charge %v, want $40.00", seats[0].Amount)
	}

	// M3: the seat charge must land in Subtotal and Total, not just the
	// line item. $49.00 base plus 5 seats at $8.00 is $89.00 either way.
	if !inv.Subtotal.Equal(types.USD(8900)) {
		t.Errorf("got subtotal %v, want $89.00 (base plus seats)", inv.Subtotal)
	}
	if !inv.Total.Equal(types.USD(8900)) {
		t.Errorf("got total %v, want $89.00", inv.Total)
	}
}

func TestGenerateInvoiceAppliesPercentageCoupon(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	if err := s.CreateCoupon(ctx, &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "LAUNCH10",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: "app_1",
	}); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if _, err := l.ApplyCoupon(ctx, sub.ID, "LAUNCH10"); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	if !inv.DiscountAmount.Equal(types.USD(490)) {
		t.Errorf("got discount %v, want $4.90", inv.DiscountAmount)
	}
	if !inv.Total.Equal(types.USD(4410)) {
		t.Errorf("got total %v, want $44.10", inv.Total)
	}

	disc := lineItemsOfType(inv, invoice.LineItemDiscount)
	if len(disc) != 1 {
		t.Fatalf("got %d discount line items, want 1", len(disc))
	}
	if !strings.Contains(disc[0].Description, "LAUNCH10") {
		t.Errorf("discount line item %q does not name the coupon", disc[0].Description)
	}
}

func TestGenerateInvoiceStacksPercentageBeforeAmount(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	// $49.00, less 10% ($4.90), less $5.00 flat = $39.10.
	for _, c := range []*coupon.Coupon{
		{
			Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "PCT10",
			Type: coupon.CouponTypePercentage, Percentage: 10,
			Currency: "usd", AppID: "app_1",
		},
		{
			Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "FLAT5",
			Type: coupon.CouponTypeAmount, Amount: types.USD(500),
			Currency: "usd", AppID: "app_1",
		},
	} {
		if err := s.CreateCoupon(ctx, c); err != nil {
			t.Fatalf("CreateCoupon %s: %v", c.Code, err)
		}
		if _, err := l.ApplyCoupon(ctx, sub.ID, c.Code); err != nil {
			t.Fatalf("ApplyCoupon %s: %v", c.Code, err)
		}
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	if !inv.DiscountAmount.Equal(types.USD(990)) {
		t.Errorf("got discount %v, want $9.90 (10%% of $49.00 plus $5.00)", inv.DiscountAmount)
	}
	if !inv.Total.Equal(types.USD(3910)) {
		t.Errorf("got total %v, want $39.10", inv.Total)
	}
}

// Review Focus 2.
func TestGenerateInvoiceClampsTotalAtZero(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	if err := s.CreateCoupon(ctx, &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "HUGE",
		Type: coupon.CouponTypeAmount, Amount: types.USD(100000),
		Currency: "usd", AppID: "app_1",
	}); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if _, err := l.ApplyCoupon(ctx, sub.ID, "HUGE"); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	if inv.Total.IsNegative() {
		t.Errorf("got a negative total %v; an over-large discount must clamp at zero", inv.Total)
	}
	if !inv.Total.IsZero() {
		t.Errorf("got total %v, want zero", inv.Total)
	}
	if !inv.DiscountAmount.Equal(types.USD(100000)) {
		t.Errorf("got discount %v, want the coupon's full $1000.00 recorded even though it exceeds the subtotal", inv.DiscountAmount)
	}
}

// stubTaxCalculator returns whatever it is given, so a test can hand back
// a wrong type on purpose.
type stubTaxCalculator struct {
	result interface{}
	err    error
}

func (s *stubTaxCalculator) Name() string { return "stub-tax" }
func (s *stubTaxCalculator) CalculateTax(_ context.Context, _ interface{}, _ string) (interface{}, error) {
	return s.result, s.err
}

func TestGenerateInvoiceCalculatesTax(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s, ledger.WithPlugin(&stubTaxCalculator{result: types.USD(980)}))

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(), Slug: "pro",
		Currency: "usd", Status: plan.StatusActive, AppID: "app_1",
		Pricing: &plan.Pricing{ID: id.NewPriceID(), BaseAmount: types.USD(4900)},
	}
	_ = s.CreatePlan(ctx, p)
	sub := &subscription.Subscription{
		Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
		TenantID: "tenant_1", PlanID: p.ID,
		Status: subscription.StatusActive, AppID: "app_1",
	}
	_ = s.CreateSubscription(ctx, sub)

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	if !inv.TaxAmount.Equal(types.USD(980)) {
		t.Errorf("got tax %v, want $9.80", inv.TaxAmount)
	}
	if !inv.Total.Equal(types.USD(5880)) {
		t.Errorf("got total %v, want $58.80", inv.Total)
	}
	if len(lineItemsOfType(inv, invoice.LineItemTax)) != 1 {
		t.Error("no tax line item was emitted")
	}
}

// An invalid ladder must fail generation rather than bill $0 (ruling R4).
func TestGenerateInvoiceRejectsAnInvalidTierLadder(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)
	ingestAPICalls(t, s, sub, 1500)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	// Mixed tier types on one feature cannot be priced unambiguously.
	p.Pricing.Tiers = append(p.Pricing.Tiers, plan.PriceTier{
		FeatureKey: "api_calls", Type: plan.TierFlat, UpTo: 5000, FlatAmount: types.USD(900),
	})
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	_, err = l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, invoice.ErrInvalidTiers) {
		t.Fatalf("got %v, want an error wrapping invoice.ErrInvalidTiers", err)
	}
}

// Review Focus 3.
func TestGenerateInvoiceRejectsATaxCalculatorReturningTheWrongType(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s, ledger.WithPlugin(&stubTaxCalculator{result: "nine dollars eighty"}))

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(), Slug: "pro",
		Currency: "usd", Status: plan.StatusActive, AppID: "app_1",
		Pricing: &plan.Pricing{ID: id.NewPriceID(), BaseAmount: types.USD(4900)},
	}
	_ = s.CreatePlan(ctx, p)
	sub := &subscription.Subscription{
		Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
		TenantID: "tenant_1", PlanID: p.ID,
		Status: subscription.StatusActive, AppID: "app_1",
	}
	_ = s.CreateSubscription(ctx, sub)

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if err == nil {
		t.Fatal("got nil error; a tax plugin returning a non-Money value must fail generation rather than silently contributing nothing")
	}
	if !strings.Contains(err.Error(), "stub-tax") {
		t.Errorf("error %q does not name the offending plugin", err.Error())
	}
}

// recordingTaxCalculator records the Money it is asked to tax, and returns
// a fixed percentage of it. Used to pin exactly what GenerateInvoice hands
// the tax plugin: the net amount (subtotal less discount), not the gross
// subtotal (addendum ruling B).
type recordingTaxCalculator struct {
	received interface{}
	pct      int
}

func (r *recordingTaxCalculator) Name() string { return "recording-tax" }
func (r *recordingTaxCalculator) CalculateTax(_ context.Context, subtotal interface{}, _ string) (interface{}, error) {
	r.received = subtotal
	m := subtotal.(types.Money)
	return m.Percent(r.pct), nil
}

// TestGenerateInvoiceTaxesTheNetAmount pins addendum ruling B: tax is
// calculated on the subtotal net of discounts, not the gross subtotal. A
// 10% coupon on the $49.00 base leaves $44.10 net; a tax stub computing
// 10% of whatever it is handed must be given that net figure and return
// $4.41, for a total of $48.51 (4410 + 441).
func TestGenerateInvoiceTaxesTheNetAmount(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	stub := &recordingTaxCalculator{pct: 10}
	l := ledger.New(s, ledger.WithPlugin(stub))

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(), Slug: "pro",
		Currency: "usd", Status: plan.StatusActive, AppID: "app_1",
		Pricing: &plan.Pricing{ID: id.NewPriceID(), BaseAmount: types.USD(4900)},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	sub := &subscription.Subscription{
		Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
		TenantID: "tenant_1", PlanID: p.ID,
		Status: subscription.StatusActive, AppID: "app_1",
	}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	if err := s.CreateCoupon(ctx, &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "PCT10",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: "app_1",
	}); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if _, err := l.ApplyCoupon(ctx, sub.ID, "PCT10"); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	got, ok := stub.received.(types.Money)
	if !ok {
		t.Fatalf("tax calculator received %T, want types.Money", stub.received)
	}
	if !got.Equal(types.USD(4410)) {
		t.Errorf("tax calculator was handed %v, want the net $44.10, not the gross subtotal", got)
	}
	if !inv.DiscountAmount.Equal(types.USD(490)) {
		t.Errorf("got discount %v, want $4.90", inv.DiscountAmount)
	}
	if !inv.TaxAmount.Equal(types.USD(441)) {
		t.Errorf("got tax %v, want $4.41", inv.TaxAmount)
	}
	if !inv.Total.Equal(types.USD(4851)) {
		t.Errorf("got total %v, want $48.51 (4410 + 441)", inv.Total)
	}
}

// TestGenerateInvoiceHandlesAnUppercasePlanCurrency pins addendum ruling C:
// every Money built during generation is normalised to one lowercased
// currency, so a plan stored with an uppercase currency code never trips
// Money.Add/Subtract's case-sensitive panic.
func TestGenerateInvoiceHandlesAnUppercasePlanCurrency(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	p.Currency = "USD"
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	if err = s.CreateCoupon(ctx, &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "FIVEOFF",
		Type: coupon.CouponTypeAmount, Amount: types.USD(500),
		Currency: "usd", AppID: "app_1",
	}); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if _, err = l.ApplyCoupon(ctx, sub.ID, "FIVEOFF"); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if !inv.Total.Equal(types.USD(4400)) {
		t.Errorf("got total %v, want $44.00", inv.Total)
	}
}

// TestGenerateInvoiceRejectsABasePriceInAnotherCurrency pins addendum
// ruling D: a base price whose Money currency does not match the plan's
// must fail generation with an error wrapping ledger.ErrInvalidPricing,
// not panic inside Money.Add.
func TestGenerateInvoiceRejectsABasePriceInAnotherCurrency(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(), Slug: "pro",
		Currency: "usd", Status: plan.StatusActive, AppID: "app_1",
		Pricing: &plan.Pricing{ID: id.NewPriceID(), BaseAmount: types.EUR(4900)},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	sub := &subscription.Subscription{
		Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
		TenantID: "tenant_1", PlanID: p.ID,
		Status: subscription.StatusActive, AppID: "app_1",
	}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, ledger.ErrInvalidPricing) {
		t.Fatalf("got %v, want an error wrapping ledger.ErrInvalidPricing", err)
	}
}

// I1: a tax plugin returning a negative amount would shrink Total instead
// of taxing it, under a line item still labelled "Tax". It must be refused
// the same way a wrong type is refused.
func TestGenerateInvoiceRejectsANegativeTaxAmount(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s, ledger.WithPlugin(&stubTaxCalculator{result: types.USD(-700)}))

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(), Slug: "pro",
		Currency: "usd", Status: plan.StatusActive, AppID: "app_1",
		Pricing: &plan.Pricing{ID: id.NewPriceID(), BaseAmount: types.USD(4900)},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	sub := &subscription.Subscription{
		Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
		TenantID: "tenant_1", PlanID: p.ID,
		Status: subscription.StatusActive, AppID: "app_1",
	}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if err == nil {
		t.Fatal("got nil error; a tax plugin returning a negative amount must fail generation rather than shrinking the total")
	}
	if !strings.Contains(err.Error(), "stub-tax") {
		t.Errorf("error %q does not name the offending plugin", err.Error())
	}
}

// I2: a coupon reaching generation did not necessarily pass through
// ApplyCoupon. This one is attached through the store's low-level
// ApplyCoupon, bypassing the engine entirely, the way a direct write, a
// migration, or a different caller could.
func TestGenerateInvoiceRejectsANegativePercentageCouponAppliedThroughTheStore(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "BADPCT",
		Type: coupon.CouponTypePercentage, Percentage: -50,
		Currency: "usd", AppID: "app_1",
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if err := s.ApplyCoupon(ctx, sub.ID, c.ID); err != nil {
		t.Fatalf("store ApplyCoupon: %v", err)
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, ledger.ErrCouponInvalid) {
		t.Fatalf("got %v, want an error wrapping ledger.ErrCouponInvalid", err)
	}
}

// G6b: the upper half of the percentage range, through the store.
func TestGenerateInvoiceRejectsAPercentageAbove100AppliedThroughTheStore(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "OVER100",
		Type: coupon.CouponTypePercentage, Percentage: 150,
		Currency: "usd", AppID: "app_1",
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if err := s.ApplyCoupon(ctx, sub.ID, c.ID); err != nil {
		t.Fatalf("store ApplyCoupon: %v", err)
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, ledger.ErrCouponInvalid) {
		t.Fatalf("got %v, want an error wrapping ledger.ErrCouponInvalid", err)
	}
}

// G7: a negative amount coupon, through the store. Subtracting it would
// raise the bill under a line labelled "Discount".
func TestGenerateInvoiceRejectsANegativeAmountCouponAppliedThroughTheStore(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "NEGAMT",
		Type: coupon.CouponTypeAmount, Amount: types.USD(-500),
		Currency: "usd", AppID: "app_1",
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if err := s.ApplyCoupon(ctx, sub.ID, c.ID); err != nil {
		t.Fatalf("store ApplyCoupon: %v", err)
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, ledger.ErrCouponInvalid) {
		t.Fatalf("got %v, want an error wrapping ledger.ErrCouponInvalid", err)
	}
}

// G11: an invalid ladder fails generation even when usage is under the
// allowance, so a bad ladder fails every billing run and not only the first
// one that goes over. No usage is ingested here.
func TestGenerateInvoiceRejectsAnInvalidMeteredLadderEvenUnderTheAllowance(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	p.Pricing.Tiers = append(p.Pricing.Tiers, plan.PriceTier{
		FeatureKey: "api_calls", Type: plan.TierFlat, UpTo: 5000, FlatAmount: types.USD(900),
	})
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	_, err = l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, invoice.ErrInvalidTiers) {
		t.Fatalf("got %v, want an error wrapping invoice.ErrInvalidTiers", err)
	}
}

// G10: the same for a seat feature, with a quantity of zero.
func TestGenerateInvoiceRejectsAnInvalidSeatLadderEvenWithZeroSeats(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	p.Features = append(p.Features, plan.Feature{
		ID: id.NewFeatureID(), Key: "seats", Name: "Team members",
		Type: plan.FeatureSeat, Period: plan.PeriodNone,
	})
	p.Pricing.Tiers = append(p.Pricing.Tiers,
		plan.PriceTier{FeatureKey: "seats", Type: plan.TierGraduated, UpTo: 10, UnitAmount: types.USD(800)},
		plan.PriceTier{FeatureKey: "seats", Type: plan.TierFlat, UpTo: 0, FlatAmount: types.USD(900)},
	)
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	// sub.Quantity is left empty: zero seats.
	_, err = l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, invoice.ErrInvalidTiers) {
		t.Fatalf("got %v, want an error wrapping invoice.ErrInvalidTiers", err)
	}
}

// I2: a coupon of a type GenerateInvoice does not know how to price (once
// silently skipped) must fail generation rather than being ignored.
func TestGenerateInvoiceRejectsACouponOfAnUnsupportedTypeAppliedThroughTheStore(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "WEIRD",
		Type: coupon.CouponType("fixed"), Currency: "usd", AppID: "app_1",
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if err := s.ApplyCoupon(ctx, sub.ID, c.ID); err != nil {
		t.Fatalf("store ApplyCoupon: %v", err)
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, ledger.ErrCouponInvalid) {
		t.Fatalf("got %v, want an error wrapping ledger.ErrCouponInvalid", err)
	}
}

// I2: a $5.00 USD coupon left attached to a subscription whose plan later
// switched to JPY must not become a five-unit JPY discount. The plan here
// carries no features, matching the currency-drift scenario exactly.
func TestGenerateInvoiceRejectsACouponWhoseCurrencyDriftedFromThePlan(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(), Slug: "pro",
		Currency: "usd", Status: plan.StatusActive, AppID: "app_1",
		Pricing: &plan.Pricing{ID: id.NewPriceID(), BaseAmount: types.USD(4900)},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	sub := &subscription.Subscription{
		Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
		TenantID: "tenant_1", PlanID: p.ID,
		Status: subscription.StatusActive, AppID: "app_1",
	}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	if err := s.CreateCoupon(ctx, &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "FIVEUSD",
		Type: coupon.CouponTypeAmount, Amount: types.USD(500),
		Currency: "usd", AppID: "app_1",
	}); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if _, err := l.ApplyCoupon(ctx, sub.ID, "FIVEUSD"); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	// The plan's currency drifts after the coupon was applied.
	p.Currency = "jpy"
	p.Pricing.BaseAmount = types.JPY(4900)
	if err := s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, ledger.ErrCouponInvalid) {
		t.Fatalf("got %v, want an error wrapping ledger.ErrCouponInvalid", err)
	}
}

// M1: proves overage is priced as a differential across tier boundaries,
// not as the billable quantity times a single tier's flat rate. Limit
// 1000, ladder 2000@5c/unbounded@3c, usage 2500:
// price(2500) - price(1000) = (2000*5 + 500*3) - (1000*5) = 11500 - 5000 = 6500.
// A mutation that re-subtracts the allowance by pricing the 1500 billable
// units at the tier rate for usage (5c) alone gets 7500 instead.
func TestGenerateInvoiceDoesNotResubtractTheAllowanceAcrossTierBoundaries(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	p.Pricing.Tiers = []plan.PriceTier{
		{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: 2000, UnitAmount: types.USD(5)},
		{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: 0, UnitAmount: types.USD(3)},
	}
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	ingestAPICalls(t, s, sub, 2500)

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	over := lineItemsOfType(inv, invoice.LineItemOverage)
	if len(over) != 1 {
		t.Fatalf("got %d overage line items, want 1", len(over))
	}
	if over[0].Quantity != 1500 {
		t.Errorf("got overage quantity %d, want 1500", over[0].Quantity)
	}
	if !over[0].Amount.Equal(types.USD(6500)) {
		t.Errorf("got overage %v, want $65.00 (priced as the differential across tier boundaries, not the allowance re-subtracted as a flat rate)", over[0].Amount)
	}
}

// M2: two percentage coupons must each compute against the original
// subtotal, not compound on top of each other. Applying the 20% coupon
// first, so the order differs from TestGenerateInvoiceStacksPercentageBeforeAmount,
// covers both application orders across the two tests. Correct: 490 (10%
// of 4900) + 980 (20% of 4900) = 1470, total 3430. Compounding 20% first
// gives 980 + 10% of the discounted 3920 = 1372 instead.
func TestGenerateInvoiceStacksTwoPercentagesWithoutCompounding(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	for _, c := range []*coupon.Coupon{
		{
			Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "PCT20",
			Type: coupon.CouponTypePercentage, Percentage: 20,
			Currency: "usd", AppID: "app_1",
		},
		{
			Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "PCT10",
			Type: coupon.CouponTypePercentage, Percentage: 10,
			Currency: "usd", AppID: "app_1",
		},
	} {
		if err := s.CreateCoupon(ctx, c); err != nil {
			t.Fatalf("CreateCoupon %s: %v", c.Code, err)
		}
		if _, err := l.ApplyCoupon(ctx, sub.ID, c.Code); err != nil {
			t.Fatalf("ApplyCoupon %s: %v", c.Code, err)
		}
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	if !inv.DiscountAmount.Equal(types.USD(1470)) {
		t.Errorf("got discount %v, want $14.70 (two independent percentages against the original subtotal, not compounded)", inv.DiscountAmount)
	}
	if !inv.Total.Equal(types.USD(3430)) {
		t.Errorf("got total %v, want $34.30", inv.Total)
	}
}

// M4: seats bill against a zero allowance, never the feature's own Limit.
// Limit 5, quantity 3, ladder unbounded@800c: all 3 seats are billed
// (2400), not zero.
func TestGenerateInvoiceSeatsBillAgainstZeroAllowanceNotTheFeatureLimit(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	p.Features = append(p.Features, plan.Feature{
		ID: id.NewFeatureID(), Key: "seats", Name: "Team members",
		Type: plan.FeatureSeat, Limit: 5, Period: plan.PeriodNone,
	})
	p.Pricing.Tiers = append(p.Pricing.Tiers, plan.PriceTier{
		FeatureKey: "seats", Type: plan.TierGraduated, UpTo: 0, UnitAmount: types.USD(800),
	})
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	sub.Quantity = map[string]int64{"seats": 3}
	if err = s.UpdateSubscription(ctx, sub); err != nil {
		t.Fatalf("UpdateSubscription: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	seats := lineItemsOfType(inv, invoice.LineItemSeat)
	if len(seats) != 1 {
		t.Fatalf("got %d seat line items, want 1", len(seats))
	}
	if seats[0].Quantity != 3 {
		t.Errorf("got seat quantity %d, want 3", seats[0].Quantity)
	}
	if !seats[0].Amount.Equal(types.USD(2400)) {
		t.Errorf("got seat charge %v, want $24.00 (all 3 seats billed against a zero allowance, not the feature's Limit of 5)", seats[0].Amount)
	}
	if !inv.Total.Equal(types.USD(7300)) {
		t.Errorf("got total %v, want $73.00", inv.Total)
	}
}

// M6: a tax calculator's returned currency must be normalised to the
// plan's lowercased currency, not copied verbatim. Money.Equal is
// case-sensitive, which is what makes this discriminate.
func TestGenerateInvoiceNormalisesTheTaxCalculatorsCurrency(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s, ledger.WithPlugin(&stubTaxCalculator{result: types.Money{Amount: 980, Currency: "USD"}}))

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(), Slug: "pro",
		Currency: "usd", Status: plan.StatusActive, AppID: "app_1",
		Pricing: &plan.Pricing{ID: id.NewPriceID(), BaseAmount: types.USD(4900)},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	sub := &subscription.Subscription{
		Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
		TenantID: "tenant_1", PlanID: p.ID,
		Status: subscription.StatusActive, AppID: "app_1",
	}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	if !inv.TaxAmount.Equal(types.Money{Amount: 980, Currency: "usd"}) {
		t.Errorf("got tax %+v, want {Amount:980 Currency:usd}", inv.TaxAmount)
	}
	if !inv.Total.Equal(types.Money{Amount: 5880, Currency: "usd"}) {
		t.Errorf("got total %+v, want {Amount:5880 Currency:usd}", inv.Total)
	}
}

// M7: a metered feature with an unlimited allowance (Limit -1) is skipped
// entirely, including ladder validation. An invalid ladder attached to
// such a feature must never surface, since the feature can never bill.
func TestGenerateInvoiceSkipsValidationForAnUnlimitedMeteredFeature(t *testing.T) {
	ctx := context.Background()
	l, s, sub := billingFixture(t)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	p.Features[0].Limit = -1
	// Mixed tier types on one feature: an invalid ladder.
	p.Pricing.Tiers = []plan.PriceTier{
		{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: 1000, UnitAmount: types.USD(3)},
		{FeatureKey: "api_calls", Type: plan.TierFlat, UpTo: 0, FlatAmount: types.USD(900)},
	}
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	ingestAPICalls(t, s, sub, 5000)

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if len(lineItemsOfType(inv, invoice.LineItemOverage)) != 0 {
		t.Errorf("got an overage line item for an unlimited feature with an invalid ladder, want none")
	}
}

// countingAggregator returns a fixed total and records how many events it saw.
type countingAggregator struct {
	total int64
	err   error
	seen  int
}

func (a *countingAggregator) Name() string           { return "stub-agg" }
func (a *countingAggregator) AggregatorName() string { return "stub-agg" }
func (a *countingAggregator) Aggregate(_ context.Context, events []interface{}) (int64, error) {
	a.seen = len(events)
	return a.total, a.err
}

// fixedStrategy returns a fixed result and counts how often it is asked.
type fixedStrategy struct {
	result interface{}
	calls  int

	// What the last call was handed.
	tiers    []interface{}
	usage    int64
	included int64
	currency string
}

func (s *fixedStrategy) Name() string         { return "stub-pricing" }
func (s *fixedStrategy) StrategyName() string { return "stub-pricing" }
func (s *fixedStrategy) Compute(tiers []interface{}, usage, included int64, currency string) interface{} {
	s.calls++
	s.tiers, s.usage, s.included, s.currency = tiers, usage, included, currency
	return s.result
}

// namedStrategy is a fixedStrategy registered under its own name, so a test
// can register two strategies at once.
type namedStrategy struct {
	fixedStrategy
	name string
}

func (s *namedStrategy) Name() string         { return s.name }
func (s *namedStrategy) StrategyName() string { return s.name }

// warnRecord is one captured Warn call.
type warnRecord struct {
	msg    string
	fields map[string]any
}

// captureLogger records Warn calls and discards everything else.
type captureLogger struct {
	log.Logger
	mu    sync.Mutex
	warns []warnRecord
}

func newCaptureLogger() *captureLogger {
	return &captureLogger{Logger: log.NewNoopLogger()}
}

func (c *captureLogger) Warn(msg string, fields ...log.Field) {
	rec := warnRecord{msg: msg, fields: map[string]any{}}
	for _, f := range fields {
		rec.fields[f.Key()] = f.Value()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.warns = append(c.warns, rec)
}

func (c *captureLogger) recorded() []warnRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]warnRecord(nil), c.warns...)
}

// hookFixture builds billingFixture's plan and subscription on a ledger that
// carries the given plugins, and lets the caller edit the plan first.
func hookFixture(t *testing.T, edit func(p *plan.Plan), plugins ...plugin.Plugin) (*ledger.Ledger, *memory.Store, *subscription.Subscription) {
	t.Helper()
	ctx := context.Background()

	opts := make([]ledger.Option, 0, len(plugins))
	for _, pl := range plugins {
		opts = append(opts, ledger.WithPlugin(pl))
	}

	// Reuse billingFixture for the plan and subscription, then rebuild the
	// ledger over the same store with the plugins attached.
	_, s, sub := billingFixture(t)
	l := ledger.New(s, opts...)

	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	edit(p)
	if err := s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	return l, s, sub
}

func setFeatureMeta(key, value string) func(*plan.Plan) {
	return func(p *plan.Plan) {
		if p.Features[0].Metadata == nil {
			p.Features[0].Metadata = map[string]string{}
		}
		p.Features[0].Metadata[key] = value
	}
}

func overageAmount(t *testing.T, inv *invoice.Invoice) types.Money {
	t.Helper()
	over := lineItemsOfType(inv, invoice.LineItemOverage)
	if len(over) == 0 {
		return types.Zero("usd")
	}
	if len(over) != 1 {
		t.Fatalf("got %d overage line items, want at most 1", len(over))
	}
	return over[0].Amount
}

func TestGenerateInvoiceUsesANamedUsageAggregator(t *testing.T) {
	ctx := context.Background()
	agg := &countingAggregator{total: 1500}
	l, s, sub := hookFixture(t, setFeatureMeta("aggregator", "stub-agg"), agg)

	// One event inside the billing period, one before it. The aggregator
	// must see only the first.
	ingestAPICalls(t, s, sub, 1)
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{{
		ID: id.NewUsageEventID(), TenantID: sub.TenantID, AppID: sub.AppID,
		FeatureKey: "api_calls", Quantity: 1,
		Timestamp: sub.CurrentPeriodStart.Add(-time.Hour),
	}}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	// The aggregator reported 1500 against a 1000 allowance at 3c.
	if got := overageAmount(t, inv); !got.Equal(types.USD(1500)) {
		t.Errorf("got overage %v, want $15.00 priced from the aggregator's total", got)
	}
	if agg.seen != 1 {
		t.Errorf("aggregator saw %d events, want 1: it must be bounded to the billing period", agg.seen)
	}
}

func TestGenerateInvoiceFallsBackWhenTheAggregatorIsNotRegistered(t *testing.T) {
	l, s, sub := hookFixture(t, setFeatureMeta("aggregator", "absent"))
	ingestAPICalls(t, s, sub, 1500)

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if got := overageAmount(t, inv); !got.Equal(types.USD(1500)) {
		t.Errorf("got overage %v, want the store's $15.00", got)
	}
}

func TestGenerateInvoiceUsesANamedPricingStrategy(t *testing.T) {
	cases := []struct {
		name string
		edit func(*plan.Plan)
	}{
		{"named on the feature", setFeatureMeta("pricing_strategy", "stub-pricing")},
		{"named on the plan", func(p *plan.Plan) {
			p.Metadata = map[string]string{"pricing_strategy": "stub-pricing"}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			strategy := &fixedStrategy{result: types.USD(12345)}
			l, s, sub := hookFixture(t, c.edit, strategy)
			ingestAPICalls(t, s, sub, 1500)

			inv, err := l.GenerateInvoice(context.Background(), sub.ID)
			if err != nil {
				t.Fatalf("GenerateInvoice: %v", err)
			}
			if strategy.calls != 1 {
				t.Errorf("strategy called %d times, want 1", strategy.calls)
			}
			if got := overageAmount(t, inv); !got.Equal(types.USD(12345)) {
				t.Errorf("got overage %v, want the strategy's $123.45", got)
			}
		})
	}
}

func TestGenerateInvoiceDoesNotCallAStrategyWithinTheAllowance(t *testing.T) {
	strategy := &fixedStrategy{result: types.USD(12345)}
	l, s, sub := hookFixture(t, setFeatureMeta("pricing_strategy", "stub-pricing"), strategy)
	ingestAPICalls(t, s, sub, 500)

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if strategy.calls != 0 {
		t.Errorf("strategy called %d times for usage inside the allowance, want 0", strategy.calls)
	}
	if got := overageAmount(t, inv); !got.IsZero() {
		t.Errorf("got overage %v, want none", got)
	}
}

func TestGenerateInvoiceRejectsABadStrategyResult(t *testing.T) {
	cases := []struct {
		name   string
		result interface{}
	}{
		{"nil", nil},
		{"wrong type", "one hundred dollars"},
		{"wrong currency", types.EUR(100)},
		{"negative", types.USD(-100)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, s, sub := hookFixture(t, setFeatureMeta("pricing_strategy", "stub-pricing"),
				&fixedStrategy{result: c.result})
			ingestAPICalls(t, s, sub, 1500)

			_, err := l.GenerateInvoice(context.Background(), sub.ID)
			if err == nil {
				t.Fatal("got nil error; a bad strategy result must fail generation")
			}
			if !strings.Contains(err.Error(), "stub-pricing") {
				t.Errorf("error %q does not name the strategy", err.Error())
			}
		})
	}
}

func TestGenerateInvoiceFallsBackWhenTheStrategyIsNotRegistered(t *testing.T) {
	l, s, sub := hookFixture(t, setFeatureMeta("pricing_strategy", "absent"))
	ingestAPICalls(t, s, sub, 1500)

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if got := overageAmount(t, inv); !got.Equal(types.USD(1500)) {
		t.Errorf("got overage %v, want the built-in $15.00", got)
	}
}

func TestGenerateInvoiceAcceptsAStrategyResultInAnotherCurrencyCasing(t *testing.T) {
	strategy := &fixedStrategy{result: types.Money{Amount: 500, Currency: "USD"}}
	l, s, sub := hookFixture(t, setFeatureMeta("pricing_strategy", "stub-pricing"), strategy)
	ingestAPICalls(t, s, sub, 1500)

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if got := overageAmount(t, inv); !got.Equal(types.USD(500)) {
		t.Errorf("got overage %v, want $5.00 normalised to the plan's currency", got)
	}
}

func TestGenerateInvoicePricesASeatFeatureWithANamedStrategy(t *testing.T) {
	ctx := context.Background()
	strategy := &fixedStrategy{result: types.USD(777)}
	l, s, sub := hookFixture(t, func(p *plan.Plan) {
		p.Features = append(p.Features, plan.Feature{
			ID: id.NewFeatureID(), Key: "seats", Name: "Team members",
			Type: plan.FeatureSeat, Limit: 0, Period: plan.PeriodNone,
			Metadata: map[string]string{"pricing_strategy": "stub-pricing"},
		})
		p.Pricing.Tiers = append(p.Pricing.Tiers, plan.PriceTier{
			FeatureKey: "seats", Type: plan.TierGraduated, UpTo: 0, UnitAmount: types.USD(800),
		})
	}, strategy)

	sub.Quantity = map[string]int64{"seats": 5}
	if err := s.UpdateSubscription(ctx, sub); err != nil {
		t.Fatalf("UpdateSubscription: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	seats := lineItemsOfType(inv, invoice.LineItemSeat)
	if len(seats) != 1 {
		t.Fatalf("got %d seat line items, want 1", len(seats))
	}
	if !seats[0].Amount.Equal(types.USD(777)) {
		t.Errorf("got seat charge %v, want the strategy's $7.77 rather than the built-in $40.00", seats[0].Amount)
	}
	if strategy.calls != 1 {
		t.Errorf("strategy called %d times, want 1", strategy.calls)
	}
}

func TestGenerateInvoiceUsesTheFeaturesStrategyOverThePlans(t *testing.T) {
	featureStrat := &namedStrategy{name: "feature-pricing", fixedStrategy: fixedStrategy{result: types.USD(1111)}}
	planStrat := &namedStrategy{name: "plan-pricing", fixedStrategy: fixedStrategy{result: types.USD(2222)}}

	l, s, sub := hookFixture(t, func(p *plan.Plan) {
		setFeatureMeta("pricing_strategy", "feature-pricing")(p)
		p.Metadata = map[string]string{"pricing_strategy": "plan-pricing"}
	}, featureStrat, planStrat)
	ingestAPICalls(t, s, sub, 1500)

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if got := overageAmount(t, inv); !got.Equal(types.USD(1111)) {
		t.Errorf("got overage %v, want the feature's $11.11", got)
	}
	if featureStrat.calls != 1 {
		t.Errorf("feature strategy called %d times, want 1", featureStrat.calls)
	}
	if planStrat.calls != 0 {
		t.Errorf("plan strategy called %d times, want 0: the feature's choice wins", planStrat.calls)
	}
}

func TestGenerateInvoiceHandsAStrategyItsDocumentedArguments(t *testing.T) {
	strategy := &fixedStrategy{result: types.USD(100)}
	l, s, sub := hookFixture(t, func(p *plan.Plan) {
		setFeatureMeta("pricing_strategy", "stub-pricing")(p)
		p.Currency = "USD"
		// Listed out of order, plus a tier for another feature that must
		// not be passed along.
		p.Pricing.Tiers = []plan.PriceTier{
			{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: 0, UnitAmount: types.USD(2)},
			{FeatureKey: "other", Type: plan.TierGraduated, UpTo: 0, UnitAmount: types.USD(9)},
			{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: 5000, UnitAmount: types.USD(3)},
		}
	}, strategy)
	ingestAPICalls(t, s, sub, 1500)

	if _, err := l.GenerateInvoice(context.Background(), sub.ID); err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	if strategy.usage != 1500 || strategy.included != 1000 {
		t.Errorf("got usage %d and included %d, want 1500 and 1000", strategy.usage, strategy.included)
	}
	if strategy.currency != "usd" {
		t.Errorf("got currency %q, want it lowercased", strategy.currency)
	}
	if len(strategy.tiers) != 2 {
		t.Fatalf("got %d tiers, want the 2 belonging to the feature", len(strategy.tiers))
	}
	first, ok1 := strategy.tiers[0].(plan.PriceTier)
	second, ok2 := strategy.tiers[1].(plan.PriceTier)
	if !ok1 || !ok2 {
		t.Fatalf("got tier types %T and %T, want plan.PriceTier values", strategy.tiers[0], strategy.tiers[1])
	}
	if first.UpTo != 5000 || second.UpTo != 0 {
		t.Errorf("got UpTo %d then %d, want the bounded tier first and the unbounded one last", first.UpTo, second.UpTo)
	}
}

func TestGenerateInvoiceBoundsTheAggregatorAtThePeriodEnd(t *testing.T) {
	ctx := context.Background()
	agg := &countingAggregator{total: 1500}
	l, s, sub := hookFixture(t, setFeatureMeta("aggregator", "stub-agg"), agg)

	ingestAPICalls(t, s, sub, 1)
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{{
		ID: id.NewUsageEventID(), TenantID: sub.TenantID, AppID: sub.AppID,
		FeatureKey: "api_calls", Quantity: 1,
		Timestamp: sub.CurrentPeriodEnd.Add(time.Hour),
	}}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	if _, err := l.GenerateInvoice(ctx, sub.ID); err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if agg.seen != 1 {
		t.Errorf("aggregator saw %d events, want 1: an event after the period end must not be billed", agg.seen)
	}
}

func TestGenerateInvoiceRefusesABadAggregatorResult(t *testing.T) {
	cases := []struct {
		name string
		agg  *countingAggregator
	}{
		{"negative total", &countingAggregator{total: -5}},
		{"error", &countingAggregator{err: errors.New("backend down")}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, s, sub := hookFixture(t, setFeatureMeta("aggregator", "stub-agg"), c.agg)
			ingestAPICalls(t, s, sub, 1500)

			_, err := l.GenerateInvoice(context.Background(), sub.ID)
			if err == nil {
				t.Fatal("got nil error; a bad aggregator result must fail generation")
			}
			if !strings.Contains(err.Error(), `"stub-agg"`) {
				t.Errorf("error %q does not name the aggregator", err.Error())
			}
		})
	}
}

func TestGenerateInvoiceWarnsWhenAnAggregatorIsNotRegistered(t *testing.T) {
	cl := newCaptureLogger()
	_, s, sub := hookFixture(t, setFeatureMeta("aggregator", "absent"))
	l := ledger.New(s, ledger.WithLogger(cl))
	ingestAPICalls(t, s, sub, 1500)

	if _, err := l.GenerateInvoice(context.Background(), sub.ID); err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	warns := cl.recorded()
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
	if !strings.Contains(warns[0].msg, "unregistered usage aggregator") {
		t.Errorf("warning %q does not say the aggregator is unregistered", warns[0].msg)
	}
	if got := warns[0].fields["aggregator"]; got != "absent" {
		t.Errorf("got aggregator field %v, want %q", got, "absent")
	}
}

func TestGenerateInvoiceWarnsWhenAStrategyIsNotRegistered(t *testing.T) {
	cases := []struct {
		name      string
		edit      func(*plan.Plan)
		wantNamer string
	}{
		{"named on the feature", setFeatureMeta("pricing_strategy", "absent"), "feature names"},
		{"named on the plan", func(p *plan.Plan) {
			p.Metadata = map[string]string{"pricing_strategy": "absent"}
		}, "plan names"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := newCaptureLogger()
			_, s, sub := hookFixture(t, c.edit)
			l := ledger.New(s, ledger.WithLogger(cl))
			ingestAPICalls(t, s, sub, 1500)

			if _, err := l.GenerateInvoice(context.Background(), sub.ID); err != nil {
				t.Fatalf("GenerateInvoice: %v", err)
			}

			warns := cl.recorded()
			if len(warns) != 1 {
				t.Fatalf("got %d warnings, want 1", len(warns))
			}
			if !strings.Contains(warns[0].msg, "unregistered pricing strategy") {
				t.Errorf("warning %q does not say the strategy is unregistered", warns[0].msg)
			}
			if !strings.Contains(warns[0].msg, c.wantNamer) {
				t.Errorf("warning %q does not blame the %s", warns[0].msg, c.wantNamer)
			}
			if got := warns[0].fields["strategy"]; got != "absent" {
				t.Errorf("got strategy field %v, want %q", got, "absent")
			}
		})
	}
}

// requireNoInvoiceStored fails when generation left an invoice behind.
func requireNoInvoiceStored(t *testing.T, s *memory.Store, sub *subscription.Subscription) {
	t.Helper()
	stored, err := s.ListInvoices(context.Background(), sub.TenantID, sub.AppID, invoice.ListOpts{})
	if err != nil {
		t.Fatalf("ListInvoices: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("got %d stored invoices after a refused generation, want none", len(stored))
	}
}

// setBaseAmount edits the fixture plan's base price.
func setBaseAmount(amount int64) func(*plan.Plan) {
	return func(p *plan.Plan) { p.Pricing.BaseAmount = types.USD(amount) }
}

// A reviewer's aggregator returned MaxInt64/2. At 3c a call that used to wrap
// to an overage line of -$46,116,860,184,273,909.07 and a Total clamped to
// $0.00. It must be an error, and nothing may be stored.
func TestGenerateInvoiceRefusesAnAggregatorTotalThatOverflowsPricing(t *testing.T) {
	agg := &countingAggregator{total: math.MaxInt64 / 2}
	l, s, sub := hookFixture(t, setFeatureMeta("aggregator", "stub-agg"), agg)
	ingestAPICalls(t, s, sub, 1)

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if !errors.Is(err, types.ErrOverflow) {
		t.Fatalf("got invoice %v, err %v, want an error wrapping types.ErrOverflow", inv, err)
	}
	if inv != nil {
		t.Errorf("got an invoice alongside the error: %+v", inv)
	}
	if !strings.Contains(err.Error(), `"api_calls"`) {
		t.Errorf("error %q does not name the feature", err.Error())
	}
	requireNoInvoiceStored(t, s, sub)
}

func TestGenerateInvoiceRefusesASubtotalThatOverflows(t *testing.T) {
	ctx := context.Background()
	l, s, sub := hookFixture(t, setBaseAmount(math.MaxInt64))

	// A base price at MaxInt64 plus one seat at $8.00.
	p, err := s.GetPlan(ctx, sub.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	p.Features = append(p.Features, plan.Feature{
		ID: id.NewFeatureID(), Key: "seats", Name: "Team members",
		Type: plan.FeatureSeat, Limit: 0, Period: plan.PeriodNone,
	})
	p.Pricing.Tiers = append(p.Pricing.Tiers, plan.PriceTier{
		FeatureKey: "seats", Type: plan.TierGraduated, UpTo: 0, UnitAmount: types.USD(800),
	})
	if err = s.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	sub.Quantity = map[string]int64{"seats": 1}
	if err = s.UpdateSubscription(ctx, sub); err != nil {
		t.Fatalf("UpdateSubscription: %v", err)
	}

	_, err = l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, types.ErrOverflow) {
		t.Fatalf("got %v, want an error wrapping types.ErrOverflow", err)
	}
	if !strings.Contains(err.Error(), "subtotal") {
		t.Errorf("error %q does not name the subtotal stage", err.Error())
	}
	requireNoInvoiceStored(t, s, sub)
}

func TestGenerateInvoiceRefusesAPercentageDiscountThatOverflows(t *testing.T) {
	ctx := context.Background()
	l, s, sub := hookFixture(t, setBaseAmount(math.MaxInt64))

	if err := s.CreateCoupon(ctx, &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(), Code: "HALF",
		Type: coupon.CouponTypePercentage, Percentage: 50,
		Currency: "usd", AppID: "app_1",
	}); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if _, err := l.ApplyCoupon(ctx, sub.ID, "HALF"); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, types.ErrOverflow) {
		t.Fatalf("got %v, want an error wrapping types.ErrOverflow", err)
	}
	if !strings.Contains(err.Error(), `"HALF"`) {
		t.Errorf("error %q does not name the coupon", err.Error())
	}
	requireNoInvoiceStored(t, s, sub)
}

func TestGenerateInvoiceRefusesADiscountTotalThatOverflows(t *testing.T) {
	ctx := context.Background()
	l, s, sub := hookFixture(t, setBaseAmount(math.MaxInt64))

	for _, code := range []string{"BIG1", "BIG2"} {
		c := &coupon.Coupon{
			Entity: types.NewEntity(), ID: id.NewCouponID(), Code: code,
			Type: coupon.CouponTypeAmount, Amount: types.USD(math.MaxInt64/2 + 1),
			Currency: "usd", AppID: "app_1",
		}
		if err := s.CreateCoupon(ctx, c); err != nil {
			t.Fatalf("CreateCoupon: %v", err)
		}
		if err := s.ApplyCoupon(ctx, sub.ID, c.ID); err != nil {
			t.Fatalf("store ApplyCoupon: %v", err)
		}
	}

	_, err := l.GenerateInvoice(ctx, sub.ID)
	if !errors.Is(err, types.ErrOverflow) {
		t.Fatalf("got %v, want an error wrapping types.ErrOverflow", err)
	}
	if !strings.Contains(err.Error(), "discount") {
		t.Errorf("error %q does not name the discount stage", err.Error())
	}
	requireNoInvoiceStored(t, s, sub)
}

func TestGenerateInvoiceRefusesATaxTotalThatOverflows(t *testing.T) {
	half := &stubTaxCalculator{result: types.USD(math.MaxInt64/2 + 1)}
	other := &namedTax{stubTaxCalculator: stubTaxCalculator{result: types.USD(math.MaxInt64/2 + 1)}, name: "other-tax"}
	l, s, sub := hookFixture(t, func(*plan.Plan) {}, half, other)

	_, err := l.GenerateInvoice(context.Background(), sub.ID)
	if !errors.Is(err, types.ErrOverflow) {
		t.Fatalf("got %v, want an error wrapping types.ErrOverflow", err)
	}
	if !strings.Contains(err.Error(), "tax") {
		t.Errorf("error %q does not name the tax stage", err.Error())
	}
	requireNoInvoiceStored(t, s, sub)
}

func TestGenerateInvoiceRefusesATotalThatOverflows(t *testing.T) {
	tax := &stubTaxCalculator{result: types.USD(1)}
	l, s, sub := hookFixture(t, setBaseAmount(math.MaxInt64), tax)

	_, err := l.GenerateInvoice(context.Background(), sub.ID)
	if !errors.Is(err, types.ErrOverflow) {
		t.Fatalf("got %v, want an error wrapping types.ErrOverflow", err)
	}
	if !strings.Contains(err.Error(), "total") {
		t.Errorf("error %q does not name the total stage", err.Error())
	}
	requireNoInvoiceStored(t, s, sub)
}

// The largest bill that fits must still generate, so the checks refuse
// overflow and nothing else.
func TestGenerateInvoiceAcceptsTheLargestTotalThatFits(t *testing.T) {
	l, _, sub := hookFixture(t, setBaseAmount(math.MaxInt64))

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if !inv.Total.Equal(types.USD(math.MaxInt64)) {
		t.Errorf("got total %v, want %v", inv.Total, types.USD(math.MaxInt64))
	}
}

// namedTax is a stubTaxCalculator under its own name, so two can be
// registered at once.
type namedTax struct {
	stubTaxCalculator
	name string
}

func (n *namedTax) Name() string { return n.name }

// A negative quantity is legal to meter (a correction, say), so the store's
// total for a period can come back below zero. Billing that as nothing
// would hide the problem; it must fail and name the feature instead.
func TestGenerateInvoiceRefusesANegativeStoreTotal(t *testing.T) {
	l, s, sub := billingFixture(t)
	ingestAPICalls(t, s, sub, -50)

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if err == nil {
		t.Fatalf("got invoice with total %v, want an error for a negative usage total", inv.Total)
	}
	if !strings.Contains(err.Error(), `"api_calls"`) {
		t.Errorf("error %q does not name the feature", err.Error())
	}
	requireNoInvoiceStored(t, s, sub)
}

// Refusing a negative total at billing time must not stop anyone metering a
// negative quantity.
func TestMeterStillAcceptsANegativeQuantity(t *testing.T) {
	l, _, _ := billingFixture(t)
	ctx := context.WithValue(context.Background(), "tenant_id", "tenant_1")
	ctx = context.WithValue(ctx, "app_id", "app_1")

	if err := l.Meter(ctx, "api_calls", -50); err != nil {
		t.Fatalf("Meter(-50): %v", err)
	}
}

func TestGenerateInvoiceRefusesANegativeBaseAmount(t *testing.T) {
	l, s, sub := hookFixture(t, setBaseAmount(-100))

	inv, err := l.GenerateInvoice(context.Background(), sub.ID)
	if !errors.Is(err, ledger.ErrInvalidPricing) {
		t.Fatalf("got invoice %v, err %v, want an error wrapping ErrInvalidPricing", inv, err)
	}
	requireNoInvoiceStored(t, s, sub)
}
