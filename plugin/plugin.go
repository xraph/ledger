// Package plugin provides an extensible plugin system for Ledger.
// Plugins can hook into various lifecycle events to extend functionality.
package plugin

import (
	"context"
	"time"

	"github.com/xraph/ledger/provider"
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
// Feature catalog lifecycle hooks
// ──────────────────────────────────────────────────

// OnFeatureCreated is called when a new catalog feature is created.
type OnFeatureCreated interface {
	Plugin
	OnFeatureCreated(ctx context.Context, feature interface{}) error
}

// OnFeatureUpdated is called when a catalog feature is updated.
type OnFeatureUpdated interface {
	Plugin
	OnFeatureUpdated(ctx context.Context, oldFeature, newFeature interface{}) error
}

// OnFeatureDeleted is called when a catalog feature is deleted.
type OnFeatureDeleted interface {
	Plugin
	OnFeatureDeleted(ctx context.Context, featureID string) error
}

// OnFeatureArchived is called when a catalog feature is archived.
type OnFeatureArchived interface {
	Plugin
	OnFeatureArchived(ctx context.Context, featureID string) error
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

// OnSubscriptionCanceled is called when a subscription stops: an immediate
// cancel, or the lifecycle clock enacting a scheduled one. A cancel dated
// later fires OnSubscriptionCancelScheduled when it is recorded.
type OnSubscriptionCanceled interface {
	Plugin
	OnSubscriptionCanceled(ctx context.Context, sub interface{}) error
}

// OnSubscriptionExpired is called when a subscription expires.
type OnSubscriptionExpired interface {
	Plugin
	OnSubscriptionExpired(ctx context.Context, sub interface{}) error
}

// OnSubscriptionCancelScheduled is called when a cancellation is recorded for
// a later date. The subscription keeps running until then, and
// OnSubscriptionCanceled fires when the lifecycle clock ends it.
type OnSubscriptionCancelScheduled interface {
	Plugin
	OnSubscriptionCancelScheduled(ctx context.Context, sub interface{}) error
}

// OnSubscriptionTrialEnded is called when the lifecycle clock ends a trial and
// the subscription becomes active.
type OnSubscriptionTrialEnded interface {
	Plugin
	OnSubscriptionTrialEnded(ctx context.Context, sub interface{}) error
}

// OnSubscriptionRenewed is called when the lifecycle clock moves a subscription
// into a new billing period. It fires once per move and receives a
// *subscription.Renewal: the subscription in its new period, and every period
// that ended in the move, oldest first, catch-up periods included. Ledger bills
// none of them; a plugin that invoices at rollover calls GenerateInvoice with
// ledger.ForPeriod for each. After a change to a plan's billing period the
// first rollover can list a period of the old cadence, which ForPeriod refuses.
type OnSubscriptionRenewed interface {
	Plugin
	OnSubscriptionRenewed(ctx context.Context, renewal interface{}) error
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

// OnInvoicePastDue is called when the lifecycle clock marks a pending invoice
// past due. The subscription's status does not change.
type OnInvoicePastDue interface {
	Plugin
	OnInvoicePastDue(ctx context.Context, inv interface{}) error
}

// ──────────────────────────────────────────────────
// Payment provider hooks
// ──────────────────────────────────────────────────

// PaymentProviderPlugin provides a payment provider implementation.
type PaymentProviderPlugin interface {
	Plugin
	Provider() provider.Provider
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

// PricingStrategy provides custom pricing calculation. A feature (or, failing
// that, its plan) selects a strategy by StrategyName under the metadata key
// "pricing_strategy". A name that is not registered falls back to the
// built-in tier pricing.
//
// Compute is called only for usage above the feature's included allowance,
// and for any positive seat count, where the allowance is zero. It is never
// called for usage the allowance covers.
//
// Each element of tiers is a plan.PriceTier (a value, not a pointer). The
// slice holds only the tiers belonging to the feature being priced, sorted
// into ladder order. usage is the total quantity for the period and included
// is the allowance. currency is the plan's currency, lowercased.
//
// Compute must return a non-negative types.Money in that currency (compared
// case-insensitively). A zero Money is the legal "free" answer. Returning
// nil, any other type, another currency or a negative amount fails invoice
// generation with an error naming the strategy.
type PricingStrategy interface {
	Plugin
	StrategyName() string
	Compute(tiers []interface{}, usage, included int64, currency string) interface{} // Returns Money
}

// ──────────────────────────────────────────────────
// Usage aggregators
// ──────────────────────────────────────────────────

// UsageAggregator provides custom usage aggregation logic. A metered feature
// selects an aggregator by AggregatorName under the metadata key
// "aggregator". A name that is not registered falls back to the store's own
// aggregation.
//
// Aggregate is called at most once per metered feature that selects the
// aggregator, per invoice. It is not called for a metered feature whose Limit
// is negative (an unlimited allowance can never bill), and it is not called
// for any feature once an earlier failure has stopped generation, including a
// feature whose price tiers fail validation.
//
// It receives the usage events for that tenant, app and feature that fall in
// the billed period, the half-open window [start, end): the period's own
// bounds once it has ended, and its start to now while it is still running.
// That is the same window the built-in sum reads, for the current period and
// for one named with ledger.ForPeriod alike. An event stamped exactly at the
// start is in the window, and one stamped exactly at the end belongs to the
// next period, so consecutive invoices never bill the same event twice or
// skip it.
//
// Each element of events is a *meter.UsageEvent. It is a pointer: asserting
// the value type meter.UsageEvent fails, and an aggregator that ignores
// elements it cannot assert will total zero usage, so nothing is billed and
// nothing reports an error. Assert the pointer type and return an error for
// anything else.
//
// The return value is the total usage quantity for the period, and it must
// be non-negative. An error or a negative total fails invoice generation
// with an error naming the aggregator.
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
