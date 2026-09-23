package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

// newInternalTestStore opens a file-backed, migrated store for tests that
// live inside this package and need direct access to unexported types
// (couponApplicationModel, s.sdb) and helpers (isUniqueViolation). A file
// under t.TempDir() is used rather than ":memory:" for the same reason
// storetest.NewSQLite does: every pooled connection re-opens the DSN, and
// ":memory:" would hand each one an independent, empty database.
func newInternalTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "internal.db")
	// No _txlock=immediate: see the comment on storetest.NewSQLite for why
	// RedeemCoupon no longer needs it - its transaction opens with a write,
	// not a read, so this runs the same DSN shape production does.
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(5000)"

	sdb := sqlitedriver.New()
	if err := sdb.Open(ctx, dsn); err != nil {
		t.Fatalf("open sqlite driver: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	db, err := grove.Open(sdb)
	if err != nil {
		t.Fatalf("grove.Open: %v", err)
	}

	s := New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// TestIsUniqueViolationMapsRawDuplicateInsertError provokes a real unique
// constraint violation directly through the store's own builder - bypassing
// ApplyCoupon entirely - and pins that isUniqueViolation recognizes the raw
// driver error by its typed extended result code. This is the deterministic
// counterpart to the conformance suite's concurrent double-apply test: that
// test may or may not provoke the race on a given run, but this one always
// does, because it inserts the duplicate row itself instead of racing for
// it.
func TestIsUniqueViolationMapsRawDuplicateInsertError(t *testing.T) {
	ctx := context.Background()
	s := newInternalTestStore(t)

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: "DIRECT-" + id.New(id.Prefix("test")).String(), Name: "direct",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: "app-direct",
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	// Insert a second row with the same (coupon_id, subscription_id) pair
	// directly through the builder ApplyCoupon itself uses, so the only
	// thing that stands between this raw error and the test is
	// isUniqueViolation - not ApplyCoupon's own control flow.
	dup := &couponApplicationModel{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       c.ID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	_, err := s.sdb.NewInsert(dup).Exec(ctx)
	if err == nil {
		t.Fatal("direct duplicate insert: got nil error, want a UNIQUE constraint violation")
	}
	if !isUniqueViolation(err) {
		t.Fatalf("isUniqueViolation(%v) = false, want true (raw error: %#v)", err, err)
	}
}

// TestIsUniqueViolationRejectsUnrelatedErrors guards the other direction:
// a helper that returns true for everything would pass the test above for
// the wrong reason. A not-found error (no rows) must not be mistaken for a
// unique violation.
func TestIsUniqueViolationRejectsUnrelatedErrors(t *testing.T) {
	ctx := context.Background()
	s := newInternalTestStore(t)

	existing := new(couponApplicationModel)
	err := s.sdb.NewSelect(existing).
		Where("coupon_id = ?", "does-not-exist").
		Scan(ctx)
	if err == nil {
		t.Fatal("Select on an empty table: got nil error, want sql.ErrNoRows")
	}
	if isUniqueViolation(err) {
		t.Fatalf("isUniqueViolation(%v) = true, want false: this is a no-rows error, not a constraint violation", err)
	}
}

// TestListAppliedCouponsPropagatesNonNotFoundErrors is the deterministic
// reproduction the fix-1 report said it could not find through the public
// interface alone: it needs a way to make GetCouponByID fail with
// something other than ErrCouponNotFound from inside
// ListAppliedCoupons' per-row loop, while the outer select still
// succeeds. Dropping the coupons table out from under a surviving
// application row does exactly that - ledger_coupon_applications carries
// no REFERENCES on sqlite (unlike postgres), so the drop succeeds and the
// application row is untouched, but every subsequent GetCouponByID call
// now fails with "no such table" rather than "no such row".
//
// This test only exists on sqlite: postgres's scratch database is shared
// across runs and dropping a table there would break every other test
// that touches it, and mongo has no equivalent DDL operation to reach for.
// Postgres and mongo run the identical I2 logic with no test of their own
// to pin it - this sqlite test is the only guard for that code shape
// across all three backends.
func TestListAppliedCouponsPropagatesNonNotFoundErrors(t *testing.T) {
	ctx := context.Background()
	s := newInternalTestStore(t)

	c := &coupon.Coupon{
		Entity: types.NewEntity(), ID: id.NewCouponID(),
		Code: "DROPTABLE-" + id.New(id.Prefix("test")).String(), Name: "drop-table",
		Type: coupon.CouponTypePercentage, Percentage: 10,
		Currency: "usd", AppID: "app-drop-table",
	}
	if err := s.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	subID := id.NewSubscriptionID()
	if err := s.ApplyCoupon(ctx, subID, c.ID); err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}

	if _, err := s.sdb.Exec(ctx, "DROP TABLE ledger_coupons"); err != nil {
		t.Fatalf("DROP TABLE ledger_coupons: %v", err)
	}

	applied, err := s.ListAppliedCoupons(ctx, subID)
	if err == nil {
		t.Fatalf("ListAppliedCoupons after dropping ledger_coupons: got nil error and %d row(s), want a non-nil error", len(applied))
	}
	if errors.Is(err, ledger.ErrCouponNotFound) {
		t.Fatalf("ListAppliedCoupons after dropping ledger_coupons: got ErrCouponNotFound, want some other error - the table is gone, which is not the same thing as the coupon not existing")
	}
}
