package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

// newInternalTestStore opens a migrated store against LEDGER_TEST_POSTGRES_DSN
// for tests that live inside this package and need direct access to
// unexported types (couponApplicationModel, s.pg) and helpers
// (isUniqueViolation, isForeignKeyViolation). It skips like
// storetest.NewPostgres does when the DSN isn't set.
func newInternalTestStore(t *testing.T) *Store {
	t.Helper()

	dsn := os.Getenv("LEDGER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LEDGER_TEST_POSTGRES_DSN to run this test")
	}

	ctx := context.Background()

	pg := pgdriver.New()
	if err := pg.Open(ctx, dsn); err != nil {
		t.Fatalf("open postgres driver: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })

	db, err := grove.Open(pg)
	if err != nil {
		t.Fatalf("grove.Open: %v", err)
	}

	s := New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// TestIsUniqueViolationMapsRawDuplicateInsertError provokes a real 23505
// (unique_violation) directly through the store's own builder - bypassing
// ApplyCoupon entirely - and pins that isUniqueViolation recognizes the raw
// pgconn.PgError by its typed SQLSTATE code. This is the deterministic
// counterpart to the conformance suite's concurrent double-apply test: that
// test may or may not provoke the race on a given run, but this one always
// does.
func TestIsUniqueViolationMapsRawDuplicateInsertError(t *testing.T) {
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

	dup := &couponApplicationModel{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       c.ID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	_, err := s.pg.NewInsert(dup).Exec(ctx)
	if err == nil {
		t.Fatal("direct duplicate insert: got nil error, want a unique_violation")
	}
	if !isUniqueViolation(err) {
		t.Fatalf("isUniqueViolation(%v) = false, want true (raw error: %#v)", err, err)
	}
	if isForeignKeyViolation(err) {
		t.Fatalf("isForeignKeyViolation(%v) = true, want false: this is a unique violation, not a foreign key one", err)
	}
}

// TestIsForeignKeyViolationMapsRawInsertAgainstUnknownCoupon provokes a real
// 23503 (foreign_key_violation) by inserting an application row that
// references a coupon id that was never created, directly through the
// builder. This is M6: if the coupon is deleted between ApplyCoupon's
// existence check and its insert, the insert itself must still be
// recognizable as "the coupon is gone" rather than surfacing as a raw
// constraint error.
func TestIsForeignKeyViolationMapsRawInsertAgainstUnknownCoupon(t *testing.T) {
	ctx := context.Background()
	s := newInternalTestStore(t)

	m := &couponApplicationModel{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       id.NewCouponID().String(), // never created
		SubscriptionID: id.NewSubscriptionID().String(),
		AppliedAt:      now(),
	}
	_, err := s.pg.NewInsert(m).Exec(ctx)
	if err == nil {
		t.Fatal("insert referencing an unknown coupon: got nil error, want a foreign_key_violation")
	}
	if !isForeignKeyViolation(err) {
		t.Fatalf("isForeignKeyViolation(%v) = false, want true (raw error: %#v)", err, err)
	}
	if isUniqueViolation(err) {
		t.Fatalf("isUniqueViolation(%v) = true, want false: this is a foreign key violation, not a unique one", err)
	}
}
