package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/types"
)

func newSubID() id.SubscriptionID { return id.NewSubscriptionID() }

func percentCoupon(code, app string, pct int) *coupon.Coupon {
	return &coupon.Coupon{Code: code, Name: code, Type: coupon.CouponTypePercentage, Percentage: pct, Currency: "usd", AppID: app}
}

func TestCreateCouponAssignsIdentityAndZeroesTheCount(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())

	c := percentCoupon("LAUNCH10", "app_1", 10)
	c.TimesRedeemed = 7
	if err := l.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if c.ID.IsNil() {
		t.Error("CreateCoupon must assign an id")
	}
	stored, err := l.Store().GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if stored.TimesRedeemed != 0 {
		t.Errorf("TimesRedeemed = %d, want 0: a new coupon has never been redeemed", stored.TimesRedeemed)
	}
}

func TestCreateCouponNormalisesCurrency(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())

	c := &coupon.Coupon{Code: "FLAT5", Name: "Flat", Type: coupon.CouponTypeAmount, Amount: types.Money{Amount: 500}, Currency: "USD", AppID: "app_1"}
	if err := l.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if c.Currency != "usd" || c.Amount.Currency != "usd" {
		t.Errorf("currencies = %q / %q, want usd / usd", c.Currency, c.Amount.Currency)
	}
}

func TestCreateCouponRejectsADuplicateCodeInTheSameApp(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())

	if err := l.CreateCoupon(ctx, percentCoupon("DUP", "app_1", 10)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := l.CreateCoupon(ctx, percentCoupon("DUP", "app_1", 20)); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("same app: got %v, want ErrAlreadyExists", err)
	}
	if err := l.CreateCoupon(ctx, percentCoupon("DUP", "app_2", 20)); err != nil {
		t.Errorf("another app may reuse the code: %v", err)
	}
}

func TestCreateCouponRejectsInvalidShapes(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	cases := []struct {
		name string
		c    *coupon.Coupon
	}{
		{"empty code", percentCoupon("  ", "app_1", 10)},
		{"percentage above 100", percentCoupon("P101", "app_1", 101)},
		{"negative percentage", percentCoupon("PNEG", "app_1", -1)},
		{"negative amount", &coupon.Coupon{Code: "ANEG", Type: coupon.CouponTypeAmount, Amount: types.USD(-1), Currency: "usd", AppID: "app_1"}},
		{"amount with no currency anywhere", &coupon.Coupon{Code: "ANOC", Type: coupon.CouponTypeAmount, Amount: types.Money{Amount: 100}, AppID: "app_1"}},
		{"unknown type", &coupon.Coupon{Code: "FIXED", Type: "fixed", Currency: "usd", AppID: "app_1"}},
		{"empty type", &coupon.Coupon{Code: "NOTYPE", Currency: "usd", AppID: "app_1"}},
		{"window ends before it starts", &coupon.Coupon{Code: "WIN", Type: coupon.CouponTypePercentage, Percentage: 10, Currency: "usd", AppID: "app_1", ValidFrom: &future, ValidUntil: &past}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := ledger.New(memory.New())
			if err := l.CreateCoupon(context.Background(), c.c); !errors.Is(err, ledger.ErrCouponInvalid) {
				t.Errorf("got %v, want ErrCouponInvalid", err)
			}
		})
	}
}

func TestUpdateCouponChangesOnlyMutableFields(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())

	c := percentCoupon("EDIT", "app_1", 10)
	if err := l.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}

	until := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	edit := *c
	edit.Name = "Renamed"
	edit.MaxRedemptions = 50
	edit.ValidUntil = &until
	if err := l.UpdateCoupon(ctx, &edit); err != nil {
		t.Fatalf("UpdateCoupon: %v", err)
	}
	got, _ := l.Store().GetCouponByID(ctx, c.ID)
	if got.Name != "Renamed" || got.MaxRedemptions != 50 || got.ValidUntil == nil || !got.ValidUntil.Equal(until) {
		t.Errorf("mutable fields not saved: %+v", got)
	}

	immutable := []struct {
		name   string
		mutate func(*coupon.Coupon)
	}{
		{"code", func(x *coupon.Coupon) { x.Code = "OTHER" }},
		{"type", func(x *coupon.Coupon) { x.Type = coupon.CouponTypeAmount; x.Amount = types.USD(100) }},
		{"percentage", func(x *coupon.Coupon) { x.Percentage = 90 }},
		{"currency", func(x *coupon.Coupon) { x.Currency = "eur" }},
		{"app", func(x *coupon.Coupon) { x.AppID = "app_2" }},
	}
	for _, m := range immutable {
		t.Run(m.name, func(t *testing.T) {
			bad := *got
			m.mutate(&bad)
			if err := l.UpdateCoupon(ctx, &bad); !errors.Is(err, ledger.ErrCouponInvalid) {
				t.Errorf("changing %s: got %v, want ErrCouponInvalid", m.name, err)
			}
		})
	}
}

func TestUpdateCouponKeepsTheRedemptionCount(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)

	c := percentCoupon("COUNT", "app_1", 10)
	if err := l.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.RedeemCoupon(ctx, newSubID(), c.ID); err != nil {
			t.Fatalf("RedeemCoupon: %v", err)
		}
	}

	stale := *c // still carries TimesRedeemed 0
	stale.Name = "Edited after two redemptions"
	if err := l.UpdateCoupon(ctx, &stale); err != nil {
		t.Fatalf("UpdateCoupon: %v", err)
	}
	got, _ := s.GetCouponByID(ctx, c.ID)
	if got.TimesRedeemed != 2 {
		t.Errorf("TimesRedeemed = %d, want 2: an edit must never roll the count back", got.TimesRedeemed)
	}
}

func TestDeleteCoupon(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New())
	c := percentCoupon("GONE", "app_1", 10)
	if err := l.CreateCoupon(ctx, c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if err := l.DeleteCoupon(ctx, c.ID); err != nil {
		t.Fatalf("DeleteCoupon: %v", err)
	}
	if _, err := l.Store().GetCouponByID(ctx, c.ID); !errors.Is(err, ledger.ErrCouponNotFound) {
		t.Errorf("after delete: got %v, want ErrCouponNotFound", err)
	}
}
