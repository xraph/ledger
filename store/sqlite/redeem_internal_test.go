package sqlite

import (
	"context"
	"testing"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

// TestRedeemCouponRollsBackOnAFailedIncrement pins rule 6 from the task
// brief: any failure after the application insert must leave no
// application row and no moved count behind. It installs a trigger that
// aborts every UPDATE on ledger_coupons, so the conditional increment
// RedeemCoupon issues inside its transaction always fails after the
// application row has already been inserted in the same transaction.
//
// This test only exists on sqlite: postgres's scratch database is shared
// across test runs, and a trigger there would break every other test that
// updates ledger_coupons.
func TestRedeemCouponRollsBackOnAFailedIncrement(t *testing.T) {
	ctx := context.Background()
	s := newInternalTestStore(t)

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: "ROLLBACK-" + id.New(id.Prefix("test")).String(), Name: "rollback",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: "app-rollback",
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	if _, err := s.sdb.Exec(ctx, `
CREATE TRIGGER fail_increment BEFORE UPDATE ON ledger_coupons
BEGIN SELECT RAISE(ABORT, 'forced failure'); END;
`); err != nil {
		t.Fatalf("create fail_increment trigger: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.RedeemCoupon(ctx, subID, c.ID); err == nil {
		t.Fatal("RedeemCoupon with the increment forced to fail: got nil error, want a non-nil one")
	}

	// The insert that ran earlier in the same transaction must have been
	// rolled back along with the failed increment: no application row for
	// this subscription, and no attempted commit.
	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons after the forced failure: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("got %d applied coupons after a rolled-back redeem, want 0", len(applied))
	}

	stored, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID after the forced failure: %v", err)
	}
	if stored.TimesRedeemed != 0 {
		t.Errorf("got TimesRedeemed %d after a rolled-back redeem, want 0", stored.TimesRedeemed)
	}

	if _, err = s.sdb.Exec(ctx, `DROP TRIGGER fail_increment`); err != nil {
		t.Fatalf("drop fail_increment trigger: %v", err)
	}

	// The rollback must not have left anything behind that blocks a retry:
	// the same (subscription, coupon) pair redeems cleanly once the trigger
	// is gone, and the count catches up to exactly 1.
	if err = s.RedeemCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("RedeemCoupon after dropping the trigger: %v", err)
	}

	applied, err = s.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons after the retry: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("got %d applied coupons after the retry, want 1", len(applied))
	}

	stored, err = s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID after the retry: %v", err)
	}
	if stored.TimesRedeemed != 1 {
		t.Errorf("got TimesRedeemed %d after the retry, want 1", stored.TimesRedeemed)
	}
}
