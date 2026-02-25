// Package id defines TypeID-based identity types for all Ledger entities.
//
// Every entity in Ledger uses a single ID struct with a prefix that identifies
// the entity type. IDs are K-sortable (UUIDv7-based), globally unique,
// and URL-safe in the format "prefix_suffix".
package id

import (
	"database/sql/driver"
	"fmt"

	"go.jetify.com/typeid/v2"
)

// Prefix identifies the entity type encoded in a TypeID.
type Prefix string

// Prefix constants for all Ledger entity types.
const (
	PrefixPlan         Prefix = "plan"  // Billing plan
	PrefixFeature      Prefix = "feat"  // Plan feature
	PrefixPrice        Prefix = "price" // Pricing configuration
	PrefixSubscription Prefix = "sub"   // Customer subscription
	PrefixUsageEvent   Prefix = "uevt"  // Usage event
	PrefixEntitlement  Prefix = "ent"   // Entitlement check result
	PrefixInvoice      Prefix = "inv"   // Invoice
	PrefixLineItem     Prefix = "li"    // Invoice line item
	PrefixCoupon       Prefix = "cpn"   // Discount coupon
	PrefixPayment      Prefix = "pay"   // Payment record
)

// ID is the primary identifier type for all Ledger entities.
// It wraps a TypeID providing a prefix-qualified, globally unique,
// sortable, URL-safe identifier in the format "prefix_suffix".
//
//nolint:recvcheck // Value receivers for read-only methods, pointer receivers for UnmarshalText/Scan.
type ID struct {
	inner typeid.TypeID
	valid bool
}

// Nil is the zero-value ID.
var Nil ID

// New generates a new globally unique ID with the given prefix.
// It panics if prefix is not a valid TypeID prefix (programming error).
func New(prefix Prefix) ID {
	tid, err := typeid.Generate(string(prefix))
	if err != nil {
		panic(fmt.Sprintf("id: invalid prefix %q: %v", prefix, err))
	}

	return ID{inner: tid, valid: true}
}

// Parse parses a TypeID string (e.g., "plan_01h2xcejqtf2nbrexx3vqjhp41")
// into an ID. Returns an error if the string is not valid.
func Parse(s string) (ID, error) {
	if s == "" {
		return Nil, fmt.Errorf("id: parse %q: empty string", s)
	}

	tid, err := typeid.Parse(s)
	if err != nil {
		return Nil, fmt.Errorf("id: parse %q: %w", s, err)
	}

	return ID{inner: tid, valid: true}, nil
}

// ParseWithPrefix parses a TypeID string and validates that its prefix
// matches the expected value.
func ParseWithPrefix(s string, expected Prefix) (ID, error) {
	parsed, err := Parse(s)
	if err != nil {
		return Nil, err
	}

	if parsed.Prefix() != expected {
		return Nil, fmt.Errorf("id: expected prefix %q, got %q", expected, parsed.Prefix())
	}

	return parsed, nil
}

// MustParse is like Parse but panics on error. Use for hardcoded ID values.
func MustParse(s string) ID {
	parsed, err := Parse(s)
	if err != nil {
		panic(fmt.Sprintf("id: must parse %q: %v", s, err))
	}

	return parsed
}

// MustParseWithPrefix is like ParseWithPrefix but panics on error.
func MustParseWithPrefix(s string, expected Prefix) ID {
	parsed, err := ParseWithPrefix(s, expected)
	if err != nil {
		panic(fmt.Sprintf("id: must parse with prefix %q: %v", expected, err))
	}

	return parsed
}

// ──────────────────────────────────────────────────
// Type aliases for backward compatibility
// ──────────────────────────────────────────────────

// PlanID is a type-safe identifier for plans (prefix: "plan").
type PlanID = ID

// FeatureID is a type-safe identifier for features (prefix: "feat").
type FeatureID = ID

// PriceID is a type-safe identifier for prices (prefix: "price").
type PriceID = ID

// SubscriptionID is a type-safe identifier for subscriptions (prefix: "sub").
type SubscriptionID = ID

// UsageEventID is a type-safe identifier for usage events (prefix: "uevt").
type UsageEventID = ID

// EntitlementID is a type-safe identifier for entitlements (prefix: "ent").
type EntitlementID = ID

// InvoiceID is a type-safe identifier for invoices (prefix: "inv").
type InvoiceID = ID

// LineItemID is a type-safe identifier for line items (prefix: "li").
type LineItemID = ID

// CouponID is a type-safe identifier for coupons (prefix: "cpn").
type CouponID = ID

// PaymentID is a type-safe identifier for payments (prefix: "pay").
type PaymentID = ID

// AnyID is a type alias that accepts any valid prefix.
type AnyID = ID

// ──────────────────────────────────────────────────
// Convenience constructors
// ──────────────────────────────────────────────────

// NewPlanID generates a new unique plan ID.
func NewPlanID() ID { return New(PrefixPlan) }

