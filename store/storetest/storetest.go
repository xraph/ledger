package storetest

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// Run executes the conformance suite against a store. newStore is called
// once per subtest so each starts from a clean database.
func Run(t *testing.T, newStore func(t *testing.T) ledgerstore.Store) {
	t.Helper()

	t.Run("PlanRoundTrip", func(t *testing.T) { testPlanRoundTrip(t, newStore(t)) })
	t.Run("PlanRoundTripWithoutPricingPlanID", func(t *testing.T) { testPlanRoundTripWithoutPricingPlanID(t, newStore(t)) })
	t.Run("PlanRoundTripWithoutChildIDs", func(t *testing.T) { testPlanRoundTripWithoutChildIDs(t, newStore(t)) })
	t.Run("CouponRoundTrip", func(t *testing.T) { testCouponRoundTrip(t, newStore(t)) })
	t.Run("GetCouponByIDUnknown", func(t *testing.T) { testGetCouponByIDUnknown(t, newStore(t)) })
	t.Run("PlanAppIsolation", func(t *testing.T) { testPlanAppIsolation(t, newStore(t)) })
	t.Run("CouponAppIsolation", func(t *testing.T) { testCouponAppIsolation(t, newStore(t)) })
	t.Run("ApplyCouponRecordsTheRedemption", func(t *testing.T) { testApplyCouponRecordsTheRedemption(t, newStore(t)) })
	t.Run("ApplyCouponIsIdempotentPerSubscription", func(t *testing.T) { testApplyCouponIsIdempotentPerSubscription(t, newStore(t)) })
	t.Run("ListAppliedCouponsIsScopedToOneSubscription", func(t *testing.T) { testListAppliedCouponsIsScopedToOneSubscription(t, newStore(t)) })
	t.Run("ListAppliedCouponsOnUnknownSubscriptionIsEmptyNotAnError", func(t *testing.T) {
		testListAppliedCouponsOnUnknownSubscriptionIsEmptyNotAnError(t, newStore(t))
	})
	t.Run("IncrementCouponRedemptions", func(t *testing.T) { testIncrementCouponRedemptions(t, newStore(t)) })
	t.Run("IncrementCouponRedemptionsOnUnknownCoupon", func(t *testing.T) { testIncrementCouponRedemptionsOnUnknownCoupon(t, newStore(t)) })
	t.Run("ApplyCouponOnUnknownCoupon", func(t *testing.T) { testApplyCouponOnUnknownCoupon(t, newStore(t)) })
	t.Run("ApplyCouponThenDeleteCouponThenList", func(t *testing.T) { testApplyCouponThenDeleteCouponThenList(t, newStore(t)) })
	t.Run("ListAppliedCouponsReturnsInApplyOrder", func(t *testing.T) { testListAppliedCouponsReturnsInApplyOrder(t, newStore(t)) })
	t.Run("ApplyCouponRejectsInvalidSubscriptionID", func(t *testing.T) { testApplyCouponRejectsInvalidSubscriptionID(t, newStore(t)) })
	t.Run("ListAppliedCouponsWithNilSubscriptionID", func(t *testing.T) { testListAppliedCouponsWithNilSubscriptionID(t, newStore(t)) })
	t.Run("ApplyCouponConcurrentDoubleApply", func(t *testing.T) { testApplyCouponConcurrentDoubleApply(t, newStore(t)) })
	t.Run("RedeemCouponRecordsAndCounts", func(t *testing.T) { testRedeemCouponRecordsAndCounts(t, newStore(t)) })
	t.Run("RedeemCouponRespectsTheCap", func(t *testing.T) { testRedeemCouponRespectsTheCap(t, newStore(t)) })
	t.Run("RedeemCouponUnlimited", func(t *testing.T) { testRedeemCouponUnlimited(t, newStore(t)) })
	t.Run("RedeemCouponNegativeCapIsUnlimited", func(t *testing.T) { testRedeemCouponNegativeCapIsUnlimited(t, newStore(t)) })
	t.Run("RedeemCouponDuplicate", func(t *testing.T) { testRedeemCouponDuplicate(t, newStore(t)) })
	t.Run("RedeemCouponRejectsBadInput", func(t *testing.T) { testRedeemCouponRejectsBadInput(t, newStore(t)) })
	t.Run("RedeemCouponConcurrentCap", func(t *testing.T) { testRedeemCouponConcurrentCap(t, newStore(t)) })
	t.Run("UpdateCouponLeavesTimesRedeemedAlone", func(t *testing.T) { testUpdateCouponLeavesTimesRedeemedAlone(t, newStore(t)) })
	t.Run("SubscriptionTenantIsolation", func(t *testing.T) { testSubscriptionTenantIsolation(t, newStore(t)) })
	t.Run("SubscriptionQuantityRoundTrip", func(t *testing.T) { testSubscriptionQuantityRoundTrip(t, newStore(t)) })
	t.Run("InvoiceTenantIsolation", func(t *testing.T) { testInvoiceTenantIsolation(t, newStore(t)) })
	t.Run("UsageTenantIsolation", func(t *testing.T) { testUsageTenantIsolation(t, newStore(t)) })
	t.Run("EmptyTenantIDBehavior", func(t *testing.T) { testEmptyTenantIDBehavior(t, newStore(t)) })
	t.Run("IngestKeylessEventsAreAllCounted", func(t *testing.T) { testIngestKeylessEventsAreAllCounted(t, newStore(t)) })
	t.Run("IngestDuplicateKeyIsCountedOnce", func(t *testing.T) { testIngestDuplicateKeyIsCountedOnce(t, newStore(t)) })
	t.Run("IngestKeyedAndKeylessMix", func(t *testing.T) { testIngestKeyedAndKeylessMix(t, newStore(t)) })
	t.Run("QueryUsageWindowIsHalfOpen", func(t *testing.T) { testQueryUsageWindowIsHalfOpen(t, newStore(t)) })
	t.Run("SubscriptionPeriodsRoundTripFromTimeNow", func(t *testing.T) {
		testSubscriptionPeriodsRoundTripFromTimeNow(t, newStore(t))
	})
	t.Run("UsageEventRoundTripsFromTimeNow", func(t *testing.T) { testUsageEventRoundTripsFromTimeNow(t, newStore(t)) })
	t.Run("CancelSubscriptionAtNowEndsIt", func(t *testing.T) { testCancelSubscriptionAtNowEndsIt(t, newStore(t)) })
	t.Run("ArchivePlanStoresTheArchive", func(t *testing.T) { testArchivePlanStoresTheArchive(t, newStore(t)) })
	t.Run("ArchiveFeatureStoresTheArchive", func(t *testing.T) { testArchiveFeatureStoresTheArchive(t, newStore(t)) })
	t.Run("MarkInvoicePaidStoresThePayment", func(t *testing.T) { testMarkInvoicePaidStoresThePayment(t, newStore(t)) })
	t.Run("MarkInvoiceVoidedStoresTheVoid", func(t *testing.T) { testMarkInvoiceVoidedStoresTheVoid(t, newStore(t)) })
	t.Run("ListFiltersCombine", func(t *testing.T) { testListFiltersCombine(t, newStore(t)) })
	t.Run("LookupsMatchEveryArgument", func(t *testing.T) { testLookupsMatchEveryArgument(t, newStore(t)) })
	t.Run("UpdateOfAMissingRowReportsIt", func(t *testing.T) { testUpdateOfAMissingRowReportsIt(t, newStore(t)) })
	t.Run("ListInvoicesBoundsAreInstants", func(t *testing.T) { testListInvoicesBoundsAreInstants(t, newStore(t)) })
	t.Run("UsageEventNearABoundaryInALocalZone", func(t *testing.T) { testUsageEventNearABoundaryInALocalZone(t, newStore(t)) })
	t.Run("ListsPageInAStableOrder", func(t *testing.T) { testListsPageInAStableOrder(t, newStore(t)) })
	t.Run("ListsPageStablyOnEqualTimestamps", func(t *testing.T) { testListsPageStablyOnEqualTimestamps(t, newStore(t)) })
	t.Run("AggregateOpensPeriodsInUTC", func(t *testing.T) { testAggregateOpensPeriodsInUTC(t, newStore(t)) })
}

// uniqueSuffix returns a value that differs on every call, including across
// separate process invocations, by borrowing a TypeID's UUIDv7-based
// randomness. The "test" prefix only labels where the value came from; it is
// never parsed back as a real entity id.
//
// Fixture values that a backend enforces as unique (a plan slug, a coupon
// code, an app id used as part of a unique index) carry this suffix so the
// suite stays repeat-run safe against a store that is migrated in place and
// never torn down, such as Postgres pointed at a persistent scratch
// database. Memory and SQLite start from an empty store on every call and
// would never need this, but using it unconditionally keeps every subtest
// identical across backends.
func uniqueSuffix() string {
	return id.New(id.Prefix("test")).String()
}

func testPlanRoundTrip(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	slug := "pro-" + uniqueSuffix()

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(),
		Name: "Pro", Slug: slug, Currency: "usd",
		Status: plan.StatusActive, AppID: appID,
		Pricing: &plan.Pricing{
			ID: id.NewPriceID(), BaseAmount: types.USD(4900),
			BillingPeriod: plan.PeriodMonthly,
		},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	got, err := s.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Slug != slug {
		t.Errorf("got slug %q, want %q", got.Slug, slug)
	}
	if got.Pricing == nil {
		t.Fatal("Pricing did not survive the round trip")
	}
	if !got.Pricing.BaseAmount.Equal(types.USD(4900)) {
		t.Errorf("got base amount %v, want $49.00", got.Pricing.BaseAmount)
	}
}

// testPlanRoundTripWithoutPricingPlanID pins down that a plan.Pricing built
// without ever setting its PlanID - the normal construction, since the
// pricing is always reached through its owning plan and doesn't strictly
// need to know its own parent's id redundantly - round-trips cleanly. The
// read-back Pricing.PlanID must come back nil, not forge a value and not
// fail the whole read.
func testPlanRoundTripWithoutPricingPlanID(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	slug := "pro-nopid-" + uniqueSuffix()

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(),
		Name: "Pro", Slug: slug, Currency: "usd",
		Status: plan.StatusActive, AppID: appID,
		Pricing: &plan.Pricing{
			ID: id.NewPriceID(), BaseAmount: types.USD(4900),
			BillingPeriod: plan.PeriodMonthly,
			// PlanID intentionally left unset.
		},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	got, err := s.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Pricing == nil {
		t.Fatal("Pricing did not survive the round trip")
	}
	if !got.Pricing.PlanID.IsNil() {
		t.Errorf("got Pricing.PlanID %q, want nil (an unset PlanID must round-trip as nil)", got.Pricing.PlanID)
	}
}

// testPlanRoundTripWithoutChildIDs pins down that a plan built the way the
// README quick start (and Ledger.CreatePlan, which only ever mints the
// plan's OWN id - never one per feature, never one for the pricing block)
// actually produces - a feature and a pricing block that never had their
// own ID set - round-trips cleanly on every backend. The read-back feature
// and pricing ids must come back nil, not fail the whole read.
func testPlanRoundTripWithoutChildIDs(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	slug := "pro-nochildids-" + uniqueSuffix()

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(),
		Name: "Pro", Slug: slug, Currency: "usd",
		Status: plan.StatusActive, AppID: appID,
		Features: []plan.Feature{
			{
				// ID intentionally left unset, as the README quick start does.
				Key: "api_calls", Name: "API Calls",
				Type: plan.FeatureMetered, Limit: 10000, Period: plan.PeriodMonthly,
			},
		},
		Pricing: &plan.Pricing{
			// ID intentionally left unset, as the README quick start does.
			BaseAmount: types.USD(4900), BillingPeriod: plan.PeriodMonthly,
		},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	got, err := s.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if len(got.Features) != 1 {
		t.Fatalf("got %d feature(s), want exactly 1", len(got.Features))
	}
	if !got.Features[0].ID.IsNil() {
		t.Errorf("got Features[0].ID %q, want nil (an unset feature ID must round-trip as nil)", got.Features[0].ID)
	}
	if got.Pricing == nil {
		t.Fatal("Pricing did not survive the round trip")
	}
	if !got.Pricing.ID.IsNil() {
		t.Errorf("got Pricing.ID %q, want nil (an unset pricing ID must round-trip as nil)", got.Pricing.ID)
	}
}

func testCouponRoundTrip(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	code := "LAUNCH10-" + uniqueSuffix()

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: code, Name: "Launch discount",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: appID,
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	byID, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if byID.Code != code {
		t.Errorf("got code %q, want %q", byID.Code, code)
	}

	byCode, err := s.GetCoupon(ctx, code, appID)
	if err != nil {
		t.Fatalf("GetCoupon: %v", err)
	}
	if byCode.ID.String() != c.ID.String() {
		t.Errorf("got id %s, want %s", byCode.ID, c.ID)
	}
}

func testGetCouponByIDUnknown(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()

	if _, err := s.GetCouponByID(ctx, id.NewCouponID()); err == nil {
		t.Fatal("got nil error for an unknown coupon, want ErrCouponNotFound")
	}
}

