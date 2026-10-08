package memory

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

// newLaunchCoupon returns an unsaved 10% coupon, code LAUNCH10, in app_1.
func newLaunchCoupon() *coupon.Coupon {
	return &coupon.Coupon{
		Entity:         types.NewEntity(),
		ID:             id.NewCouponID(),
		Code:           "LAUNCH10",
		Name:           "LAUNCH10",
		Type:           coupon.CouponTypePercentage,
		Percentage:     10,
		Currency:       "usd",
		MaxRedemptions: 0,
		AppID:          "app_1",
	}
}

func TestApplyCouponRecordsTheRedemption(t *testing.T) {
	ctx := context.Background()
	s := New()

	c := newLaunchCoupon()
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
	if applied[0].Code != "LAUNCH10" {
		t.Errorf("got code %q, want LAUNCH10", applied[0].Code)
	}
}

func TestApplyCouponIsIdempotentPerSubscription(t *testing.T) {
	ctx := context.Background()
	s := New()

	c := newLaunchCoupon()
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("first ApplyCoupon: %v", err)
	}

	err := s.ApplyCoupon(ctx, subID, c.ID)
	if err == nil {
		t.Fatal("second ApplyCoupon: got nil error, want a rejection")
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("got %d applied coupons after a duplicate apply, want 1", len(applied))
	}
}

func TestListAppliedCouponsIsScopedToOneSubscription(t *testing.T) {
	ctx := context.Background()
	s := New()

	c := newLaunchCoupon()
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

func TestListAppliedCouponsOnUnknownSubscriptionIsEmptyNotAnError(t *testing.T) {
	ctx := context.Background()
	s := New()

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

func TestIncrementCouponRedemptions(t *testing.T) {
	ctx := context.Background()
	s := New()

	c := newLaunchCoupon()
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

func TestIncrementCouponRedemptionsOnUnknownCoupon(t *testing.T) {
	ctx := context.Background()
	s := New()

	err := s.IncrementCouponRedemptions(ctx, id.NewCouponID())
	if err == nil {
		t.Fatal("got nil error for an unknown coupon, want ErrCouponNotFound")
	}
}

func TestApplyCouponStampsAppliedAt(t *testing.T) {
	ctx := context.Background()
	s := New()

	c := newLaunchCoupon()
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	before := time.Now().UTC().Add(-time.Second)
	subID := id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	apps := s.couponApplicationsFor(subID)
	if len(apps) != 1 {
		t.Fatalf("got %d applications, want 1", len(apps))
	}
	if apps[0].AppliedAt.Before(before) || apps[0].AppliedAt.After(after) {
		t.Errorf("AppliedAt %v is outside [%v, %v]", apps[0].AppliedAt, before, after)
	}
}
