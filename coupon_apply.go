package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
)

// ApplyCoupon attaches a coupon to a subscription by code.
//
// Every check that can fail runs before any write, so a rejected coupon
// leaves no application row and no incremented redemption count. The
// application row and the redemption count are written together through
// l.store.RedeemCoupon, which the store commits as one unit: a review of
// the previous two-write version found both of its assumptions false. A
// coupon read as under its cap could still be over its cap by the time the
// write landed, because the check and the write were two separate steps
// with no lock between them - a barrier test redeemed a coupon capped at 1
// five times this way. And a row without a count turned out not to be
// recoverable either: a retry after a failure between the two writes saw
// the application row already there and returned ErrCouponAlreadyApplied,
// so the count never caught up. RedeemCoupon closes both gaps by enforcing
// the cap with a conditional increment inside the same unit that records
// the application, rather than a read followed by a write.
func (l *Ledger) ApplyCoupon(ctx context.Context, subID id.SubscriptionID, code string) (*coupon.Coupon, error) {
	sub, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, fmt.Errorf("ledger: resolve subscription: %w", err)
	}

	c, err := l.store.GetCoupon(ctx, code, sub.AppID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if c.ValidFrom != nil && now.Before(*c.ValidFrom) {
		return nil, ErrCouponNotStarted
	}
	if c.ValidUntil != nil && now.After(*c.ValidUntil) {
		return nil, ErrCouponExpired
	}

	// MaxRedemptions of zero means unlimited.
	if c.MaxRedemptions > 0 && c.TimesRedeemed >= c.MaxRedemptions {
		return nil, ErrCouponExhausted
	}

	// A coupon cannot raise a bill: a percentage outside 0..100 or a
	// negative amount would add money instead of discounting it, under a
	// line item still labelled "Discount". Reject the shape here, with the
	// other built-in checks, before any write and before plugin validators
	// are ever consulted.
	switch c.Type {
	case coupon.CouponTypePercentage:
		if c.Percentage < 0 || c.Percentage > 100 {
			return nil, fmt.Errorf("%w: percentage %d is out of range 0..100", ErrCouponInvalid, c.Percentage)
		}
	case coupon.CouponTypeAmount:
		if c.Amount.Amount < 0 {
			return nil, fmt.Errorf("%w: amount %v is negative", ErrCouponInvalid, c.Amount)
		}
	}

	// Currency must match the Money the discount will actually be computed
	// from, not just the coupon's Currency label: Money.Add and Subtract
	// panic when the two Money values' Currency fields differ, and a label
	// can lie about what an amount coupon's Money actually holds. Comparison
	// is case-insensitive: "USD" and "usd" name the same currency.
	if !sub.PlanID.IsNil() {
		p, planErr := l.store.GetPlan(ctx, sub.PlanID)
		if planErr != nil {
			return nil, fmt.Errorf("ledger: resolve plan for coupon currency check: %w", planErr)
		}

		if p.Currency != "" {
			planCurrency := strings.ToLower(p.Currency)

			switch c.Type {
			case coupon.CouponTypeAmount:
				// The Money value is what Subtract will actually operate
				// on. Fall back to the label only when the Money itself
				// carries no currency, and refuse when neither says
				// anything: that is not a currency to skip the check for.
				couponCurrency := c.Amount.Currency
				if couponCurrency == "" {
					couponCurrency = c.Currency
				}
				if couponCurrency == "" || strings.ToLower(couponCurrency) != planCurrency {
					return nil, fmt.Errorf("%w: coupon is in %q, plan bills in %q",
						ErrCouponInvalid, couponCurrency, p.Currency)
				}
			default:
				// No Money is involved before the discount is computed for
				// a percentage coupon, but a coupon explicitly labelled for
				// another currency is still refused.
				if c.Currency != "" && strings.ToLower(c.Currency) != planCurrency {
					return nil, fmt.Errorf("%w: coupon is in %q, plan bills in %q",
						ErrCouponInvalid, c.Currency, p.Currency)
				}
			}
		}
	}

	for _, v := range l.plugins.GetCouponValidators() {
		if vErr := v.ValidateCoupon(ctx, c, sub); vErr != nil {
			return nil, fmt.Errorf("ledger: coupon validator %q refused: %w", v.Name(), vErr)
		}
	}

	if err := l.store.RedeemCoupon(ctx, subID, c.ID); err != nil {
		return nil, err
	}

	// Re-read rather than incrementing c.TimesRedeemed locally: c was read
	// before RedeemCoupon ran, so its TimesRedeemed is now stale by exactly
	// the one redemption that just landed. Re-reading gets the count the
	// store actually recorded rather than assuming a local increment
	// matches it.
	updated, err := l.store.GetCouponByID(ctx, c.ID)
	if err != nil {
		// The write above already succeeded; a failure to re-read the coupon
		// back is not a failure to apply it. Return the coupon as resolved
		// before the increment - every field but TimesRedeemed is already
		// correct - rather than guessing at a count this call never
		// confirmed.
		return c, nil
	}

	return updated, nil
}

// ListAppliedCoupons returns the coupons attached to a subscription, oldest
// application first.
func (l *Ledger) ListAppliedCoupons(ctx context.Context, subID id.SubscriptionID) ([]*coupon.Coupon, error) {
	return l.store.ListAppliedCoupons(ctx, subID)
}