// testPlanAppIsolation writes a plan under each of two apps and asserts a
// list scoped to one app returns only that app's plan.
func testPlanAppIsolation(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appA := "app-a-" + uniqueSuffix()
	appB := "app-b-" + uniqueSuffix()

	pA := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(),
		Name: "Plan A", Slug: "plan-a-" + uniqueSuffix(), Currency: "usd",
		Status: plan.StatusActive, AppID: appA,
	}
	pB := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(),
		Name: "Plan B", Slug: "plan-b-" + uniqueSuffix(), Currency: "usd",
		Status: plan.StatusActive, AppID: appB,
	}
	if err := s.CreatePlan(ctx, pA); err != nil {
		t.Fatalf("CreatePlan(appA): %v", err)
	}
	if err := s.CreatePlan(ctx, pB); err != nil {
		t.Fatalf("CreatePlan(appB): %v", err)
	}

	got, err := s.ListPlans(ctx, appA, plan.ListOpts{})
	if err != nil {
		t.Fatalf("ListPlans(appA): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListPlans(appA): got %d rows, want exactly 1", len(got))
	}
	if got[0].ID.String() != pA.ID.String() {
		t.Errorf("ListPlans(appA): got id %s, want %s", got[0].ID, pA.ID)
	}
	for _, g := range got {
		if g.ID.String() == pB.ID.String() {
			t.Errorf("ListPlans(appA) leaked appB's plan %s", pB.ID)
		}
	}
}

// testCouponAppIsolation writes a coupon under each of two apps and asserts
// a list scoped to one app returns only that app's coupon.
func testCouponAppIsolation(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appA := "app-a-" + uniqueSuffix()
	appB := "app-b-" + uniqueSuffix()

	cA := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: "ISOA-" + uniqueSuffix(), Name: "Isolation A",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: appA,
	}
	cB := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: "ISOB-" + uniqueSuffix(), Name: "Isolation B",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: appB,
	}
	if err := s.CreateCoupon(ctx, cA); err != nil {
		t.Fatalf("CreateCoupon(appA): %v", err)
	}
	if err := s.CreateCoupon(ctx, cB); err != nil {
		t.Fatalf("CreateCoupon(appB): %v", err)
	}

	got, err := s.ListCoupons(ctx, appA, coupon.ListOpts{})
	if err != nil {
		t.Fatalf("ListCoupons(appA): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListCoupons(appA): got %d rows, want exactly 1", len(got))
	}
	if got[0].ID.String() != cA.ID.String() {
		t.Errorf("ListCoupons(appA): got id %s, want %s", got[0].ID, cA.ID)
	}
	for _, g := range got {
		if g.ID.String() == cB.ID.String() {
			t.Errorf("ListCoupons(appA) leaked appB's coupon %s", cB.ID)
		}
	}
}

// newTestCoupon builds a coupon fixture whose code carries a unique suffix,
// so a UNIQUE(code, app_id) index doesn't reject a second run against a
// persistent database (Postgres run twice in a row against the same DSN).
// newTestFeature builds a catalog feature with a unique key. An empty appID
// makes it global.
func newTestFeature(appID string) *feature.Feature {
	return &feature.Feature{
		Entity: types.NewEntity(), ID: id.NewFeatureID(),
		Key: "feat-" + uniqueSuffix(), Name: "Feature",
		Type: feature.FeatureMetered, DefaultLimit: 100, Period: feature.PeriodMonthly,
		Status: feature.StatusActive, AppID: appID,
	}
}

func newTestCoupon(appID string) *coupon.Coupon {
	code := "LAUNCH10-" + uniqueSuffix()
	return &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: code, Name: code,
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: appID,
	}
}

// testApplyCouponRecordsTheRedemption applies a coupon to a subscription and
// asserts the coupon shows up in that subscription's applied list.
func testApplyCouponRecordsTheRedemption(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("got %d applied coupons, want 1", len(applied))
	}
	if applied[0].ID.String() != c.ID.String() {
		t.Errorf("got coupon id %s, want %s", applied[0].ID, c.ID)
	}
}

// testApplyCouponIsIdempotentPerSubscription asserts a second Apply of the
// same coupon to the same subscription is rejected and does not duplicate
// the application row.
func testApplyCouponIsIdempotentPerSubscription(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("first ApplyCoupon: %v", err)
	}

	err := s.ApplyCoupon(ctx, subID, c.ID)
	if !errors.Is(err, ledger.ErrCouponAlreadyApplied) {
		t.Fatalf("second ApplyCoupon: got %v, want a wrap of ErrCouponAlreadyApplied", err)
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("got %d applied coupons after a duplicate apply, want 1", len(applied))
	}
}

// testListAppliedCouponsIsScopedToOneSubscription asserts a coupon applied
// to one subscription does not show up when listing another subscription's
// applied coupons.
func testListAppliedCouponsIsScopedToOneSubscription(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	mine, theirs := id.NewSubscriptionID(), id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, mine, c.ID); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	applied, err := s.ListAppliedCoupons(ctx, theirs)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("got %d applied coupons on an untouched subscription, want 0", len(applied))
	}
}

// testListAppliedCouponsOnUnknownSubscriptionIsEmptyNotAnError asserts an
// unknown subscription id is a valid, empty answer rather than an error.
func testListAppliedCouponsOnUnknownSubscriptionIsEmptyNotAnError(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()

	applied, err := s.ListAppliedCoupons(ctx, id.NewSubscriptionID())
	if err != nil {
		t.Fatalf("ListAppliedCoupons on an unknown subscription: %v", err)
	}
	if applied == nil {
		t.Error("got a nil slice, want an empty one: a caller ranging over nil sees no difference, but a caller checking len(x) == 0 against a nil map entry does")
	}
	if len(applied) != 0 {
		t.Errorf("got %d applied coupons, want 0", len(applied))
	}
}

// testIncrementCouponRedemptions increments a coupon's redemption count
// three times and asserts each increment is reflected on read-back.
func testIncrementCouponRedemptions(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	for i := 1; i <= 3; i++ {
		if err := s.IncrementCouponRedemptions(ctx, c.ID); err != nil {
			t.Fatalf("IncrementCouponRedemptions call %d: %v", i, err)
		}

		got, err := s.GetCouponByID(ctx, c.ID)
		if err != nil {
			t.Fatalf("GetCouponByID: %v", err)
		}
		if got.TimesRedeemed != i {
			t.Errorf("after %d increments: got TimesRedeemed %d, want %d", i, got.TimesRedeemed, i)
		}
	}
}

// testIncrementCouponRedemptionsOnUnknownCoupon asserts incrementing an
// unknown coupon's redemptions is rejected rather than silently no-op'd.
func testIncrementCouponRedemptionsOnUnknownCoupon(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()

	err := s.IncrementCouponRedemptions(ctx, id.NewCouponID())
	if !errors.Is(err, ledger.ErrCouponNotFound) {
		t.Fatalf("got %v, want a wrap of ErrCouponNotFound", err)
	}
}

// testApplyCouponOnUnknownCoupon asserts applying a coupon id that was
// never created is rejected with ErrCouponNotFound, and leaves no
// application row behind.
func testApplyCouponOnUnknownCoupon(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	subID := id.NewSubscriptionID()
	unknownCoupon := id.NewCouponID()

	err := s.ApplyCoupon(ctx, subID, unknownCoupon)
	if !errors.Is(err, ledger.ErrCouponNotFound) {
		t.Fatalf("ApplyCoupon on an unknown coupon: got %v, want a wrap of ErrCouponNotFound", err)
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("got %d applied coupons after a failed apply, want 0", len(applied))
	}
}

// testApplyCouponThenDeleteCouponThenList asserts that deleting a coupon
// after it was applied leaves ListAppliedCoupons returning an empty,
// non-nil slice and no error - not a failure, and not a leaked stale
// coupon.
func testApplyCouponThenDeleteCouponThenList(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	if err := s.DeleteCoupon(ctx, c.ID); err != nil {
		t.Fatalf("DeleteCoupon: %v", err)
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons after the coupon was deleted: %v", err)
	}
	if applied == nil {
		t.Error("got a nil slice, want an empty one")
	}
	if len(applied) != 0 {
		t.Errorf("got %d applied coupons after the coupon was deleted, want 0", len(applied))
	}
}

// testListAppliedCouponsReturnsInApplyOrder applies three distinct coupons
// to the same subscription in sequence and asserts the list comes back in
// that same order - the applied_at-then-id ordering the backends commit to.
func testListAppliedCouponsReturnsInApplyOrder(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	subID := id.NewSubscriptionID()

	coupons := make([]*coupon.Coupon, 3)
	for i := range coupons {
		c := newTestCoupon(appID)
		if err := s.CreateCoupon(ctx, c); err != nil {
			t.Fatalf("CreateCoupon %d: %v", i, err)
		}
		coupons[i] = c
		if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
			t.Fatalf("ApplyCoupon %d: %v", i, err)
		}
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != len(coupons) {
		t.Fatalf("got %d applied coupons, want %d", len(applied), len(coupons))
	}
	for i, c := range coupons {
		if applied[i].ID.String() != c.ID.String() {
			t.Errorf("position %d: got coupon %s, want %s (apply order)", i, applied[i].ID, c.ID)
		}
	}
}

// testApplyCouponRejectsInvalidSubscriptionID asserts ApplyCoupon rejects a
// nil subscription id and a wrong-prefix id (a plan id, standing in for
// "any id that isn't a subscription id") before touching storage.
func testApplyCouponRejectsInvalidSubscriptionID(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	if err := s.ApplyCoupon(ctx, id.Nil, c.ID); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("ApplyCoupon(id.Nil, ...): got %v, want a wrap of ErrInvalidInput", err)
	}

	wrongPrefix := id.NewPlanID()
	if err := s.ApplyCoupon(ctx, wrongPrefix, c.ID); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("ApplyCoupon(a plan id, ...): got %v, want a wrap of ErrInvalidInput", err)
	}
}

// testListAppliedCouponsWithNilSubscriptionID asserts ListAppliedCoupons
// answers a nil subscription id with an empty, non-nil slice and no error,
// without needing a query to reach storage at all.
func testListAppliedCouponsWithNilSubscriptionID(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()

	applied, err := s.ListAppliedCoupons(ctx, id.Nil)
	if err != nil {
		t.Fatalf("ListAppliedCoupons(id.Nil): got error %v, want nil", err)
	}
	if applied == nil {
		t.Error("got a nil slice, want an empty one")
	}
	if len(applied) != 0 {
		t.Errorf("got %d applied coupons, want 0", len(applied))
	}
}

// testApplyCouponConcurrentDoubleApply fires the same (subscription,
// coupon) pair at ApplyCoupon from 10 goroutines at once. Exactly one must
// win; every loser must see ErrCouponAlreadyApplied, not a raw driver
// error, and the pair must land exactly once in storage either way.
//
// This test is necessarily probabilistic: whether the race is actually
// provoked on a given run depends on scheduling and, for the SQL and mongo
// backends, on how quickly the unique index rejects the losers. It is not
// the thing that pins the typed-error mapping - see the deterministic
// internal test in each backend's own package for that - but it is the
// closest thing to a proof that the mapping also holds under real
// concurrent access through the public interface, not just when provoked
// directly against the builder.
func testApplyCouponConcurrentDoubleApply(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()

	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			errs[i] = s.ApplyCoupon(ctx, subID, c.ID)
		}(i)
	}
	wg.Wait()

	var wins, losses int
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ledger.ErrCouponAlreadyApplied):
			losses++
		default:
			t.Errorf("got an error from a concurrent ApplyCoupon that is neither nil nor ErrCouponAlreadyApplied: %v", err)
		}
	}
	if wins != 1 {
		t.Errorf("got %d winning ApplyCoupon calls out of %d, want exactly 1 (losses=%d)", wins, n, losses)
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("got %d applied coupons after a concurrent double apply, want exactly 1", len(applied))
	}
}

// testRedeemCouponRecordsAndCounts asserts a single redemption both records
// the application and increments the count, the two things RedeemCoupon
// exists to do as one unit.
func testRedeemCouponRecordsAndCounts(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.RedeemCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("RedeemCoupon: %v", err)
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("got %d applied coupons, want 1", len(applied))
	}
	if applied[0].ID.String() != c.ID.String() {
		t.Errorf("got applied coupon %s, want %s", applied[0].ID, c.ID)
	}

	got, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if got.TimesRedeemed != 1 {
		t.Errorf("got TimesRedeemed %d, want 1", got.TimesRedeemed)
	}
}

// testUpdateCouponLeavesTimesRedeemedAlone pins that UpdateCoupon writes a
// coupon's editable fields but never its redemption count: a caller holding a
// stale copy, with TimesRedeemed still 0, must not roll the count back.
func testUpdateCouponLeavesTimesRedeemedAlone(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := s.RedeemCoupon(ctx, id.NewSubscriptionID(), c.ID); err != nil {
			t.Fatalf("RedeemCoupon %d: %v", i, err)
		}
	}

	stale := *c // still carries TimesRedeemed 0
	stale.Name = "Renamed " + uniqueSuffix()
	if err := s.UpdateCoupon(ctx, &stale); err != nil {
		t.Fatalf("UpdateCoupon: %v", err)
	}

	got, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if got.Name != stale.Name {
		t.Errorf("Name = %q, want %q", got.Name, stale.Name)
	}
	if got.TimesRedeemed != 2 {
		t.Errorf("TimesRedeemed = %d, want 2: UpdateCoupon must not write the count", got.TimesRedeemed)
	}
}

