// Package id provides TypeID-based identity types for all Ledger entities.
//
// Every entity in Ledger gets a type-prefixed, K-sortable, UUIDv7-based
// identifier. IDs are validated at parse time to ensure the prefix matches
// the expected type.
//
// Examples:
//
//	plan_01h2xcejqtf2nbrexx3vqjhp41
//	sub_01h2xcejqtf2nbrexx3vqjhp41
//	inv_01h455vb4pex5vsknk084sn02q
package id

import (
	"fmt"

	"go.jetify.com/typeid/v2"
)

// ──────────────────────────────────────────────────
// Prefix constants
// ──────────────────────────────────────────────────

const (
	// Core billing entities
	PrefixPlan         = "plan"  // Billing plan
	PrefixFeature      = "feat"  // Plan feature
	PrefixPrice        = "price" // Pricing configuration
	PrefixSubscription = "sub"   // Customer subscription

	// Usage tracking
	PrefixUsageEvent  = "uevt" // Usage event
	PrefixEntitlement = "ent"  // Entitlement check result

	// Billing documents
	PrefixInvoice  = "inv" // Invoice
	PrefixLineItem = "li"  // Invoice line item
	PrefixCoupon   = "cpn" // Discount coupon
	PrefixPayment  = "pay" // Payment record
)

// ──────────────────────────────────────────────────
// Type aliases for readability
// ──────────────────────────────────────────────────

// PlanID is a type-safe identifier for plans (prefix: "plan").
type PlanID = typeid.TypeID

// FeatureID is a type-safe identifier for features (prefix: "feat").
type FeatureID = typeid.TypeID

// PriceID is a type-safe identifier for prices (prefix: "price").
type PriceID = typeid.TypeID

// SubscriptionID is a type-safe identifier for subscriptions (prefix: "sub").
type SubscriptionID = typeid.TypeID

// UsageEventID is a type-safe identifier for usage events (prefix: "uevt").
type UsageEventID = typeid.TypeID

// EntitlementID is a type-safe identifier for entitlements (prefix: "ent").
type EntitlementID = typeid.TypeID

// InvoiceID is a type-safe identifier for invoices (prefix: "inv").
type InvoiceID = typeid.TypeID

// LineItemID is a type-safe identifier for line items (prefix: "li").
type LineItemID = typeid.TypeID

// CouponID is a type-safe identifier for coupons (prefix: "cpn").
type CouponID = typeid.TypeID

// PaymentID is a type-safe identifier for payments (prefix: "pay").
type PaymentID = typeid.TypeID

// AnyID is a TypeID that accepts any valid prefix.
type AnyID = typeid.TypeID

// ──────────────────────────────────────────────────
// Constructors
// ──────────────────────────────────────────────────

// NewPlanID returns a new random PlanID.
func NewPlanID() PlanID { return must(typeid.Generate(PrefixPlan)) }

// NewFeatureID returns a new random FeatureID.
func NewFeatureID() FeatureID { return must(typeid.Generate(PrefixFeature)) }

// NewPriceID returns a new random PriceID.
func NewPriceID() PriceID { return must(typeid.Generate(PrefixPrice)) }

// NewSubscriptionID returns a new random SubscriptionID.
func NewSubscriptionID() SubscriptionID { return must(typeid.Generate(PrefixSubscription)) }

// NewUsageEventID returns a new random UsageEventID.
func NewUsageEventID() UsageEventID { return must(typeid.Generate(PrefixUsageEvent)) }

// NewEntitlementID returns a new random EntitlementID.
func NewEntitlementID() EntitlementID { return must(typeid.Generate(PrefixEntitlement)) }

// NewInvoiceID returns a new random InvoiceID.
func NewInvoiceID() InvoiceID { return must(typeid.Generate(PrefixInvoice)) }

// NewLineItemID returns a new random LineItemID.
func NewLineItemID() LineItemID { return must(typeid.Generate(PrefixLineItem)) }

// NewCouponID returns a new random CouponID.
func NewCouponID() CouponID { return must(typeid.Generate(PrefixCoupon)) }

// NewPaymentID returns a new random PaymentID.
func NewPaymentID() PaymentID { return must(typeid.Generate(PrefixPayment)) }

// ──────────────────────────────────────────────────
// Parsing (validates prefix at parse time)
// ──────────────────────────────────────────────────

// ParsePlanID parses a string into a PlanID. Returns an error if the
// prefix is not "plan" or the suffix is invalid.
func ParsePlanID(s string) (PlanID, error) { return parseWithPrefix(PrefixPlan, s) }

// ParseFeatureID parses a string into a FeatureID.
func ParseFeatureID(s string) (FeatureID, error) { return parseWithPrefix(PrefixFeature, s) }

// ParsePriceID parses a string into a PriceID.
func ParsePriceID(s string) (PriceID, error) { return parseWithPrefix(PrefixPrice, s) }

// ParseSubscriptionID parses a string into a SubscriptionID.
func ParseSubscriptionID(s string) (SubscriptionID, error) {
	return parseWithPrefix(PrefixSubscription, s)
}

// ParseUsageEventID parses a string into a UsageEventID.
func ParseUsageEventID(s string) (UsageEventID, error) {
	return parseWithPrefix(PrefixUsageEvent, s)
}

// ParseEntitlementID parses a string into an EntitlementID.
func ParseEntitlementID(s string) (EntitlementID, error) {
	return parseWithPrefix(PrefixEntitlement, s)
}

// ParseInvoiceID parses a string into an InvoiceID.
func ParseInvoiceID(s string) (InvoiceID, error) { return parseWithPrefix(PrefixInvoice, s) }

// ParseLineItemID parses a string into a LineItemID.
func ParseLineItemID(s string) (LineItemID, error) { return parseWithPrefix(PrefixLineItem, s) }

// ParseCouponID parses a string into a CouponID.
func ParseCouponID(s string) (CouponID, error) { return parseWithPrefix(PrefixCoupon, s) }

// ParsePaymentID parses a string into a PaymentID.
func ParsePaymentID(s string) (PaymentID, error) { return parseWithPrefix(PrefixPayment, s) }

// ParseAny parses a string into an AnyID, accepting any valid prefix.
func ParseAny(s string) (AnyID, error) { return typeid.Parse(s) }

// ──────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────

// parseWithPrefix parses a TypeID and validates that its prefix matches expected.
func parseWithPrefix(expected, s string) (typeid.TypeID, error) {
	tid, err := typeid.Parse(s)
	if err != nil {
		return tid, err
	}
	if tid.Prefix() != expected {
		return tid, fmt.Errorf("id: expected prefix %q, got %q", expected, tid.Prefix())
	}
	return tid, nil
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
