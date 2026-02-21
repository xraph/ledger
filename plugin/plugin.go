// Package plugin provides an extensible plugin system for Ledger.
// Plugins can hook into various lifecycle events to extend functionality.
package plugin

import (
	"context"
	"time"
)

// Plugin is the base interface that all plugins must implement.
type Plugin interface {
	Name() string
}

// ──────────────────────────────────────────────────
// Lifecycle hooks
// ──────────────────────────────────────────────────

// OnInit is called when the plugin is initialized.
type OnInit interface {
	Plugin
	OnInit(ctx context.Context, l interface{}) error
}

// OnShutdown is called when the plugin is shutting down.
type OnShutdown interface {
	Plugin
	OnShutdown(ctx context.Context) error
}

// ──────────────────────────────────────────────────
// Plan lifecycle hooks
// ──────────────────────────────────────────────────

// OnPlanCreated is called when a new plan is created.
type OnPlanCreated interface {
	Plugin
	OnPlanCreated(ctx context.Context, plan interface{}) error
}

// OnPlanUpdated is called when a plan is updated.
type OnPlanUpdated interface {
	Plugin
	OnPlanUpdated(ctx context.Context, oldPlan, newPlan interface{}) error
}

// OnPlanArchived is called when a plan is archived.
type OnPlanArchived interface {
	Plugin
	OnPlanArchived(ctx context.Context, planID string) error
}

// ──────────────────────────────────────────────────
// Subscription lifecycle hooks
// ──────────────────────────────────────────────────

// OnSubscriptionCreated is called when a new subscription is created.
type OnSubscriptionCreated interface {
	Plugin
	OnSubscriptionCreated(ctx context.Context, sub interface{}) error
}

// OnSubscriptionChanged is called when a subscription changes plans.
type OnSubscriptionChanged interface {
	Plugin
	OnSubscriptionChanged(ctx context.Context, sub interface{}, oldPlan, newPlan interface{}) error
}

// OnSubscriptionCanceled is called when a subscription is canceled.
type OnSubscriptionCanceled interface {
	Plugin
	OnSubscriptionCanceled(ctx context.Context, sub interface{}) error
}

// OnSubscriptionExpired is called when a subscription expires.
type OnSubscriptionExpired interface {
	Plugin
	OnSubscriptionExpired(ctx context.Context, sub interface{}) error
}

// ──────────────────────────────────────────────────
// Usage/Metering hooks
// ──────────────────────────────────────────────────

// OnUsageIngested is called when usage events are ingested.
type OnUsageIngested interface {
	Plugin
	OnUsageIngested(ctx context.Context, events []interface{}) error
}

// OnUsageFlushed is called when usage events are flushed to the store.
type OnUsageFlushed interface {
	Plugin
	OnUsageFlushed(ctx context.Context, count int, elapsed time.Duration) error
}

// ──────────────────────────────────────────────────
// Entitlement hooks
// ──────────────────────────────────────────────────

// OnEntitlementChecked is called when an entitlement is checked.
type OnEntitlementChecked interface {
	Plugin
	OnEntitlementChecked(ctx context.Context, result interface{}) error
}

// OnQuotaExceeded is called when a quota is exceeded.
type OnQuotaExceeded interface {
	Plugin
	OnQuotaExceeded(ctx context.Context, tenantID, featureKey string, used, limit int64) error
}

// OnSoftLimitReached is called when a soft limit is reached.
type OnSoftLimitReached interface {
	Plugin
	OnSoftLimitReached(ctx context.Context, tenantID, featureKey string, used, limit int64) error
}

// ──────────────────────────────────────────────────
// Invoice lifecycle hooks
// ──────────────────────────────────────────────────

// OnInvoiceGenerated is called when an invoice is generated.
type OnInvoiceGenerated interface {
	Plugin
	OnInvoiceGenerated(ctx context.Context, inv interface{}) error
}

// OnInvoiceFinalized is called when an invoice is finalized.
type OnInvoiceFinalized interface {
	Plugin
	OnInvoiceFinalized(ctx context.Context, inv interface{}) error
}

// OnInvoicePaid is called when an invoice is paid.
type OnInvoicePaid interface {
	Plugin
	OnInvoicePaid(ctx context.Context, inv interface{}) error
}

// OnInvoiceFailed is called when an invoice payment fails.
type OnInvoiceFailed interface {
	Plugin
	OnInvoiceFailed(ctx context.Context, inv interface{}, err error) error
}

// OnInvoiceVoided is called when an invoice is voided.
type OnInvoiceVoided interface {
	Plugin
	OnInvoiceVoided(ctx context.Context, inv interface{}, reason string) error
}

// ──────────────────────────────────────────────────
// Payment provider hooks
// ──────────────────────────────────────────────────

// PaymentProviderPlugin provides a payment provider implementation.
type PaymentProviderPlugin interface {
	Plugin
	Provider() interface{} // Returns provider.Provider
}

// OnProviderSync is called when syncing with a payment provider.
type OnProviderSync interface {
	Plugin
	OnProviderSync(ctx context.Context, provider string, success bool, err error) error
}

// OnWebhookReceived is called when a webhook is received.
type OnWebhookReceived interface {
	Plugin
	OnWebhookReceived(ctx context.Context, provider string, payload []byte) error
}

// ──────────────────────────────────────────────────
// Pricing strategies
// ──────────────────────────────────────────────────

// PricingStrategy provides custom pricing calculation.
type PricingStrategy interface {
	Plugin
	StrategyName() string
	Compute(tiers []interface{}, usage, included int64, currency string) interface{} // Returns Money
}

// ──────────────────────────────────────────────────
// Usage aggregators
// ──────────────────────────────────────────────────

// UsageAggregator provides custom usage aggregation logic.
type UsageAggregator interface {
	Plugin
	AggregatorName() string
	Aggregate(ctx context.Context, events []interface{}) (int64, error)
}

// ──────────────────────────────────────────────────
// Tax calculators
// ──────────────────────────────────────────────────

// TaxCalculator calculates tax for invoices.
type TaxCalculator interface {
	Plugin
	CalculateTax(ctx context.Context, subtotal interface{}, tenantID string) (interface{}, error) // Returns Money
}

// ──────────────────────────────────────────────────
// Invoice formatters
// ──────────────────────────────────────────────────

// InvoiceFormatter formats invoices for export.
type InvoiceFormatter interface {
	Plugin
	Format() string                                                   // "pdf", "html", "csv", etc.
	Render(ctx context.Context, inv interface{}, w interface{}) error // w is io.Writer
}

// ──────────────────────────────────────────────────
// Coupon validators
// ──────────────────────────────────────────────────

// CouponValidator provides custom coupon validation logic.
type CouponValidator interface {
	Plugin
	ValidateCoupon(ctx context.Context, coupon interface{}, sub interface{}) error
}