// testRedeemCouponRespectsTheCap redeems a MaxRedemptions-2 coupon onto
// three different subscriptions in sequence: the first two must succeed,
// the third must be refused as exhausted, and the third subscription must
// end up with no application row of its own.
func testRedeemCouponRespectsTheCap(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	c.MaxRedemptions = 2
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	sub1, sub2, sub3 := id.NewSubscriptionID(), id.NewSubscriptionID(), id.NewSubscriptionID()

	if err := s.RedeemCoupon(ctx, sub1, c.ID); err != nil {
		t.Fatalf("RedeemCoupon sub1: %v", err)
	}
	if err := s.RedeemCoupon(ctx, sub2, c.ID); err != nil {
		t.Fatalf("RedeemCoupon sub2: %v", err)
	}
	if err := s.RedeemCoupon(ctx, sub3, c.ID); !errors.Is(err, ledger.ErrCouponExhausted) {
		t.Fatalf("RedeemCoupon sub3: got %v, want a wrap of ErrCouponExhausted", err)
	}

	applied, err := s.ListAppliedCoupons(ctx, sub3)
	if err != nil {
		t.Fatalf("ListAppliedCoupons sub3: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("sub3 got %d applied coupons after an exhausted redeem, want 0", len(applied))
	}

	got, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if got.TimesRedeemed != 2 {
		t.Errorf("got TimesRedeemed %d, want 2", got.TimesRedeemed)
	}
}

// testRedeemCouponUnlimited asserts MaxRedemptions 0 never trips the cap,
// no matter how high TimesRedeemed already is.
func testRedeemCouponUnlimited(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	c.MaxRedemptions = 0
	c.TimesRedeemed = 5
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.RedeemCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("RedeemCoupon: %v", err)
	}

	got, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if got.TimesRedeemed != 6 {
		t.Errorf("got TimesRedeemed %d, want 6", got.TimesRedeemed)
	}
}

// testRedeemCouponNegativeCapIsUnlimited pins the one cap rule every
// backend and the engine share: MaxRedemptions of zero or less means
// unlimited. The SQL and mongo backends used to encode "= 0", which read a
// negative cap as already exhausted while memory and ApplyCoupon read it as
// unlimited.
func testRedeemCouponNegativeCapIsUnlimited(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	c.MaxRedemptions = -1
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	for i := range 3 {
		if err := s.RedeemCoupon(ctx, id.NewSubscriptionID(), c.ID); err != nil {
			t.Fatalf("RedeemCoupon %d with MaxRedemptions -1: %v", i+1, err)
		}
	}

	got, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if got.TimesRedeemed != 3 {
		t.Errorf("got TimesRedeemed %d, want 3", got.TimesRedeemed)
	}
}

// testRedeemCouponDuplicate asserts redeeming the same (subscription,
// coupon) pair twice rejects the second call without moving the count or
// adding a second application row.
func testRedeemCouponDuplicate(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.RedeemCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("first RedeemCoupon: %v", err)
	}
	if err := s.RedeemCoupon(ctx, subID, c.ID); !errors.Is(err, ledger.ErrCouponAlreadyApplied) {
		t.Fatalf("second RedeemCoupon: got %v, want a wrap of ErrCouponAlreadyApplied", err)
	}

	got, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if got.TimesRedeemed != 1 {
		t.Errorf("got TimesRedeemed %d, want 1", got.TimesRedeemed)
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("got %d applied coupons, want 1", len(applied))
	}
}

// testRedeemCouponRejectsBadInput asserts every rejection RedeemCoupon owes
// on bad input - an unknown coupon, a nil subscription id, a subscription
// id that names some other kind of entity - leaves no row and no count
// change behind, exactly like ApplyCoupon's equivalent checks.
func testRedeemCouponRejectsBadInput(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	checkNoRowsNoCountChange := func(t *testing.T, subID id.SubscriptionID) {
		t.Helper()
		if !subID.IsNil() {
			applied, err := s.ListAppliedCoupons(ctx, subID)
			if err != nil {
				t.Fatalf("ListAppliedCoupons: %v", err)
			}
			if len(applied) != 0 {
				t.Errorf("got %d applied coupons after a rejected redeem, want 0", len(applied))
			}
		}
		got, err := s.GetCouponByID(ctx, c.ID)
		if err != nil {
			t.Fatalf("GetCouponByID: %v", err)
		}
		if got.TimesRedeemed != 0 {
			t.Errorf("got TimesRedeemed %d after a rejected redeem, want 0", got.TimesRedeemed)
		}
	}

	t.Run("unknown coupon", func(t *testing.T) {
		subID := id.NewSubscriptionID()
		unknownCoupon := id.NewCouponID()
		if err := s.RedeemCoupon(ctx, subID, unknownCoupon); !errors.Is(err, ledger.ErrCouponNotFound) {
			t.Fatalf("got %v, want a wrap of ErrCouponNotFound", err)
		}
		applied, err := s.ListAppliedCoupons(ctx, subID)
		if err != nil {
			t.Fatalf("ListAppliedCoupons: %v", err)
		}
		if len(applied) != 0 {
			t.Errorf("got %d applied coupons after a rejected redeem, want 0", len(applied))
		}
	})

	t.Run("nil subscription id", func(t *testing.T) {
		if err := s.RedeemCoupon(ctx, id.Nil, c.ID); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Fatalf("got %v, want a wrap of ErrInvalidInput", err)
		}
		checkNoRowsNoCountChange(t, id.Nil)
	})

	t.Run("a plan id as the subscription id", func(t *testing.T) {
		wrongPrefix := id.NewPlanID()
		if err := s.RedeemCoupon(ctx, wrongPrefix, c.ID); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Fatalf("got %v, want a wrap of ErrInvalidInput", err)
		}
		checkNoRowsNoCountChange(t, wrongPrefix)
	})
}

// testRedeemCouponConcurrentCap is the load-bearing proof: a coupon capped
// at 3 redemptions, redeemed onto 12 different subscriptions by 12
// goroutines at once. If the cap were enforced by a read of TimesRedeemed
// followed by a separate write, several goroutines could read the count
// before any of them writes it and all decide they are under the cap; a
// conditional increment inside one unit cannot make that mistake because
// there is no gap between the check and the write for another goroutine to
// land in. Exactly 3 must succeed, exactly 9 must see ErrCouponExhausted,
// the stored count must land on exactly 3, and the 12 subscriptions' lists
// must hold exactly 3 applications between them - not more, and not fewer
// than the 3 that were supposed to win.
func testRedeemCouponConcurrentCap(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	c := newTestCoupon(appID)
	c.MaxRedemptions = 3
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	const n = 12
	subIDs := make([]id.SubscriptionID, n)
	for i := range subIDs {
		subIDs[i] = id.NewSubscriptionID()
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			errs[i] = s.RedeemCoupon(ctx, subIDs[i], c.ID)
		}(i)
	}
	wg.Wait()

	var wins, exhausted int
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ledger.ErrCouponExhausted):
			exhausted++
		default:
			t.Errorf("got an error from a concurrent RedeemCoupon that is neither nil nor ErrCouponExhausted: %v", err)
		}
	}
	if wins != 3 {
		t.Errorf("got %d winning RedeemCoupon calls out of %d, want exactly 3 (exhausted=%d)", wins, n, exhausted)
	}
	if exhausted != n-3 {
		t.Errorf("got %d exhausted RedeemCoupon calls out of %d, want exactly %d", exhausted, n, n-3)
	}

	got, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if got.TimesRedeemed != 3 {
		t.Errorf("got TimesRedeemed %d, want exactly 3", got.TimesRedeemed)
	}

	total := 0
	for _, subID := range subIDs {
		applied, err := s.ListAppliedCoupons(ctx, subID)
		if err != nil {
			t.Fatalf("ListAppliedCoupons: %v", err)
		}
		total += len(applied)
	}
	if total != 3 {
		t.Errorf("got %d applications across all 12 subscriptions, want exactly 3", total)
	}
}

// newTestSubscription builds a subscription fixture. PlanID references a
// freshly minted plan id rather than a plan actually created in the store:
// none of the four backends enforce a foreign key from subscriptions to
// plans, so this is enough to exercise CreateSubscription and the list
// methods without pulling in the plan fixtures too.
func newTestSubscription(tenantID, appID string) *subscription.Subscription {
	now := time.Now().UTC()
	return &subscription.Subscription{
		Entity:             types.NewEntity(),
		ID:                 id.NewSubscriptionID(),
		TenantID:           tenantID,
		PlanID:             id.NewPlanID(),
		Status:             subscription.StatusActive,
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   now.AddDate(0, 1, 0),
		AppID:              appID,
	}
}

// newTestInvoice builds an invoice fixture. SubscriptionID similarly
// references a freshly minted id rather than a real subscription row.
func newTestInvoice(tenantID, appID string) *invoice.Invoice {
	now := time.Now().UTC()
	return &invoice.Invoice{
		Entity:         types.NewEntity(),
		ID:             id.NewInvoiceID(),
		TenantID:       tenantID,
		SubscriptionID: id.NewSubscriptionID(),
		Status:         invoice.StatusDraft,
		Currency:       "usd",
		Subtotal:       types.USD(1000),
		TaxAmount:      types.USD(0),
		DiscountAmount: types.USD(0),
		Total:          types.USD(1000),
		PeriodStart:    now,
		PeriodEnd:      now.AddDate(0, 1, 0),
		AppID:          appID,
	}
}

func newTestUsageEvent(tenantID, appID string) *meter.UsageEvent {
	return &meter.UsageEvent{
		ID:         id.NewUsageEventID(),
		TenantID:   tenantID,
		AppID:      appID,
		FeatureKey: "api_calls",
		Quantity:   1,
		Timestamp:  time.Now().UTC(),
	}
}

func hasSubscriptionID(subs []*subscription.Subscription, want id.SubscriptionID) bool {
	for _, sub := range subs {
		if sub.ID.String() == want.String() {
			return true
		}
	}
	return false
}

func hasInvoiceID(invs []*invoice.Invoice, want id.InvoiceID) bool {
	for _, inv := range invs {
		if inv.ID.String() == want.String() {
			return true
		}
	}
	return false
}

func hasUsageEventID(evts []*meter.UsageEvent, want id.UsageEventID) bool {
	for _, e := range evts {
		if e.ID.String() == want.String() {
			return true
		}
	}
	return false
}

// testSubscriptionTenantIsolation writes a subscription under each of two
// tenants in the same app and asserts a list scoped to one tenant returns
// only that tenant's subscription, identified by id and not merely by count.
func testSubscriptionTenantIsolation(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantA := "tenant-a-" + uniqueSuffix()
	tenantB := "tenant-b-" + uniqueSuffix()

	subA := newTestSubscription(tenantA, appID)
	subB := newTestSubscription(tenantB, appID)
	if err := s.CreateSubscription(ctx, subA); err != nil {
		t.Fatalf("CreateSubscription(tenantA): %v", err)
	}
	if err := s.CreateSubscription(ctx, subB); err != nil {
		t.Fatalf("CreateSubscription(tenantB): %v", err)
	}

	got, err := s.ListSubscriptions(ctx, tenantA, appID, subscription.ListOpts{})
	if err != nil {
		t.Fatalf("ListSubscriptions(tenantA): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListSubscriptions(tenantA): got %d rows, want exactly 1", len(got))
	}
	if got[0].ID.String() != subA.ID.String() {
		t.Errorf("ListSubscriptions(tenantA): got id %s, want %s", got[0].ID, subA.ID)
	}
	if hasSubscriptionID(got, subB.ID) {
		t.Errorf("ListSubscriptions(tenantA) leaked tenantB's subscription %s", subB.ID)
	}
}

