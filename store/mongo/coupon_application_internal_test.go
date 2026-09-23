package mongo

import (
	"context"
	"os"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

// newInternalTestStore opens a migrated store against LEDGER_TEST_MONGO_URI
// for tests that live inside this package and need direct access to
// unexported types (couponApplicationDoc, s.mdb) and helpers
// (isDuplicateKeyError). It skips like storetest.NewMongo does when the URI
// isn't set.
func newInternalTestStore(t *testing.T) *Store {
	t.Helper()

	uri := os.Getenv("LEDGER_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set LEDGER_TEST_MONGO_URI to run this test")
	}

	ctx := context.Background()

	md := mongodriver.New()
	if err := md.Open(ctx, uri); err != nil {
		t.Fatalf("open mongo driver: %v", err)
	}
	t.Cleanup(func() { _ = md.Close() })

	db, err := grove.Open(md)
	if err != nil {
		t.Fatalf("grove.Open: %v", err)
	}

	s := New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// TestIsDuplicateKeyErrorMapsRawDuplicateInsertError provokes a real E11000
// directly through the store's own builder - bypassing ApplyCoupon entirely
// - and pins that isDuplicateKeyError recognizes the raw driver error via
// the official driver's typed classification. This is the deterministic
// counterpart to the conformance suite's concurrent double-apply test: that
// test may or may not provoke the race on a given run, but this one always
// does.
//
// This test needs a real mongod (LEDGER_TEST_MONGO_URI) and could not be
// run in this environment - no mongo server is reachable here. It was
// verified by reading the grove mongo driver and official driver sources
// only; see the fix report for details.
func TestIsDuplicateKeyErrorMapsRawDuplicateInsertError(t *testing.T) {
	ctx := context.Background()
	s := newInternalTestStore(t)

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: "DIRECT-" + id.New(id.Prefix("test")).String(), Name: "direct",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: "app-direct-" + id.New(id.Prefix("test")).String(),
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	dup := &couponApplicationDoc{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       c.ID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	_, err := s.mdb.NewInsert(dup).Exec(ctx)
	if err == nil {
		t.Fatal("direct duplicate insert: got nil error, want a duplicate key error (E11000)")
	}
	if !isDuplicateKeyError(err) {
		t.Fatalf("isDuplicateKeyError(%v) = false, want true (raw error: %#v)", err, err)
	}
}
