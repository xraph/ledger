package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

// normaliseCoupon lowercases a coupon's currencies and gives an amount coupon's
// Money the coupon's currency when the Money carries none.
func normaliseCoupon(c *coupon.Coupon) {
	c.Code = strings.TrimSpace(c.Code)
	c.Currency = strings.ToLower(strings.TrimSpace(c.Currency))
	c.Amount.Currency = strings.ToLower(strings.TrimSpace(c.Amount.Currency))
	if c.Type == coupon.CouponTypeAmount && c.Amount.Currency == "" {
		c.Amount.Currency = c.Currency
	}
}

// validateCouponShape checks the fields that decide how a coupon discounts.
// ApplyCoupon, CreateCoupon and UpdateCoupon all use it, so a coupon that could
// not be applied cannot be created either.
func validateCouponShape(c *coupon.Coupon) error {
	if c.Code == "" {
		return fmt.Errorf("%w: a coupon needs a code", ErrCouponInvalid)
	}
	switch c.Type {
	case coupon.CouponTypePercentage:
		if c.Percentage < 0 || c.Percentage > 100 {
			return fmt.Errorf("%w: coupon %q has percentage %d, outside 0 to 100", ErrCouponInvalid, c.Code, c.Percentage)
		}
	case coupon.CouponTypeAmount:
		if c.Amount.Amount < 0 {
			return fmt.Errorf("%w: coupon %q has a negative amount", ErrCouponInvalid, c.Code)
		}
		if c.Amount.Currency == "" {
			return fmt.Errorf("%w: amount coupon %q has no currency", ErrCouponInvalid, c.Code)
		}
	default:
		return fmt.Errorf("%w: coupon %q has unsupported type %q", ErrCouponInvalid, c.Code, c.Type)
	}
	if c.ValidFrom != nil && c.ValidUntil != nil && c.ValidUntil.Before(*c.ValidFrom) {
		return fmt.Errorf("%w: coupon %q is valid until before it is valid from", ErrCouponInvalid, c.Code)
	}
	return nil
}

// CreateCoupon validates and stores a new coupon. A code is unique within an
// app. A new coupon's redemption count is always zero.
func (l *Ledger) CreateCoupon(ctx context.Context, c *coupon.Coupon) error {
	normaliseCoupon(c)
	if err := validateCouponShape(c); err != nil {
		return err
	}

	existing, err := l.store.GetCoupon(ctx, c.Code, c.AppID)
	switch {
	case err == nil && existing != nil:
		return fmt.Errorf("%w: coupon code %q already exists in this app", ErrAlreadyExists, c.Code)
	case err != nil && !errors.Is(err, ErrCouponNotFound):
		return err
	}

	if c.ID.IsNil() {
		c.ID = id.NewCouponID()
	}
	c.Entity = types.NewEntity()
	c.TimesRedeemed = 0

	return l.store.CreateCoupon(ctx, c)
}

// UpdateCoupon saves the mutable fields of an existing coupon: name, redemption
// cap, validity window and metadata. The code, type, value, currency and app are
// fixed once created, because applied coupons are priced from them on every
// future invoice. The redemption count is never written here.
func (l *Ledger) UpdateCoupon(ctx context.Context, c *coupon.Coupon) error {
	existing, err := l.store.GetCouponByID(ctx, c.ID)
	if err != nil {
		return err
	}

	normaliseCoupon(c)
	if c.AppID != existing.AppID || c.Code != existing.Code || c.Type != existing.Type ||
		c.Percentage != existing.Percentage || c.Amount.Amount != existing.Amount.Amount ||
		!strings.EqualFold(c.Amount.Currency, existing.Amount.Currency) ||
		!strings.EqualFold(c.Currency, existing.Currency) {
		return fmt.Errorf("%w: a coupon's code, type, value, currency and app cannot change once it is created", ErrCouponInvalid)
	}
	if err := validateCouponShape(c); err != nil {
		return err
	}

	c.CreatedAt = existing.CreatedAt
	c.Touch()

	return l.store.UpdateCoupon(ctx, c)
}

// DeleteCoupon removes a coupon. Subscriptions it was applied to stop receiving
// its discount on their next invoice.
func (l *Ledger) DeleteCoupon(ctx context.Context, couponID id.CouponID) error {
	return l.store.DeleteCoupon(ctx, couponID)
}