// testSubscriptionQuantityRoundTrip pins down that Subscription.Quantity, the
// per-feature seat count, survives every backend's model mapping intact. A
// backend that maps every other field but silently drops this one would
// still pass every other subtest in this suite; only reading it back here
// catches that.
//
// It also pins the nil case: a subscription created without a Quantity must
// read back an empty, non-nil map, never nil, so a caller can index it
// without a guard. And it pins UpdateSubscription as a full replace: seats
// change, projects disappears entirely, not just gets zeroed.
func testSubscriptionQuantityRoundTrip(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()

	sub := newTestSubscription(tenantID, appID)
	sub.Quantity = map[string]int64{"seats": 12, "projects": 3}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	got, err := s.GetSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if want := (map[string]int64{"seats": 12, "projects": 3}); !reflect.DeepEqual(got.Quantity, want) {
		t.Errorf("Quantity after create: got %v, want %v", got.Quantity, want)
	}

	noQtySub := newTestSubscription(tenantID, appID)
	if err := s.CreateSubscription(ctx, noQtySub); err != nil {
		t.Fatalf("CreateSubscription(nil Quantity): %v", err)
	}
	gotNoQty, err := s.GetSubscription(ctx, noQtySub.ID)
	if err != nil {
		t.Fatalf("GetSubscription(nil Quantity): %v", err)
	}
	if gotNoQty.Quantity == nil {
		t.Error("Quantity for a subscription created with none came back nil, want an empty non-nil map")
	}
	if len(gotNoQty.Quantity) != 0 {
		t.Errorf("Quantity for a subscription created with none: got %v, want empty", gotNoQty.Quantity)
	}

	got.Quantity = map[string]int64{"seats": 20}
	if err := s.UpdateSubscription(ctx, got); err != nil {
		t.Fatalf("UpdateSubscription: %v", err)
	}
	gotAfterUpdate, err := s.GetSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetSubscription after update: %v", err)
	}
	if want := (map[string]int64{"seats": 20}); !reflect.DeepEqual(gotAfterUpdate.Quantity, want) {
		t.Errorf("Quantity after update: got %v, want %v", gotAfterUpdate.Quantity, want)
	}
}

// testInvoiceTenantIsolation is the invoice analogue of
// testSubscriptionTenantIsolation.
func testInvoiceTenantIsolation(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantA := "tenant-a-" + uniqueSuffix()
	tenantB := "tenant-b-" + uniqueSuffix()

	invA := newTestInvoice(tenantA, appID)
	invB := newTestInvoice(tenantB, appID)
	if err := s.CreateInvoice(ctx, invA); err != nil {
		t.Fatalf("CreateInvoice(tenantA): %v", err)
	}
	if err := s.CreateInvoice(ctx, invB); err != nil {
		t.Fatalf("CreateInvoice(tenantB): %v", err)
	}

	got, err := s.ListInvoices(ctx, tenantA, appID, invoice.ListOpts{})
	if err != nil {
		t.Fatalf("ListInvoices(tenantA): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListInvoices(tenantA): got %d rows, want exactly 1", len(got))
	}
	if got[0].ID.String() != invA.ID.String() {
		t.Errorf("ListInvoices(tenantA): got id %s, want %s", got[0].ID, invA.ID)
	}
	if hasInvoiceID(got, invB.ID) {
		t.Errorf("ListInvoices(tenantA) leaked tenantB's invoice %s", invB.ID)
	}
}

// testIngestKeylessEventsAreAllCounted pins down that usage events with no
// idempotency key are never treated as duplicates of one another. A store
// backend that enforces uniqueness on the literal (possibly empty) stored
// key value, rather than only on genuinely repeated non-empty keys, would
// silently drop every keyless event past the first - under-billing metered
// usage with no error raised anywhere.
func testIngestKeylessEventsAreAllCounted(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	featureKey := "keyless-" + uniqueSuffix()

	events := []*meter.UsageEvent{
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 1, Timestamp: time.Now().UTC()},
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 2, Timestamp: time.Now().UTC()},
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 4, Timestamp: time.Now().UTC()},
	}
	if err := s.IngestBatch(ctx, events); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{FeatureKey: featureKey})
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("QueryUsage: got %d event(s), want exactly 3 (every keyless event must be counted, not dropped as a false duplicate)", len(got))
	}

	total, err := s.Aggregate(ctx, tenantID, appID, featureKey, plan.PeriodMonthly)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if total != 7 {
		t.Errorf("Aggregate: got %d, want 7 (1+2+4)", total)
	}
}

// testIngestDuplicateKeyIsCountedOnce pins down the other half of the
// contract: a genuinely repeated non-empty idempotency key must still
// collapse to a single counted event, exactly as it did before keyless
// events were fixed to stop colliding with each other.
func testIngestDuplicateKeyIsCountedOnce(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	featureKey := "dupkey-" + uniqueSuffix()
	key := "idem-" + uniqueSuffix()

	events := []*meter.UsageEvent{
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 3, Timestamp: time.Now().UTC(), IdempotencyKey: key},
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 3, Timestamp: time.Now().UTC(), IdempotencyKey: key},
	}
	if err := s.IngestBatch(ctx, events); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{FeatureKey: featureKey})
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("QueryUsage: got %d event(s), want exactly 1 (a repeated idempotency key must collapse to one)", len(got))
	}

	total, err := s.Aggregate(ctx, tenantID, appID, featureKey, plan.PeriodMonthly)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if total != 3 {
		t.Errorf("Aggregate: got %d, want 3 (counted once, not twice)", total)
	}
}

// testIngestKeyedAndKeylessMix combines both shapes in one batch: keyless
// events must all survive alongside events carrying distinct real keys.
func testIngestKeyedAndKeylessMix(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	featureKey := "mixed-" + uniqueSuffix()

	events := []*meter.UsageEvent{
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 1, Timestamp: time.Now().UTC()},
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 1, Timestamp: time.Now().UTC()},
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 1, Timestamp: time.Now().UTC(), IdempotencyKey: "mix-a-" + uniqueSuffix()},
		{ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID, FeatureKey: featureKey, Quantity: 1, Timestamp: time.Now().UTC(), IdempotencyKey: "mix-b-" + uniqueSuffix()},
	}
	if err := s.IngestBatch(ctx, events); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{FeatureKey: featureKey})
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("QueryUsage: got %d event(s), want exactly 4 (2 keyless + 2 distinctly keyed, all counted)", len(got))
	}

	total, err := s.Aggregate(ctx, tenantID, appID, featureKey, plan.PeriodMonthly)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if total != 4 {
		t.Errorf("Aggregate: got %d, want 4", total)
	}
}

// testUsageTenantIsolation is the usage-event analogue of
// testSubscriptionTenantIsolation.
func testUsageTenantIsolation(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantA := "tenant-a-" + uniqueSuffix()
	tenantB := "tenant-b-" + uniqueSuffix()

	evtA := newTestUsageEvent(tenantA, appID)
	evtB := newTestUsageEvent(tenantB, appID)
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{evtA, evtB}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := s.QueryUsage(ctx, tenantA, appID, meter.QueryOpts{})
	if err != nil {
		t.Fatalf("QueryUsage(tenantA): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("QueryUsage(tenantA): got %d rows, want exactly 1", len(got))
	}
	if got[0].ID.String() != evtA.ID.String() {
		t.Errorf("QueryUsage(tenantA): got id %s, want %s", got[0].ID, evtA.ID)
	}
	if hasUsageEventID(got, evtB.ID) {
		t.Errorf("QueryUsage(tenantA) leaked tenantB's usage event %s", evtB.ID)
	}
}

// testEmptyTenantIDBehavior pins down, rather than guesses at, what an empty
// tenantID does on ListSubscriptions, ListInvoices, and QueryUsage today.
//
// PINNED BEHAVIOUR: every store.Store implementation in this repository
// (memory, sqlite, postgres, mongo) treats tenantID == "" as "do not filter
// by tenant" while the appID filter still applies normally. A caller that
// fails to resolve a tenant and passes "" straight through gets every
// tenant's rows back for the given app: not zero rows, but also not every
// row in the table regardless of app. That is a real, present-day gap: a
// contract layer sitting in front of these stores must refuse to call them
// with an empty tenant id, because the stores themselves will not stop it.
// This test exists so that guard has something concrete to defend, and so
// this behaviour cannot silently change in either direction without a test
// failing here first. If a future backend disagreed with the others, that
// would be a finding to report, not a difference to average away.
//
// A third fixture row lives under a different appID entirely. It is the
// control: presence-only assertions ("does the result contain tenant A and
// tenant B") would also be satisfied by a strictly worse, undocumented
// behaviour where the appID filter is dropped too and every app's rows come
// back. The control row plus an exact count on top of the two presence
// checks is what tells those two behaviours apart.
func testEmptyTenantIDBehavior(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	otherAppID := "app-other-" + uniqueSuffix()
	tenantA := "tenant-a-" + uniqueSuffix()
	tenantB := "tenant-b-" + uniqueSuffix()
	tenantOther := "tenant-other-" + uniqueSuffix()

	subA := newTestSubscription(tenantA, appID)
	subB := newTestSubscription(tenantB, appID)
	subOther := newTestSubscription(tenantOther, otherAppID)
	if err := s.CreateSubscription(ctx, subA); err != nil {
		t.Fatalf("CreateSubscription(tenantA): %v", err)
	}
	if err := s.CreateSubscription(ctx, subB); err != nil {
		t.Fatalf("CreateSubscription(tenantB): %v", err)
	}
	if err := s.CreateSubscription(ctx, subOther); err != nil {
		t.Fatalf("CreateSubscription(otherApp): %v", err)
	}

	invA := newTestInvoice(tenantA, appID)
	invB := newTestInvoice(tenantB, appID)
	invOther := newTestInvoice(tenantOther, otherAppID)
	if err := s.CreateInvoice(ctx, invA); err != nil {
		t.Fatalf("CreateInvoice(tenantA): %v", err)
	}
	if err := s.CreateInvoice(ctx, invB); err != nil {
		t.Fatalf("CreateInvoice(tenantB): %v", err)
	}
	if err := s.CreateInvoice(ctx, invOther); err != nil {
		t.Fatalf("CreateInvoice(otherApp): %v", err)
	}

	evtA := newTestUsageEvent(tenantA, appID)
	evtB := newTestUsageEvent(tenantB, appID)
	evtOther := newTestUsageEvent(tenantOther, otherAppID)
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{evtA, evtB, evtOther}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	subs, err := s.ListSubscriptions(ctx, "", appID, subscription.ListOpts{})
	if err != nil {
		t.Fatalf("ListSubscriptions(empty tenant): %v", err)
	}
	if !hasSubscriptionID(subs, subA.ID) || !hasSubscriptionID(subs, subB.ID) {
		t.Errorf("ListSubscriptions(tenantID=%q, appID=%s): got %d row(s) not covering both tenants; "+
			"an empty tenant id is expected, today, to match every tenant under the app", "", appID, len(subs))
	}
	if hasSubscriptionID(subs, subOther.ID) {
		t.Errorf("ListSubscriptions(tenantID=%q, appID=%s): got otherApp's subscription %s; "+
			"an empty tenant id must not also drop the appID filter", "", appID, subOther.ID)
	}
	if len(subs) != 2 {
		t.Errorf("ListSubscriptions(tenantID=%q, appID=%s): got %d row(s), want exactly 2 (tenantA + tenantB)", "", appID, len(subs))
	}

	invs, err := s.ListInvoices(ctx, "", appID, invoice.ListOpts{})
	if err != nil {
		t.Fatalf("ListInvoices(empty tenant): %v", err)
	}
	if !hasInvoiceID(invs, invA.ID) || !hasInvoiceID(invs, invB.ID) {
		t.Errorf("ListInvoices(tenantID=%q, appID=%s): got %d row(s) not covering both tenants; "+
			"an empty tenant id is expected, today, to match every tenant under the app", "", appID, len(invs))
	}
	if hasInvoiceID(invs, invOther.ID) {
		t.Errorf("ListInvoices(tenantID=%q, appID=%s): got otherApp's invoice %s; "+
			"an empty tenant id must not also drop the appID filter", "", appID, invOther.ID)
	}
	if len(invs) != 2 {
		t.Errorf("ListInvoices(tenantID=%q, appID=%s): got %d row(s), want exactly 2 (tenantA + tenantB)", "", appID, len(invs))
	}

	evts, err := s.QueryUsage(ctx, "", appID, meter.QueryOpts{})
	if err != nil {
		t.Fatalf("QueryUsage(empty tenant): %v", err)
	}
	if !hasUsageEventID(evts, evtA.ID) || !hasUsageEventID(evts, evtB.ID) {
		t.Errorf("QueryUsage(tenantID=%q, appID=%s): got %d row(s) not covering both tenants; "+
			"an empty tenant id is expected, today, to match every tenant under the app", "", appID, len(evts))
	}
	if hasUsageEventID(evts, evtOther.ID) {
		t.Errorf("QueryUsage(tenantID=%q, appID=%s): got otherApp's usage event %s; "+
			"an empty tenant id must not also drop the appID filter", "", appID, evtOther.ID)
	}
	if len(evts) != 2 {
		t.Errorf("QueryUsage(tenantID=%q, appID=%s): got %d row(s), want exactly 2 (tenantA + tenantB)", "", appID, len(evts))
	}
}

// testQueryUsageWindowIsHalfOpen pins down the usage window as [Start, End):
// an event stamped exactly at Start belongs to the window, and one stamped
// exactly at End belongs to the next. Billing periods abut, so a window that
// is closed at both ends bills a boundary event in two consecutive invoices,
// and one that is open at both bills it in neither.
//
// Timestamps are second-aligned UTC because MongoDB stores milliseconds and
// SQLite stores text: a sub-second value would turn a boundary comparison
// into a precision test, which is not what this pins down.
func testQueryUsageWindowIsHalfOpen(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	featureKey := "window-" + uniqueSuffix()

	start := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	at := func(ts time.Time) *meter.UsageEvent {
		return &meter.UsageEvent{
			ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID,
			FeatureKey: featureKey, Quantity: 1, Timestamp: ts,
		}
	}
	beforeStart := at(start.Add(-time.Second))
	atStart := at(start)
	afterStart := at(start.Add(time.Second))
	beforeEnd := at(end.Add(-time.Second))
	atEnd := at(end)

	if err := s.IngestBatch(ctx, []*meter.UsageEvent{beforeStart, atStart, afterStart, beforeEnd, atEnd}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{FeatureKey: featureKey, Start: start, End: end})
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}

	for _, want := range []struct {
		name string
		evt  *meter.UsageEvent
	}{
		{"the event exactly at Start", atStart},
		{"the event a second after Start", afterStart},
		{"the event a second before End", beforeEnd},
	} {
		if !hasUsageEventID(got, want.evt.ID) {
			t.Errorf("QueryUsage[Start, End): missing %s; the window is closed at Start", want.name)
		}
	}
	if hasUsageEventID(got, beforeStart.ID) {
		t.Errorf("QueryUsage[Start, End): returned the event a second before Start")
	}
	if hasUsageEventID(got, atEnd.ID) {
		t.Errorf("QueryUsage[Start, End): returned the event exactly at End; the window is open at End, "+
			"and that event belongs to the next billing period (got %d events)", len(got))
	}
	if len(got) != 3 {
		t.Errorf("QueryUsage[Start, End): got %d events, want exactly 3", len(got))
	}

	// The bounds are instants, not strings: the same window written in
	// another zone selects the same events.
	zone := time.FixedZone("UTC+5", 5*60*60)
	shifted, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{
		FeatureKey: featureKey, Start: start.In(zone), End: end.In(zone),
	})
	if err != nil {
		t.Fatalf("QueryUsage with bounds in another zone: %v", err)
	}
	if len(shifted) != 3 || !hasUsageEventID(shifted, atStart.ID) || !hasUsageEventID(shifted, afterStart.ID) ||
		!hasUsageEventID(shifted, beforeEnd.ID) {
		t.Errorf("QueryUsage with bounds in another zone: got %d events, want the same three as the UTC window", len(shifted))
	}
}