// NewFeatureID generates a new unique feature ID.
func NewFeatureID() ID { return New(PrefixFeature) }

// NewPriceID generates a new unique price ID.
func NewPriceID() ID { return New(PrefixPrice) }

// NewSubscriptionID generates a new unique subscription ID.
func NewSubscriptionID() ID { return New(PrefixSubscription) }

// NewUsageEventID generates a new unique usage event ID.
func NewUsageEventID() ID { return New(PrefixUsageEvent) }

// NewEntitlementID generates a new unique entitlement ID.
func NewEntitlementID() ID { return New(PrefixEntitlement) }

// NewInvoiceID generates a new unique invoice ID.
func NewInvoiceID() ID { return New(PrefixInvoice) }

// NewLineItemID generates a new unique line item ID.
func NewLineItemID() ID { return New(PrefixLineItem) }

// NewCouponID generates a new unique coupon ID.
func NewCouponID() ID { return New(PrefixCoupon) }

// NewPaymentID generates a new unique payment ID.
func NewPaymentID() ID { return New(PrefixPayment) }

// ──────────────────────────────────────────────────
// Convenience parsers
// ──────────────────────────────────────────────────

// ParsePlanID parses a string and validates the "plan" prefix.
func ParsePlanID(s string) (ID, error) { return ParseWithPrefix(s, PrefixPlan) }

// ParseFeatureID parses a string and validates the "feat" prefix.
func ParseFeatureID(s string) (ID, error) { return ParseWithPrefix(s, PrefixFeature) }

// ParsePriceID parses a string and validates the "price" prefix.
func ParsePriceID(s string) (ID, error) { return ParseWithPrefix(s, PrefixPrice) }

// ParseSubscriptionID parses a string and validates the "sub" prefix.
func ParseSubscriptionID(s string) (ID, error) { return ParseWithPrefix(s, PrefixSubscription) }

// ParseUsageEventID parses a string and validates the "uevt" prefix.
func ParseUsageEventID(s string) (ID, error) { return ParseWithPrefix(s, PrefixUsageEvent) }

// ParseEntitlementID parses a string and validates the "ent" prefix.
func ParseEntitlementID(s string) (ID, error) { return ParseWithPrefix(s, PrefixEntitlement) }

// ParseInvoiceID parses a string and validates the "inv" prefix.
func ParseInvoiceID(s string) (ID, error) { return ParseWithPrefix(s, PrefixInvoice) }

// ParseLineItemID parses a string and validates the "li" prefix.
func ParseLineItemID(s string) (ID, error) { return ParseWithPrefix(s, PrefixLineItem) }

// ParseCouponID parses a string and validates the "cpn" prefix.
func ParseCouponID(s string) (ID, error) { return ParseWithPrefix(s, PrefixCoupon) }

// ParsePaymentID parses a string and validates the "pay" prefix.
func ParsePaymentID(s string) (ID, error) { return ParseWithPrefix(s, PrefixPayment) }

// ParseAny parses a string into an ID without type checking the prefix.
func ParseAny(s string) (ID, error) { return Parse(s) }

// ──────────────────────────────────────────────────
// ID methods
// ──────────────────────────────────────────────────

// String returns the full TypeID string representation (prefix_suffix).
// Returns an empty string for the Nil ID.
func (i ID) String() string {
	if !i.valid {
		return ""
	}

	return i.inner.String()
}

// Prefix returns the prefix component of this ID.
func (i ID) Prefix() Prefix {
	if !i.valid {
		return ""
	}

	return Prefix(i.inner.Prefix())
}

// IsNil reports whether this ID is the zero value.
func (i ID) IsNil() bool {
	return !i.valid
}

// MarshalText implements encoding.TextMarshaler.
func (i ID) MarshalText() ([]byte, error) {
	if !i.valid {
		return []byte{}, nil
	}

	return []byte(i.inner.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (i *ID) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		*i = Nil

		return nil
	}

	parsed, err := Parse(string(data))
	if err != nil {
		return err
	}

	*i = parsed

	return nil
}

// Value implements driver.Valuer for database storage.
// Returns nil for the Nil ID so that optional foreign key columns store NULL.
func (i ID) Value() (driver.Value, error) {
	if !i.valid {
		return nil, nil //nolint:nilnil // nil is the canonical NULL for driver.Valuer
	}

	return i.inner.String(), nil
}

// Scan implements sql.Scanner for database retrieval.
func (i *ID) Scan(src any) error {
	if src == nil {
		*i = Nil

		return nil
	}

	switch v := src.(type) {
	case string:
		if v == "" {
			*i = Nil

			return nil
		}

		return i.UnmarshalText([]byte(v))
	case []byte:
		if len(v) == 0 {
			*i = Nil

			return nil
		}

		return i.UnmarshalText(v)
	default:
		return fmt.Errorf("id: cannot scan %T into ID", src)
	}
}
