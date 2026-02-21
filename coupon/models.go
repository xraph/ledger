package coupon

import (
	"time"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

type Coupon struct {
	types.Entity
	ID             id.CouponID       `json:"id"`
	Code           string            `json:"code"`
	Name           string            `json:"name"`
	Type           CouponType        `json:"type"`
	Amount         types.Money       `json:"amount,omitempty"`
	Percentage     int               `json:"percentage,omitempty"`
	Currency       string            `json:"currency"`
	MaxRedemptions int               `json:"max_redemptions"`
	TimesRedeemed  int               `json:"times_redeemed"`
	ValidFrom      *time.Time        `json:"valid_from,omitempty"`
	ValidUntil     *time.Time        `json:"valid_until,omitempty"`
	AppID          string            `json:"app_id"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type CouponType string

const (
	CouponTypePercentage CouponType = "percentage"
	CouponTypeAmount     CouponType = "amount"
)