// nowWithMonotonic returns time.Now() untouched. That is what Ledger's own
// write paths (Ledger.Meter, Ledger.CreateSubscription) hand to a store, and
// it carries a monotonic clock reading on any host, whatever its zone: the
// default string form ends in "m=+...". The tests below must not call .UTC()
// or .Round(0) on it, since stripping the reading is exactly what a store has
// to do for itself. (time.Time.In drops the reading, so a value moved into
// another zone with it is a different fixture; see
// testUsageEventNearABoundaryInALocalZone.)
func nowWithMonotonic(t *testing.T) time.Time {
	t.Helper()

	got := time.Now()
	if !strings.Contains(got.String(), "m=+") {
		t.Fatalf("time.Now() has no monotonic reading: %q", got.String())
	}
	return got
}

// sameInstantToMillisecond reports whether a and b are the same instant once
// both are truncated to the millisecond, the precision MongoDB stores.
func sameInstantToMillisecond(a, b time.Time) bool {
	return a.Truncate(time.Millisecond).Equal(b.Truncate(time.Millisecond))
}

// testSubscriptionPeriodsRoundTripFromTimeNow stores a subscription whose
// period bounds and CancelAt come straight from time.Now(), monotonic reading
// intact, and reads it back. SQLite once wrote such a value as
// "2026-09-29 15:43:04.194044 -0500 CDT m=+0.017196459" (or "+0000 UTC m=+..."
// on a UTC host) and could not scan it again, so every subscription made
// through Ledger.CreateSubscription was unreadable there.
func testSubscriptionPeriodsRoundTripFromTimeNow(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()

	start := nowWithMonotonic(t)
	end := start.AddDate(0, 1, 0)
	cancelAt := nowWithMonotonic(t).Add(48 * time.Hour)

	sub := newTestSubscription(tenantID, appID)
	sub.CurrentPeriodStart = start
	sub.CurrentPeriodEnd = end
	sub.CancelAt = &cancelAt
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	got, err := s.GetSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}

	if got.CurrentPeriodStart.IsZero() || !sameInstantToMillisecond(got.CurrentPeriodStart, start) {
		t.Errorf("CurrentPeriodStart: got %v, want the instant %v", got.CurrentPeriodStart, start)
	}
	if got.CurrentPeriodEnd.IsZero() || !sameInstantToMillisecond(got.CurrentPeriodEnd, end) {
		t.Errorf("CurrentPeriodEnd: got %v, want the instant %v", got.CurrentPeriodEnd, end)
	}
	if got.CancelAt == nil || got.CancelAt.IsZero() {
		t.Fatalf("CancelAt: got %v, want the instant %v", got.CancelAt, cancelAt)
	}
	if !sameInstantToMillisecond(*got.CancelAt, cancelAt) {
		t.Errorf("CancelAt: got %v, want the instant %v", *got.CancelAt, cancelAt)
	}
}

// testUsageEventRoundTripsFromTimeNow ingests an event stamped straight from
// time.Now(), monotonic reading intact, and queries it back through a window
// written in UTC. SQLite once stored such an event in a form QueryUsage could
// not scan.
func testUsageEventRoundTripsFromTimeNow(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	featureKey := "local-" + uniqueSuffix()

	stamp := nowWithMonotonic(t)
	evt := &meter.UsageEvent{
		ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID,
		FeatureKey: featureKey, Quantity: 1, Timestamp: stamp,
	}
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{evt}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{
		FeatureKey: featureKey,
		Start:      stamp.Add(-time.Hour).UTC(),
		End:        stamp.Add(time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	if len(got) != 1 || got[0].ID.String() != evt.ID.String() {
		t.Fatalf("QueryUsage: got %d events, want exactly the ingested event %s", len(got), evt.ID)
	}
	if got[0].Timestamp.IsZero() || !sameInstantToMillisecond(got[0].Timestamp, stamp) {
		t.Errorf("Timestamp: got %v, want the instant %v", got[0].Timestamp, stamp)
	}
}

// testUsageEventNearABoundaryInALocalZone is the text-comparison case. SQLite
// compares timestamps as text, so a row stored in UTC-5 sorts by its local
// wall clock: an event 30 minutes into a UTC window has a local wall clock of
// the previous evening and falls outside a text comparison against UTC
// bounds, and is silently dropped when it is the only row.
func testUsageEventNearABoundaryInALocalZone(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	featureKey := "boundary-" + uniqueSuffix()

	start := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	local := time.FixedZone("CDT", -5*3600)
	evt := &meter.UsageEvent{
		ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID,
		FeatureKey: featureKey, Quantity: 1, Timestamp: start.Add(30 * time.Minute).In(local),
	}
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{evt}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{FeatureKey: featureKey, Start: start, End: end})
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	if len(got) != 1 || got[0].ID.String() != evt.ID.String() {
		t.Errorf("QueryUsage[Start, End): got %d events, want the event stamped 30 minutes after Start "+
			"(its UTC-5 wall clock is the previous evening, and a store that compares text drops it)", len(got))
	}
}

// testListInvoicesBoundsAreInstants lists invoices with bounds written in a
// zone other than the one the periods were stored in. The bounds are
// instants: [2026-03-01, 2026-04-01) UTC selects the same invoice whether it
// is spelled in UTC or in UTC-5. SQLite compares timestamps as text, so bounds
// bound in the caller's zone once matched nothing. The neighbouring period
// must stay out, which also pins that every backend honours Start and End.
func testListInvoicesBoundsAreInstants(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()

	start := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	inPeriod := newTestInvoice(tenantID, appID)
	inPeriod.PeriodStart, inPeriod.PeriodEnd = start, end
	nextPeriod := newTestInvoice(tenantID, appID)
	nextPeriod.PeriodStart, nextPeriod.PeriodEnd = end, end.AddDate(0, 1, 0)
	for _, inv := range []*invoice.Invoice{inPeriod, nextPeriod} {
		if err := s.CreateInvoice(ctx, inv); err != nil {
			t.Fatalf("CreateInvoice: %v", err)
		}
	}

	for name, zone := range map[string]*time.Location{"UTC": time.UTC, "UTC-5": time.FixedZone("CDT", -5*3600)} {
		got, err := s.ListInvoices(ctx, tenantID, appID, invoice.ListOpts{Start: start.In(zone), End: end.In(zone)})
		if err != nil {
			t.Fatalf("ListInvoices with bounds in %s: %v", name, err)
		}
		if len(got) != 1 || got[0].ID.String() != inPeriod.ID.String() {
			t.Errorf("ListInvoices with bounds in %s: got %d invoices, want exactly the invoice for [Start, End)", name, len(got))
		}
	}
}

// testListsPageInAStableOrder pins down that every list method pages
// consistently: fetched two rows at a time, the pages neither overlap nor
// skip, and together they read exactly as the unpaged listing does. It does
// not assert a sort direction, because the backends' existing orders stand
// (plans and catalog features oldest first, the rest newest first). What a
// caller needs is that offset paging is stable, and that is what the in-memory
// test double got wrong: it paged over Go's randomised map order, or ignored
// Limit and Offset altogether.
//
// The global feature list belongs to no app, so it is checked by
// testSharedListPages, which cannot assume the list holds only this test's rows.
//
// Rows are stamped a second apart so no two share a created_at, which keeps
// the assertion about paging rather than about tie-breaking.
func testListsPageInAStableOrder(t *testing.T, s ledgerstore.Store) {
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	testListsPage(t, s, func(i int) time.Time { return base.Add(time.Duration(i) * time.Second) }, false)
}

// testListsPageStablyOnEqualTimestamps stamps every row with one instant, as
// a batch ingest does, so the sort key alone cannot order them. A list that
// sorts by the timestamp only leaves the database free to return tied rows in
// a different order on each query, and a row then repeats on one page or
// vanishes between two. The id is the tie-break that keeps them in place.
func testListsPageStablyOnEqualTimestamps(t *testing.T, s ledgerstore.Store) {
	instant := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	testListsPage(t, s, func(int) time.Time { return instant }, true)
}

// testListsPage seeds five rows into every paged list, stamping row i with
// at(i), then pages each list two rows at a time. tied says every row shares
// one timestamp, in which case the rows come back in id order, not creation
// order.
func testListsPage(t *testing.T, s ledgerstore.Store, at func(i int) time.Time, tied bool) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()

	const rows = 5
	var globalIDs []string
	for i := 0; i < rows; i++ {
		p := &plan.Plan{
			Entity: types.Entity{CreatedAt: at(i), UpdatedAt: at(i)}, ID: id.NewPlanID(),
			Name: "Plan", Slug: "plan-" + uniqueSuffix(), Currency: "usd",
			Status: plan.StatusActive, AppID: appID,
		}
		if err := s.CreatePlan(ctx, p); err != nil {
			t.Fatalf("CreatePlan: %v", err)
		}

		sub := newTestSubscription(tenantID, appID)
		sub.CreatedAt, sub.UpdatedAt = at(i), at(i)
		if err := s.CreateSubscription(ctx, sub); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}

		inv := newTestInvoice(tenantID, appID)
		inv.CreatedAt, inv.UpdatedAt = at(i), at(i)
		if err := s.CreateInvoice(ctx, inv); err != nil {
			t.Fatalf("CreateInvoice: %v", err)
		}

		c := newTestCoupon(appID)
		c.CreatedAt, c.UpdatedAt = at(i), at(i)
		if err := s.CreateCoupon(ctx, c); err != nil {
			t.Fatalf("CreateCoupon: %v", err)
		}

		e := newTestUsageEvent(tenantID, appID)
		e.Timestamp = at(i)
		if err := s.IngestBatch(ctx, []*meter.UsageEvent{e}); err != nil {
			t.Fatalf("IngestBatch: %v", err)
		}

		f := newTestFeature(appID)
		f.CreatedAt, f.UpdatedAt = at(i), at(i)
		if err := s.CreateFeature(ctx, f); err != nil {
			t.Fatalf("CreateFeature: %v", err)
		}

		g := newTestFeature("")
		g.CreatedAt, g.UpdatedAt = at(i), at(i)
		if err := s.CreateFeature(ctx, g); err != nil {
			t.Fatalf("CreateFeature (global): %v", err)
		}
		globalIDs = append(globalIDs, g.ID.String())
	}

	lists := []struct {
		name string
		// shared marks a list that cannot be isolated to this test's rows:
		// the global catalog belongs to no app, so a store that outlives the
		// run (Postgres) holds rows from earlier runs as well.
		shared bool
		list   func(limit, offset int) ([]string, error)
	}{
		{"ListPlans", false, func(limit, offset int) ([]string, error) {
			got, err := s.ListPlans(ctx, appID, plan.ListOpts{Limit: limit, Offset: offset})
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID.String()
			}
			return ids, err
		}},
		{"ListSubscriptions", false, func(limit, offset int) ([]string, error) {
			got, err := s.ListSubscriptions(ctx, tenantID, appID, subscription.ListOpts{Limit: limit, Offset: offset})
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID.String()
			}
			return ids, err
		}},
		{"ListInvoices", false, func(limit, offset int) ([]string, error) {
			got, err := s.ListInvoices(ctx, tenantID, appID, invoice.ListOpts{Limit: limit, Offset: offset})
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID.String()
			}
			return ids, err
		}},
		{"ListCoupons", false, func(limit, offset int) ([]string, error) {
			got, err := s.ListCoupons(ctx, appID, coupon.ListOpts{Limit: limit, Offset: offset})
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID.String()
			}
			return ids, err
		}},
		{"QueryUsage", false, func(limit, offset int) ([]string, error) {
			got, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{Limit: limit, Offset: offset})
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID.String()
			}
			return ids, err
		}},
		{"ListFeatures", false, func(limit, offset int) ([]string, error) {
			got, err := s.ListFeatures(ctx, appID, feature.ListOpts{Limit: limit, Offset: offset})
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID.String()
			}
			return ids, err
		}},
		{"ListGlobalFeatures", true, func(limit, offset int) ([]string, error) {
			got, err := s.ListGlobalFeatures(ctx, feature.ListOpts{Limit: limit, Offset: offset})
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID.String()
			}
			return ids, err
		}},
	}

	for _, l := range lists {
		if l.shared {
			t.Run(l.name, func(t *testing.T) {
				if tied {
					// Oldest first with ties broken by id, ascending.
					sort.Strings(globalIDs)
				}
				testSharedListPages(t, l.list, globalIDs)
			})
			continue
		}
		t.Run(l.name, func(t *testing.T) {
			all, err := l.list(0, 0)
			if err != nil {
				t.Fatalf("unpaged: %v", err)
			}
			if len(all) != rows {
				t.Fatalf("unpaged: got %d rows, want %d", len(all), rows)
			}

			var joined []string
			seen := map[string]int{}
			for i, wantSize := range []int{2, 2, 1} {
				page, err := l.list(2, i*2)
				if err != nil {
					t.Fatalf("page at offset %d: %v", i*2, err)
				}
				if len(page) != wantSize {
					t.Errorf("page at offset %d: got %d rows, want %d", i*2, len(page), wantSize)
				}
				for _, rid := range page {
					seen[rid]++
					joined = append(joined, rid)
				}
			}
			for _, rid := range all {
				if seen[rid] != 1 {
					t.Errorf("row %s appears on %d pages, want exactly 1", rid, seen[rid])
				}
			}
			if len(seen) != rows {
				t.Errorf("pages hold %d distinct rows, want %d", len(seen), rows)
			}
			if !reflect.DeepEqual(joined, all) {
				t.Errorf("pages read %v, but the unpaged list reads %v", joined, all)
			}

			past, err := l.list(2, rows)
			if err != nil {
				t.Fatalf("page past the end: %v", err)
			}
			if len(past) != 0 {
				t.Errorf("page past the end: got %d rows, want 0", len(past))
			}
		})
	}
}

