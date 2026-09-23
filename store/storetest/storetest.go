package storetest

import (
	"context"
	"testing"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/plan"
	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/types"
)

// Run executes the conformance suite against a store. newStore is called
// once per subtest so each starts from a clean database.
func Run(t *testing.T, newStore func(t *testing.T) ledgerstore.Store) {
	t.Helper()

	t.Run("PlanRoundTrip", func(t *testing.T) { testPlanRoundTrip(t, newStore(t)) })
	t.Run("CouponRoundTrip", func(t *testing.T) { testCouponRoundTrip(t, newStore(t)) })
	t.Run("GetCouponByIDUnknown", func(t *testing.T) { testGetCouponByIDUnknown(t, newStore(t)) })
}

func testPlanRoundTrip(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()

	p := &plan.Plan{
		Entity: types.NewEntity(), ID: id.NewPlanID(),
		Name: "Pro", Slug: "pro", Currency: "usd",
		Status: plan.StatusActive, AppID: "app_1",
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
	if got.Slug != "pro" {
		t.Errorf("got slug %q, want pro", got.Slug)
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

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: "LAUNCH10", Name: "Launch discount",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: "app_1",
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	byID, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if byID.Code != "LAUNCH10" {
		t.Errorf("got code %q, want LAUNCH10", byID.Code)
	}

	byCode, err := s.GetCoupon(ctx, "LAUNCH10", "app_1")
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
