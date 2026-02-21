package coupon

import (
	"context"

	"github.com/xraph/ledger/id"
)

type Store interface {
	Create(ctx context.Context, c *Coupon) error
	Get(ctx context.Context, code string, appID string) (*Coupon, error)
	GetByID(ctx context.Context, couponID id.CouponID) (*Coupon, error)
	List(ctx context.Context, appID string, opts ListOpts) ([]*Coupon, error)
	Update(ctx context.Context, c *Coupon) error
	Delete(ctx context.Context, couponID id.CouponID) error
}

type ListOpts struct {
	Active bool
	Limit  int
	Offset int
}