// testSharedListPages is the paging check for a list that other rows share, so
// its length is not known. It pages through the whole list two rows at a time
// and asserts the same three things: the pages neither overlap nor skip, they
// read exactly as the unpaged list does, and a page past the end is empty.
// It also asserts the rows this test created (mine, oldest first) come back
// in that order, which is the order the backends promise.
func testSharedListPages(t *testing.T, list func(limit, offset int) ([]string, error), mine []string) {
	t.Helper()

	all, err := list(0, 0)
	if err != nil {
		t.Fatalf("unpaged: %v", err)
	}

	next := 0
	for _, rid := range all {
		if next < len(mine) && rid == mine[next] {
			next++
		}
	}
	if next != len(mine) {
		t.Errorf("the unpaged list holds %d of the %d rows created, in creation order; want all of them", next, len(mine))
	}

	var joined []string
	seen := map[string]int{}
	for offset := 0; offset <= len(all); offset += 2 {
		page, err := list(2, offset)
		if err != nil {
			t.Fatalf("page at offset %d: %v", offset, err)
		}
		want := 2
		if len(all)-offset < want {
			want = len(all) - offset
		}
		if len(page) != want {
			t.Errorf("page at offset %d: got %d rows, want %d", offset, len(page), want)
		}
		for _, rid := range page {
			seen[rid]++
			joined = append(joined, rid)
		}
	}
	for _, rid := range all {
		if seen[rid] != 1 {
			t.Errorf("row %s appears on %d pages, want exactly 1", rid, seen[rid])
		}
	}
	if !reflect.DeepEqual(joined, all) {
		t.Errorf("pages read %v, but the unpaged list reads %v", joined, all)
	}

	past, err := list(2, len(all))
	if err != nil {
		t.Fatalf("page past the end: %v", err)
	}
	if len(past) != 0 {
		t.Errorf("page past the end: got %d rows, want 0", len(past))
	}
}

// testCancelSubscriptionAtNowEndsIt pins the boundary of an immediate cancel.
// Ledger.CancelSubscription passes cancelAt = time.Now() for one, and a store
// must treat an instant that has already been reached as reached: the
// subscription is canceled at once, not left active until some later read.
// Every backend once asked time.Now().After(cancelAt), which is false when
// both readings land on the same instant.
func testCancelSubscriptionAtNowEndsIt(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()

	sub := newTestSubscription(tenantID, appID)
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err := s.CancelSubscription(ctx, sub.ID, time.Now()); err != nil {
		t.Fatalf("CancelSubscription: %v", err)
	}

	got, err := s.GetSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got.Status != subscription.StatusCanceled {
		t.Errorf("status after a cancel at now: got %q, want %q", got.Status, subscription.StatusCanceled)
	}
	if got.CancelAt == nil {
		t.Error("cancel_at was not stored")
	}
	if got.CanceledAt == nil {
		t.Error("canceled_at was not stored")
	}

	// A cancel dated in the future only schedules the end: the subscription
	// stays active until then.
	later := newTestSubscription(tenantID, appID)
	if err = s.CreateSubscription(ctx, later); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err = s.CancelSubscription(ctx, later.ID, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("CancelSubscription (future): %v", err)
	}
	gotLater, err := s.GetSubscription(ctx, later.ID)
	if err != nil {
		t.Fatalf("GetSubscription (future): %v", err)
	}
	if gotLater.Status != subscription.StatusActive || gotLater.CancelAt == nil {
		t.Errorf("a future cancel: status %q cancel_at %v, want active with cancel_at set", gotLater.Status, gotLater.CancelAt)
	}
}

// The four subtests below pin the updates that set a status plus timestamps
// in one statement: ArchivePlan, ArchiveFeature, MarkInvoicePaid and
// MarkInvoiceVoided. Each reads the row back and checks every column the
// update writes. On postgres they are the only guard on the argument order of
// those statements: CancelSubscription once numbered its placeholders by hand
// in an order the builder does not bind in, and every immediate cancel failed.
//
// Each fixture is backdated an hour before it is written, so an updated_at
// that the update forgot to set cannot pass for one it did.

// backdate moves an entity's timestamps an hour into the past, truncated to
// the second so every backend stores them without rounding.
func backdate(e *types.Entity) {
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	e.CreatedAt = past
	e.UpdatedAt = past
}

// requireStampedDuring fails unless got falls between start and end, allowing
// a second either side for a backend that stores less than nanosecond
// precision.
func requireStampedDuring(t *testing.T, column string, got, start, end time.Time) {
	t.Helper()
	if got.Before(start.Add(-time.Second)) || got.After(end.Add(time.Second)) {
		t.Errorf("%s: got %v, want a time between %v and %v", column, got, start, end)
	}
}

func testArchivePlanStoresTheArchive(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(),
		Name: "Pro", Slug: "pro-archive-" + uniqueSuffix(), Currency: "usd",
		Status: plan.StatusActive, AppID: appID,
		Pricing: &plan.Pricing{
			ID: id.NewPriceID(), BaseAmount: types.USD(4900),
			BillingPeriod: plan.PeriodMonthly,
		},
	}
	backdate(&p.Entity)
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	start := time.Now()
	if err := s.ArchivePlan(ctx, p.ID); err != nil {
		t.Fatalf("ArchivePlan: %v", err)
	}
	end := time.Now()

	got, err := s.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Status != plan.StatusArchived {
		t.Errorf("status: got %q, want %q", got.Status, plan.StatusArchived)
	}
	requireStampedDuring(t, "updated_at", got.UpdatedAt, start, end)

	if err = s.ArchivePlan(ctx, id.NewPlanID()); !errors.Is(err, ledger.ErrPlanNotFound) {
		t.Errorf("ArchivePlan on an unknown plan: got %v, want ErrPlanNotFound", err)
	}
}

func testArchiveFeatureStoresTheArchive(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()

	f := newTestFeature(appID)
	backdate(&f.Entity)
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatalf("CreateFeature: %v", err)
	}

	start := time.Now()
	if err := s.ArchiveFeature(ctx, f.ID); err != nil {
		t.Fatalf("ArchiveFeature: %v", err)
	}
	end := time.Now()

	got, err := s.GetFeature(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFeature: %v", err)
	}
	if got.Status != feature.StatusArchived {
		t.Errorf("status: got %q, want %q", got.Status, feature.StatusArchived)
	}
	requireStampedDuring(t, "updated_at", got.UpdatedAt, start, end)

	if err = s.ArchiveFeature(ctx, id.NewFeatureID()); !errors.Is(err, ledger.ErrFeatureNotFound) {
		t.Errorf("ArchiveFeature on an unknown feature: got %v, want ErrFeatureNotFound", err)
	}
}

// testMarkInvoicePaidStoresThePayment passes paidAt in a non-UTC zone, dated
// well before the call, so a store that stamped its own clock instead of the
// caller's instant, or shifted the instant by the zone offset, would fail.
func testMarkInvoicePaidStoresThePayment(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()

	inv := newTestInvoice(tenantID, appID)
	inv.Status = invoice.StatusPending
	backdate(&inv.Entity)
	if err := s.CreateInvoice(ctx, inv); err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}

	paidAt := time.Now().Add(-30 * time.Minute).Truncate(time.Second).In(time.FixedZone("CDT", -5*3600))
	paymentRef := "pi_" + uniqueSuffix()

	start := time.Now()
	if err := s.MarkInvoicePaid(ctx, inv.ID, paidAt, paymentRef); err != nil {
		t.Fatalf("MarkInvoicePaid: %v", err)
	}
	end := time.Now()

	got, err := s.GetInvoice(ctx, inv.ID)
	if err != nil {
		t.Fatalf("GetInvoice: %v", err)
	}
	if got.Status != invoice.StatusPaid {
		t.Errorf("status: got %q, want %q", got.Status, invoice.StatusPaid)
	}
	if got.PaidAt == nil {
		t.Error("paid_at was not stored")
	} else if !got.PaidAt.Equal(paidAt) {
		t.Errorf("paid_at: got %v, want %v", got.PaidAt, paidAt)
	}
	if got.PaymentRef != paymentRef {
		t.Errorf("payment_ref: got %q, want %q", got.PaymentRef, paymentRef)
	}
	requireStampedDuring(t, "updated_at", got.UpdatedAt, start, end)

	if err = s.MarkInvoicePaid(ctx, id.NewInvoiceID(), paidAt, paymentRef); !errors.Is(err, ledger.ErrInvoiceNotFound) {
		t.Errorf("MarkInvoicePaid on an unknown invoice: got %v, want ErrInvoiceNotFound", err)
	}
}

func testMarkInvoiceVoidedStoresTheVoid(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()

	inv := newTestInvoice(tenantID, appID)
	inv.Status = invoice.StatusPending
	backdate(&inv.Entity)
	if err := s.CreateInvoice(ctx, inv); err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}

	reason := "duplicate of " + uniqueSuffix()

	start := time.Now()
	if err := s.MarkInvoiceVoided(ctx, inv.ID, reason); err != nil {
		t.Fatalf("MarkInvoiceVoided: %v", err)
	}
	end := time.Now()

	got, err := s.GetInvoice(ctx, inv.ID)
	if err != nil {
		t.Fatalf("GetInvoice: %v", err)
	}
	if got.Status != invoice.StatusVoided {
		t.Errorf("status: got %q, want %q", got.Status, invoice.StatusVoided)
	}
	if got.VoidedAt == nil {
		t.Error("voided_at was not stored")
	} else {
		requireStampedDuring(t, "voided_at", *got.VoidedAt, start, end)
	}
	if got.VoidReason != reason {
		t.Errorf("void_reason: got %q, want %q", got.VoidReason, reason)
	}
	requireStampedDuring(t, "updated_at", got.UpdatedAt, start, end)

	if err = s.MarkInvoiceVoided(ctx, id.NewInvoiceID(), reason); !errors.Is(err, ledger.ErrInvoiceNotFound) {
		t.Errorf("MarkInvoiceVoided on an unknown invoice: got %v, want ErrInvoiceNotFound", err)
	}
}

