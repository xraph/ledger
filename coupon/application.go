package coupon

import (
	"time"

	"github.com/xraph/ledger/id"
)

// Application records that a coupon was applied to a subscription.
//
// The pair (CouponID, SubscriptionID) is unique: a coupon applies to a
// subscription once. Redemption counting lives on Coupon.TimesRedeemed and
// is incremented separately, because a store that fails partway through
// must not leave a count that no application row explains.
type Application struct {
	ID             id.CouponApplicationID `json:"id"`
	CouponID       id.CouponID            `json:"coupon_id"`
	SubscriptionID id.SubscriptionID      `json:"subscription_id"`
	AppliedAt      time.Time              `json:"applied_at"`
}
