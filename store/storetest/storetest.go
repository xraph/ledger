package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
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
	t.Run("SubscriptionTenantIsolation", func(t *testing.T) { testSubscriptionTenantIsolation(t, newStore(t)) })
	t.Run("InvoiceTenantIsolation", func(t *testing.T) { testInvoiceTenantIsolation(t, newStore(t)) })
	t.Run("UsageTenantIsolation", func(t *testing.T) { testUsageTenantIsolation(t, newStore(t)) })
	t.Run("EmptyTenantIDBehavior", func(t *testing.T) { testEmptyTenantIDBehavior(t, newStore(t)) })
}

// uniqueSuffix returns a value that differs on every call, including across
// separate process invocations, by borrowing a TypeID's UUIDv7-based
// randomness. The "test" prefix only labels where the value came from; it is
// never parsed back as a real entity id.
//
// Fixture values that a backend enforces as unique — a plan slug, a coupon
// code, an app id used as part of a unique index — carry this suffix so the
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
// tenant's rows back for the given app — not zero rows, but also not every
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