// testListFiltersCombine sets every filter each list method takes at once,
// with a row that fails each filter on its own, so a filter that binds the
// wrong argument, or is dropped, lets a row through or loses the one that
// should match. On postgres the filters are conditional WHERE clauses, and
// only a call that switches several of them on checks that their arguments
// still line up.
func testListFiltersCombine(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	otherApp := "app-other-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	otherTenant := "tenant-other-" + uniqueSuffix()

	t.Run("ListPlans", func(t *testing.T) {
		newPlan := func(app string, status plan.Status) *plan.Plan {
			p := &plan.Plan{
				Entity: types.NewEntity(), ID: id.NewPlanID(),
				Name: "Plan", Slug: "plan-filter-" + uniqueSuffix(), Currency: "usd",
				Status: status, AppID: app,
				Pricing: &plan.Pricing{
					ID: id.NewPriceID(), BaseAmount: types.USD(1000),
					BillingPeriod: plan.PeriodMonthly,
				},
			}
			if err := s.CreatePlan(ctx, p); err != nil {
				t.Fatalf("CreatePlan: %v", err)
			}
			return p
		}
		want := newPlan(appID, plan.StatusArchived)
		newPlan(appID, plan.StatusActive)
		newPlan(otherApp, plan.StatusArchived)

		got, err := s.ListPlans(ctx, appID, plan.ListOpts{Status: plan.StatusArchived})
		if err != nil {
			t.Fatalf("ListPlans: %v", err)
		}
		requireIDs(t, "ListPlans", planIDs(got), want.ID.String())
	})

	t.Run("ListFeatures", func(t *testing.T) {
		newFeature := func(app string, status feature.Status) *feature.Feature {
			f := newTestFeature(app)
			f.Status = status
			if err := s.CreateFeature(ctx, f); err != nil {
				t.Fatalf("CreateFeature: %v", err)
			}
			return f
		}
		want := newFeature(appID, feature.StatusArchived)
		newFeature(appID, feature.StatusActive)
		newFeature(otherApp, feature.StatusArchived)

		got, err := s.ListFeatures(ctx, appID, feature.ListOpts{Status: feature.StatusArchived})
		if err != nil {
			t.Fatalf("ListFeatures: %v", err)
		}
		requireIDs(t, "ListFeatures", featureIDs(got), want.ID.String())

		// Global features are shared by every run against a persistent
		// database, so this checks membership and the status of every row
		// returned, not an exact list.
		globalArchived := newFeature("", feature.StatusArchived)
		globalActive := newFeature("", feature.StatusActive)
		global, err := s.ListGlobalFeatures(ctx, feature.ListOpts{Status: feature.StatusArchived})
		if err != nil {
			t.Fatalf("ListGlobalFeatures: %v", err)
		}
		seen := map[string]bool{}
		for _, f := range global {
			seen[f.ID.String()] = true
			if f.AppID != "" || f.Status != feature.StatusArchived {
				t.Errorf("ListGlobalFeatures returned %s with app %q status %q, want global and archived", f.ID, f.AppID, f.Status)
			}
		}
		if !seen[globalArchived.ID.String()] {
			t.Errorf("ListGlobalFeatures left out the archived global feature %s", globalArchived.ID)
		}
		if seen[globalActive.ID.String()] || seen[want.ID.String()] {
			t.Error("ListGlobalFeatures returned an active or app-scoped feature")
		}
	})

	t.Run("ListSubscriptions", func(t *testing.T) {
		newSub := func(tenant, app string, status subscription.Status) *subscription.Subscription {
			sub := newTestSubscription(tenant, app)
			sub.Status = status
			if err := s.CreateSubscription(ctx, sub); err != nil {
				t.Fatalf("CreateSubscription: %v", err)
			}
			return sub
		}
		want := newSub(tenantID, appID, subscription.StatusTrialing)
		newSub(tenantID, appID, subscription.StatusActive)
		newSub(otherTenant, appID, subscription.StatusTrialing)
		newSub(tenantID, otherApp, subscription.StatusTrialing)

		got, err := s.ListSubscriptions(ctx, tenantID, appID, subscription.ListOpts{Status: subscription.StatusTrialing})
		if err != nil {
			t.Fatalf("ListSubscriptions: %v", err)
		}
		requireIDs(t, "ListSubscriptions", subscriptionIDs(got), want.ID.String())
	})

	t.Run("ListInvoices", func(t *testing.T) {
		base := time.Now().UTC().Truncate(time.Second)
		newInv := func(tenant, app string, status invoice.Status, start time.Time) *invoice.Invoice {
			inv := newTestInvoice(tenant, app)
			inv.Status = status
			inv.PeriodStart = start
			inv.PeriodEnd = start.AddDate(0, 1, 0)
			if err := s.CreateInvoice(ctx, inv); err != nil {
				t.Fatalf("CreateInvoice: %v", err)
			}
			return inv
		}
		want := newInv(tenantID, appID, invoice.StatusPending, base)
		pendingEarlier := newInv(tenantID, appID, invoice.StatusPending, base.AddDate(0, -3, 0))
		newInv(tenantID, appID, invoice.StatusPaid, base)
		newInv(otherTenant, appID, invoice.StatusPending, base)
		otherAppPending := newInv(tenantID, otherApp, invoice.StatusPending, base)

		got, err := s.ListInvoices(ctx, tenantID, appID, invoice.ListOpts{
			Status: invoice.StatusPending,
			Start:  base.Add(-time.Hour),
			End:    base.AddDate(0, 1, 0).Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		requireIDs(t, "ListInvoices", invoiceIDs(got), want.ID.String())

		// ListPendingInvoices filters by app and status only, so the
		// earlier period and the other tenant's invoice both come back.
		pending, err := s.ListPendingInvoices(ctx, appID)
		if err != nil {
			t.Fatalf("ListPendingInvoices: %v", err)
		}
		ids := invoiceIDs(pending)
		for _, inv := range []*invoice.Invoice{want, pendingEarlier} {
			if !containsID(ids, inv.ID.String()) {
				t.Errorf("ListPendingInvoices left out pending invoice %s", inv.ID)
			}
		}
		if containsID(ids, otherAppPending.ID.String()) {
			t.Error("ListPendingInvoices returned another app's invoice")
		}
		for _, inv := range pending {
			if inv.Status != invoice.StatusPending {
				t.Errorf("ListPendingInvoices returned %s with status %q", inv.ID, inv.Status)
			}
		}
	})

	t.Run("ListCoupons", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		earlier, later := now.Add(-time.Hour), now.Add(time.Hour)
		newCoupon := func(app string, from, until *time.Time) *coupon.Coupon {
			c := newTestCoupon(app)
			c.ValidFrom, c.ValidUntil = from, until
			if err := s.CreateCoupon(ctx, c); err != nil {
				t.Fatalf("CreateCoupon: %v", err)
			}
			return c
		}
		open := newCoupon(appID, nil, nil)
		inWindow := newCoupon(appID, &earlier, &later)
		newCoupon(appID, nil, &earlier) // expired
		newCoupon(appID, &later, nil)   // not yet valid
		newCoupon(otherApp, nil, nil)

		got, err := s.ListCoupons(ctx, appID, coupon.ListOpts{Active: true})
		if err != nil {
			t.Fatalf("ListCoupons: %v", err)
		}
		requireIDs(t, "ListCoupons", couponIDs(got), open.ID.String(), inWindow.ID.String())
	})
}

// requireIDs fails unless got holds exactly the want ids, in any order.
func requireIDs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s: got ids %v, want %v", what, g, w)
	}
}

func containsID(ids []string, want string) bool {
	for _, got := range ids {
		if got == want {
			return true
		}
	}
	return false
}

func planIDs(ps []*plan.Plan) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID.String()
	}
	return out
}

func featureIDs(fs []*feature.Feature) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.ID.String()
	}
	return out
}

func subscriptionIDs(subs []*subscription.Subscription) []string {
	out := make([]string, len(subs))
	for i, sub := range subs {
		out[i] = sub.ID.String()
	}
	return out
}

func invoiceIDs(invs []*invoice.Invoice) []string {
	out := make([]string, len(invs))
	for i, inv := range invs {
		out[i] = inv.ID.String()
	}
	return out
}

func couponIDs(cs []*coupon.Coupon) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID.String()
	}
	return out
}

