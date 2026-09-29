package contract

import (
	"context"
	"strings"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

func registerCoupons(b *binder) {
	query(b, "coupons.list", couponsList)
	query(b, "coupons.detail", couponsDetail)
	command(b, "coupons.create", couponsCreate)
	command(b, "coupons.update", couponsUpdate)
	command(b, "coupons.delete", couponsDelete)
	command(b, "coupons.apply", couponsApply)
}

func loadCoupon(ctx context.Context, eng *ledger.Ledger, sc scope, raw string) (*coupon.Coupon, error) {
	couponID, err := parseID("id", raw, id.ParseCouponID)
	if err != nil {
		return nil, err
	}
	c, err := eng.Store().GetCouponByID(ctx, couponID)
	if err != nil {
		return nil, err
	}
	if !sc.owns(c.AppID) {
		return nil, notFound("coupon")
	}
	return c, nil
}

type CouponsListInput struct {
	PageInput
	// Active lists only coupons inside their validity window now.
	Active bool `json:"active"`
}

func couponsList(ctx context.Context, eng *ledger.Ledger, sc scope, in CouponsListInput) (Page[*coupon.Coupon], error) {
	limit, offset := in.window()
	rows, err := eng.Store().ListCoupons(ctx, sc.AppID, coupon.ListOpts{Active: in.Active, Limit: limit + 1, Offset: offset})
	if err != nil {
		return Page[*coupon.Coupon]{}, err
	}
	return pageFrom(rows, limit, offset), nil
}

func couponsDetail(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*coupon.Coupon, error) {
	return loadCoupon(ctx, eng, sc, in.ID)
}

type CouponCreateInput struct {
	Code           string            `json:"code"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Amount         types.Money       `json:"amount"`
	Percentage     int               `json:"percentage"`
	Currency       string            `json:"currency"`
	MaxRedemptions int               `json:"max_redemptions"`
	ValidFrom      *time.Time        `json:"valid_from"`
	ValidUntil     *time.Time        `json:"valid_until"`
	Metadata       map[string]string `json:"metadata"`
}

func couponsCreate(ctx context.Context, eng *ledger.Ledger, sc scope, in CouponCreateInput) (*coupon.Coupon, error) {
	c := &coupon.Coupon{
		Code: in.Code, Name: in.Name, Type: coupon.CouponType(in.Type), Amount: in.Amount,
		Percentage: in.Percentage, Currency: in.Currency, MaxRedemptions: in.MaxRedemptions,
		ValidFrom: in.ValidFrom, ValidUntil: in.ValidUntil, Metadata: in.Metadata, AppID: sc.AppID,
	}
	if err := eng.CreateCoupon(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// CouponUpdateInput carries only the fields a coupon may change after it is
// created. Its code, type, value and currency are fixed, because applied
// coupons are priced from them on every future invoice.
type CouponUpdateInput struct {
	ID             string              `json:"id"`
	Name           *string             `json:"name"`
	MaxRedemptions *int                `json:"max_redemptions"`
	ValidFrom      Nullable[time.Time] `json:"valid_from"`
	ValidUntil     Nullable[time.Time] `json:"valid_until"`
	Metadata       *map[string]string  `json:"metadata"`
}

func couponsUpdate(ctx context.Context, eng *ledger.Ledger, sc scope, in CouponUpdateInput) (*coupon.Coupon, error) {
	c, err := loadCoupon(ctx, eng, sc, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.MaxRedemptions != nil {
		c.MaxRedemptions = *in.MaxRedemptions
	}
	c.ValidFrom = in.ValidFrom.apply(c.ValidFrom)
	c.ValidUntil = in.ValidUntil.apply(c.ValidUntil)
	if in.Metadata != nil {
		c.Metadata = *in.Metadata
	}
	if err := eng.UpdateCoupon(ctx, c); err != nil {
		return nil, err
	}
	return eng.Store().GetCouponByID(ctx, c.ID)
}

func couponsDelete(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (Ack, error) {
	c, err := loadCoupon(ctx, eng, sc, in.ID)
	if err != nil {
		return Ack{}, err
	}
	if err := eng.DeleteCoupon(ctx, c.ID); err != nil {
		return Ack{}, err
	}
	return Ack{OK: true}, nil
}

type CouponApplyInput struct {
	SubscriptionID string `json:"subscription_id"`
	Code           string `json:"code"`
}

// couponsApply redeems a coupon onto a subscription. The code is looked up in
// the subscription's app, which loadSubscription has already pinned to the
// scope, so one app can never apply another's code.
func couponsApply(ctx context.Context, eng *ledger.Ledger, sc scope, in CouponApplyInput) (*coupon.Coupon, error) {
	sub, err := loadSubscription(ctx, eng, sc, "subscription_id", in.SubscriptionID)
	if err != nil {
		return nil, err
	}
	code := strings.TrimSpace(in.Code)
	if code == "" {
		return nil, badRequest("code is required")
	}
	return eng.ApplyCoupon(ctx, sub.ID, code)
}