// testLookupsMatchEveryArgument covers the single-row lookups, deletes and
// cache calls the rest of the suite never reaches. Each one is given a row
// that matches every argument next to rows that miss on exactly one, so an
// argument bound to the wrong column finds the wrong row, or none.
func testLookupsMatchEveryArgument(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	otherApp := "app-other-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	otherTenant := "tenant-other-" + uniqueSuffix()

	t.Run("GetPlanBySlugAndDeletePlan", func(t *testing.T) {
		slug := "plan-lookup-" + uniqueSuffix()
		newPlan := func(app string) *plan.Plan {
			p := &plan.Plan{
				Entity: types.NewEntity(), ID: id.NewPlanID(),
				Name: "Plan", Slug: slug, Currency: "usd",
				Status: plan.StatusActive, AppID: app,
				Pricing: &plan.Pricing{
					ID: id.NewPriceID(), BaseAmount: types.USD(1000),
					BillingPeriod: plan.PeriodMonthly,
				},
			}
			if err := s.CreatePlan(ctx, p); err != nil {
				t.Fatalf("CreatePlan: %v", err)
			}
			return p
		}
		mine, theirs := newPlan(appID), newPlan(otherApp)

		for _, want := range []*plan.Plan{mine, theirs} {
			got, err := s.GetPlanBySlug(ctx, slug, want.AppID)
			if err != nil {
				t.Fatalf("GetPlanBySlug(%q): %v", want.AppID, err)
			}
			if got.ID.String() != want.ID.String() {
				t.Errorf("GetPlanBySlug(%q): got plan %s, want %s", want.AppID, got.ID, want.ID)
			}
		}
		if _, err := s.GetPlanBySlug(ctx, slug, "app-none-"+uniqueSuffix()); !errors.Is(err, ledger.ErrPlanNotFound) {
			t.Errorf("GetPlanBySlug in an app without it: got %v, want ErrPlanNotFound", err)
		}

		if err := s.DeletePlan(ctx, mine.ID); err != nil {
			t.Fatalf("DeletePlan: %v", err)
		}
		if _, err := s.GetPlan(ctx, mine.ID); !errors.Is(err, ledger.ErrPlanNotFound) {
			t.Errorf("GetPlan after DeletePlan: got %v, want ErrPlanNotFound", err)
		}
		if _, err := s.GetPlan(ctx, theirs.ID); err != nil {
			t.Errorf("DeletePlan took the other app's plan with it: %v", err)
		}
		if err := s.DeletePlan(ctx, mine.ID); !errors.Is(err, ledger.ErrPlanNotFound) {
			t.Errorf("DeletePlan a second time: got %v, want ErrPlanNotFound", err)
		}
		if err := s.DeletePlan(ctx, id.NewPlanID()); !errors.Is(err, ledger.ErrPlanNotFound) {
			t.Errorf("DeletePlan on an unknown plan: got %v, want ErrPlanNotFound", err)
		}
	})

	t.Run("GetFeatureByKeyAndDeleteFeature", func(t *testing.T) {
		mine := newTestFeature(appID)
		theirs := newTestFeature(otherApp)
		theirs.Key = mine.Key
		for _, f := range []*feature.Feature{mine, theirs} {
			if err := s.CreateFeature(ctx, f); err != nil {
				t.Fatalf("CreateFeature: %v", err)
			}
		}

		for _, want := range []*feature.Feature{mine, theirs} {
			got, err := s.GetFeatureByKey(ctx, mine.Key, want.AppID)
			if err != nil {
				t.Fatalf("GetFeatureByKey(%q): %v", want.AppID, err)
			}
			if got.ID.String() != want.ID.String() {
				t.Errorf("GetFeatureByKey(%q): got feature %s, want %s", want.AppID, got.ID, want.ID)
			}
		}
		if _, err := s.GetFeatureByKey(ctx, mine.Key, "app-none-"+uniqueSuffix()); !errors.Is(err, ledger.ErrFeatureNotFound) {
			t.Errorf("GetFeatureByKey in an app without it: got %v, want ErrFeatureNotFound", err)
		}

		if err := s.DeleteFeature(ctx, mine.ID); err != nil {
			t.Fatalf("DeleteFeature: %v", err)
		}
		if _, err := s.GetFeature(ctx, mine.ID); !errors.Is(err, ledger.ErrFeatureNotFound) {
			t.Errorf("GetFeature after DeleteFeature: got %v, want ErrFeatureNotFound", err)
		}
		if _, err := s.GetFeature(ctx, theirs.ID); err != nil {
			t.Errorf("DeleteFeature took the other app's feature with it: %v", err)
		}
		if err := s.DeleteFeature(ctx, mine.ID); !errors.Is(err, ledger.ErrFeatureNotFound) {
			t.Errorf("DeleteFeature a second time: got %v, want ErrFeatureNotFound", err)
		}
		if err := s.DeleteFeature(ctx, id.NewFeatureID()); !errors.Is(err, ledger.ErrFeatureNotFound) {
			t.Errorf("DeleteFeature on an unknown feature: got %v, want ErrFeatureNotFound", err)
		}
	})

	t.Run("DeleteCoupon", func(t *testing.T) {
		mine, theirs := newTestCoupon(appID), newTestCoupon(otherApp)
		for _, c := range []*coupon.Coupon{mine, theirs} {
			if err := s.CreateCoupon(ctx, c); err != nil {
				t.Fatalf("CreateCoupon: %v", err)
			}
		}

		if err := s.DeleteCoupon(ctx, mine.ID); err != nil {
			t.Fatalf("DeleteCoupon: %v", err)
		}
		if _, err := s.GetCouponByID(ctx, mine.ID); !errors.Is(err, ledger.ErrCouponNotFound) {
			t.Errorf("GetCouponByID after DeleteCoupon: got %v, want ErrCouponNotFound", err)
		}
		if _, err := s.GetCouponByID(ctx, theirs.ID); err != nil {
			t.Errorf("DeleteCoupon took the other app's coupon with it: %v", err)
		}
		if err := s.DeleteCoupon(ctx, mine.ID); !errors.Is(err, ledger.ErrCouponNotFound) {
			t.Errorf("DeleteCoupon a second time: got %v, want ErrCouponNotFound", err)
		}
		if err := s.DeleteCoupon(ctx, id.NewCouponID()); !errors.Is(err, ledger.ErrCouponNotFound) {
			t.Errorf("DeleteCoupon on an unknown coupon: got %v, want ErrCouponNotFound", err)
		}
	})

	t.Run("GetActiveSubscription", func(t *testing.T) {
		base := time.Now().UTC().Truncate(time.Second)
		newSub := func(tenant, app string, status subscription.Status, age time.Duration) *subscription.Subscription {
			sub := newTestSubscription(tenant, app)
			sub.Status = status
			sub.CreatedAt = base.Add(-age)
			sub.UpdatedAt = sub.CreatedAt
			if err := s.CreateSubscription(ctx, sub); err != nil {
				t.Fatalf("CreateSubscription: %v", err)
			}
			return sub
		}
		// The newest active or trialing subscription wins. Every row newer
		// than it misses on one argument: its status, its tenant or its app.
		newSub(tenantID, appID, subscription.StatusActive, 3*time.Hour)
		want := newSub(tenantID, appID, subscription.StatusTrialing, 2*time.Hour)
		newSub(tenantID, appID, subscription.StatusCanceled, time.Hour)
		newSub(otherTenant, appID, subscription.StatusActive, time.Hour)
		newSub(tenantID, otherApp, subscription.StatusActive, time.Hour)

		got, err := s.GetActiveSubscription(ctx, tenantID, appID)
		if err != nil {
			t.Fatalf("GetActiveSubscription: %v", err)
		}
		if got.ID.String() != want.ID.String() {
			t.Errorf("GetActiveSubscription: got %s (%s, %s, %s), want %s", got.ID, got.TenantID, got.AppID, got.Status, want.ID)
		}

		lapsed := "tenant-lapsed-" + uniqueSuffix()
		newSub(lapsed, appID, subscription.StatusCanceled, time.Hour)
		if _, err = s.GetActiveSubscription(ctx, lapsed, appID); !errors.Is(err, ledger.ErrNoActiveSubscription) {
			t.Errorf("GetActiveSubscription with only a canceled one: got %v, want ErrNoActiveSubscription", err)
		}
	})

	t.Run("GetInvoiceByPeriod", func(t *testing.T) {
		start := time.Now().UTC().Truncate(time.Second)
		end := start.AddDate(0, 1, 0)
		newInv := func(tenant, app string, from, to time.Time) *invoice.Invoice {
			inv := newTestInvoice(tenant, app)
			inv.PeriodStart, inv.PeriodEnd = from, to
			if err := s.CreateInvoice(ctx, inv); err != nil {
				t.Fatalf("CreateInvoice: %v", err)
			}
			return inv
		}
		want := newInv(tenantID, appID, start, end)
		newInv(otherTenant, appID, start, end)
		newInv(tenantID, otherApp, start, end)
		newInv(tenantID, appID, start.Add(time.Hour), end)
		newInv(tenantID, appID, start, end.Add(time.Hour))

		got, err := s.GetInvoiceByPeriod(ctx, tenantID, appID, start, end)
		if err != nil {
			t.Fatalf("GetInvoiceByPeriod: %v", err)
		}
		if got.ID.String() != want.ID.String() {
			t.Errorf("GetInvoiceByPeriod: got %s, want %s", got.ID, want.ID)
		}
		if _, err = s.GetInvoiceByPeriod(ctx, tenantID, appID, start.Add(-time.Hour), end); !errors.Is(err, ledger.ErrInvoiceNotFound) {
			t.Errorf("GetInvoiceByPeriod for a period with no invoice: got %v, want ErrInvoiceNotFound", err)
		}
	})

	t.Run("EntitlementCache", func(t *testing.T) {
		set := func(tenant, app, key string, used int64, ttl time.Duration) {
			r := &entitlement.Result{Allowed: true, Feature: key, Used: used, Limit: 100, Remaining: 100 - used}
			if err := s.SetCached(ctx, tenant, app, key, r, ttl); err != nil {
				t.Fatalf("SetCached: %v", err)
			}
		}
		// used tells the entries apart, so a hit on the wrong one shows.
		requireHit := func(tenant, app, key string, used int64) {
			t.Helper()
			got, err := s.GetCached(ctx, tenant, app, key)
			if err != nil {
				t.Errorf("GetCached(%s, %s, %s): %v", tenant, app, key, err)
				return
			}
			if got.Used != used || got.Feature != key {
				t.Errorf("GetCached(%s, %s, %s): got used %d feature %q, want used %d", tenant, app, key, got.Used, got.Feature, used)
			}
		}
		requireMiss := func(tenant, app, key string) {
			t.Helper()
			if _, err := s.GetCached(ctx, tenant, app, key); !errors.Is(err, ledger.ErrCacheMiss) {
				t.Errorf("GetCached(%s, %s, %s): got %v, want ErrCacheMiss", tenant, app, key, err)
			}
		}

		keyA, keyB, keyC := "feat-a-"+uniqueSuffix(), "feat-b-"+uniqueSuffix(), "feat-c-"+uniqueSuffix()
		set(tenantID, appID, keyA, 1, time.Hour)
		set(tenantID, appID, keyB, 2, time.Hour)
		set(tenantID, appID, keyC, 3, -time.Minute) // already expired
		set(tenantID, otherApp, keyA, 4, time.Hour)
		set(otherTenant, appID, keyA, 5, time.Hour)

		requireHit(tenantID, appID, keyA, 1)
		requireHit(tenantID, appID, keyB, 2)
		requireMiss(tenantID, appID, keyC)
		requireHit(tenantID, otherApp, keyA, 4)
		requireHit(otherTenant, appID, keyA, 5)

		if err := s.InvalidateFeature(ctx, tenantID, appID, keyA); err != nil {
			t.Fatalf("InvalidateFeature: %v", err)
		}
		requireMiss(tenantID, appID, keyA)
		requireHit(tenantID, appID, keyB, 2)
		requireHit(tenantID, otherApp, keyA, 4)
		requireHit(otherTenant, appID, keyA, 5)

		if err := s.Invalidate(ctx, tenantID, appID); err != nil {
			t.Fatalf("Invalidate: %v", err)
		}
		requireMiss(tenantID, appID, keyB)
		requireHit(tenantID, otherApp, keyA, 4)
		requireHit(otherTenant, appID, keyA, 5)
	})

	t.Run("PurgeUsage", func(t *testing.T) {
		// PurgeUsage is not scoped to a tenant, so the cutoff sits in 2001,
		// where no other subtest's events live.
		cutoff := time.Date(2001, 6, 1, 0, 0, 0, 0, time.UTC)
		old := newTestUsageEvent(tenantID, appID)
		old.Timestamp = cutoff.Add(-24 * time.Hour)
		recent := newTestUsageEvent(tenantID, appID)
		if err := s.IngestBatch(ctx, []*meter.UsageEvent{old, recent}); err != nil {
			t.Fatalf("IngestBatch: %v", err)
		}

		n, err := s.PurgeUsage(ctx, cutoff)
		if err != nil {
			t.Fatalf("PurgeUsage: %v", err)
		}
		if n < 1 {
			t.Errorf("PurgeUsage: removed %d events, want at least the one before the cutoff", n)
		}
		got, err := s.QueryUsage(ctx, tenantID, appID, meter.QueryOpts{})
		if err != nil {
			t.Fatalf("QueryUsage: %v", err)
		}
		ids := make([]string, len(got))
		for i, e := range got {
			ids[i] = e.ID.String()
		}
		requireIDs(t, "QueryUsage after PurgeUsage", ids, recent.ID.String())
	})
}

// testUpdateOfAMissingRowReportsIt pins the whole-row updates to one rule:
// an id that isn't stored is refused with the entity's not-found error, and
// nothing is written. An update that would succeed on nothing hides a caller
// holding a stale or mistyped id, and memory once inserted the row instead.
//
// Each update is also run twenty times on a row that does exist. Every write
// stamps updated_at, but mongo keeps only milliseconds, so some of those
// writes land in the same millisecond as the one before and change nothing.
// A backend that counted modified rows, not matched ones, would call those
// not found. The guard rests on timing: with twenty writes it caught that
// mistake in 39 of 40 runs against a local mongo.
func testUpdateOfAMissingRowReportsIt(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()

	// check runs update on a row that was never created, then twenty times on
	// one that was, and uses get to confirm the missing row is still missing.
	check := func(t *testing.T, notFound error, update func(existing bool) error, get func() error) {
		t.Helper()
		if err := update(false); !errors.Is(err, notFound) {
			t.Errorf("update of a missing row: got %v, want %v", err, notFound)
		}
		if err := get(); !errors.Is(err, notFound) {
			t.Errorf("read after updating a missing row: got %v, want %v, so the update wrote it", err, notFound)
		}
		for i := 0; i < 20; i++ {
			if err := update(true); err != nil {
				t.Errorf("update %d of a stored row: %v", i+1, err)
			}
		}
	}

	t.Run("UpdatePlan", func(t *testing.T) {
		newPlan := func() *plan.Plan {
			return &plan.Plan{
				Entity: types.NewEntity(), ID: id.NewPlanID(),
				Name: "Plan", Slug: "plan-update-" + uniqueSuffix(), Currency: "usd",
				Status: plan.StatusActive, AppID: appID,
				Pricing: &plan.Pricing{
					ID: id.NewPriceID(), BaseAmount: types.USD(1000),
					BillingPeriod: plan.PeriodMonthly,
				},
			}
		}
		stored, missing := newPlan(), newPlan()
		if err := s.CreatePlan(ctx, stored); err != nil {
			t.Fatalf("CreatePlan: %v", err)
		}
		check(t, ledger.ErrPlanNotFound,
			func(existing bool) error {
				if existing {
					return s.UpdatePlan(ctx, stored)
				}
				return s.UpdatePlan(ctx, missing)
			},
			func() error { _, err := s.GetPlan(ctx, missing.ID); return err })
	})

	t.Run("UpdateFeature", func(t *testing.T) {
		stored, missing := newTestFeature(appID), newTestFeature(appID)
		if err := s.CreateFeature(ctx, stored); err != nil {
			t.Fatalf("CreateFeature: %v", err)
		}
		check(t, ledger.ErrFeatureNotFound,
			func(existing bool) error {
				if existing {
					return s.UpdateFeature(ctx, stored)
				}
				return s.UpdateFeature(ctx, missing)
			},
			func() error { _, err := s.GetFeature(ctx, missing.ID); return err })
	})

	t.Run("UpdateSubscription", func(t *testing.T) {
		stored, missing := newTestSubscription(tenantID, appID), newTestSubscription(tenantID, appID)
		if err := s.CreateSubscription(ctx, stored); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}
		check(t, ledger.ErrSubscriptionNotFound,
			func(existing bool) error {
				if existing {
					return s.UpdateSubscription(ctx, stored)
				}
				return s.UpdateSubscription(ctx, missing)
			},
			func() error { _, err := s.GetSubscription(ctx, missing.ID); return err })
	})

	t.Run("UpdateInvoice", func(t *testing.T) {
		stored, missing := newTestInvoice(tenantID, appID), newTestInvoice(tenantID, appID)
		if err := s.CreateInvoice(ctx, stored); err != nil {
			t.Fatalf("CreateInvoice: %v", err)
		}
		check(t, ledger.ErrInvoiceNotFound,
			func(existing bool) error {
				if existing {
					return s.UpdateInvoice(ctx, stored)
				}
				return s.UpdateInvoice(ctx, missing)
			},
			func() error { _, err := s.GetInvoice(ctx, missing.ID); return err })
	})

	t.Run("UpdateCoupon", func(t *testing.T) {
		stored, missing := newTestCoupon(appID), newTestCoupon(appID)
		if err := s.CreateCoupon(ctx, stored); err != nil {
			t.Fatalf("CreateCoupon: %v", err)
		}
		check(t, ledger.ErrCouponNotFound,
			func(existing bool) error {
				if existing {
					return s.UpdateCoupon(ctx, stored)
				}
				return s.UpdateCoupon(ctx, missing)
			},
			func() error { _, err := s.GetCouponByID(ctx, missing.ID); return err })
	})
}
