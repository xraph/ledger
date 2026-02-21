// Package observability provides a metrics extension for Ledger that records
// lifecycle event counts via go-utils MetricFactory.
package observability

import (
	"context"
	"time"

	"github.com/xraph/ledger/plugin"
)

// Ensure MetricsExtension implements required interfaces.
var (
	_ plugin.Plugin                 = (*MetricsExtension)(nil)
	_ plugin.OnInit                 = (*MetricsExtension)(nil)
	_ plugin.OnPlanCreated          = (*MetricsExtension)(nil)
	_ plugin.OnPlanUpdated          = (*MetricsExtension)(nil)
	_ plugin.OnPlanArchived         = (*MetricsExtension)(nil)
	_ plugin.OnSubscriptionCreated  = (*MetricsExtension)(nil)
	_ plugin.OnSubscriptionChanged  = (*MetricsExtension)(nil)
	_ plugin.OnSubscriptionCanceled = (*MetricsExtension)(nil)
	_ plugin.OnSubscriptionExpired  = (*MetricsExtension)(nil)
	_ plugin.OnUsageIngested        = (*MetricsExtension)(nil)
	_ plugin.OnUsageFlushed         = (*MetricsExtension)(nil)
	_ plugin.OnEntitlementChecked   = (*MetricsExtension)(nil)
	_ plugin.OnQuotaExceeded        = (*MetricsExtension)(nil)
	_ plugin.OnInvoiceGenerated     = (*MetricsExtension)(nil)
	_ plugin.OnInvoiceFinalized     = (*MetricsExtension)(nil)
	_ plugin.OnInvoicePaid          = (*MetricsExtension)(nil)
	_ plugin.OnProviderSync         = (*MetricsExtension)(nil)
)

// Counter interface for metric counters.
type Counter interface {
	Inc()
	Add(float64)
}

// Histogram interface for metric histograms.
type Histogram interface {
	Observe(float64)
}

// MetricFactory creates metrics.
type MetricFactory interface {
	Counter(name string) Counter
	Histogram(name string) Histogram
}

// MetricsExtension records system-wide lifecycle metrics.
// Register it as a Ledger plugin to automatically track billing metrics.
type MetricsExtension struct {
	factory MetricFactory

	// Plan metrics
	PlanCreated  Counter
	PlanUpdated  Counter
	PlanArchived Counter

	// Subscription metrics
	SubscriptionCreated    Counter
	SubscriptionUpgraded   Counter
	SubscriptionDowngraded Counter
	SubscriptionCanceled   Counter
	SubscriptionExpired    Counter

	// Usage metrics
	UsageEventsIngested Counter
	UsageBatchSize      Histogram
	UsageFlushLatency   Histogram

	// Entitlement metrics
	EntitlementChecks      Counter
	EntitlementCacheHits   Counter
	EntitlementCacheMisses Counter
	EntitlementDenied      Counter
	EntitlementLatency     Histogram

	// Invoice metrics
	InvoiceGenerated Counter
	InvoiceFinalized Counter
	InvoicePaid      Counter
	InvoiceVoided    Counter
	InvoiceTotal     Histogram

	// Provider metrics
	ProviderSyncSuccess Counter
	ProviderSyncFailure Counter
	WebhookReceived     Counter
	WebhookProcessed    Counter

	// Error metrics
	StoreErrors  Counter
	PluginErrors Counter
}

// NewMetricsExtension creates a MetricsExtension with the provided MetricFactory.
// Use app.Metrics() in forge extensions.
func NewMetricsExtension(factory MetricFactory) *MetricsExtension {
	return &MetricsExtension{
		factory: factory,

		// Plan metrics
		PlanCreated:  factory.Counter("ledger.plan.created"),
		PlanUpdated:  factory.Counter("ledger.plan.updated"),
		PlanArchived: factory.Counter("ledger.plan.archived"),

		// Subscription metrics
		SubscriptionCreated:    factory.Counter("ledger.subscription.created"),
		SubscriptionUpgraded:   factory.Counter("ledger.subscription.upgraded"),
		SubscriptionDowngraded: factory.Counter("ledger.subscription.downgraded"),
		SubscriptionCanceled:   factory.Counter("ledger.subscription.canceled"),
		SubscriptionExpired:    factory.Counter("ledger.subscription.expired"),

		// Usage metrics
		UsageEventsIngested: factory.Counter("ledger.usage.events.ingested"),
		UsageBatchSize:      factory.Histogram("ledger.usage.batch.size"),
		UsageFlushLatency:   factory.Histogram("ledger.usage.flush.latency_ms"),

		// Entitlement metrics
		EntitlementChecks:      factory.Counter("ledger.entitlement.checks"),
		EntitlementCacheHits:   factory.Counter("ledger.entitlement.cache.hits"),
		EntitlementCacheMisses: factory.Counter("ledger.entitlement.cache.misses"),
		EntitlementDenied:      factory.Counter("ledger.entitlement.denied"),
		EntitlementLatency:     factory.Histogram("ledger.entitlement.latency_ms"),

		// Invoice metrics
		InvoiceGenerated: factory.Counter("ledger.invoice.generated"),
		InvoiceFinalized: factory.Counter("ledger.invoice.finalized"),
		InvoicePaid:      factory.Counter("ledger.invoice.paid"),
		InvoiceVoided:    factory.Counter("ledger.invoice.voided"),
		InvoiceTotal:     factory.Histogram("ledger.invoice.total_amount"),

		// Provider metrics
		ProviderSyncSuccess: factory.Counter("ledger.provider.sync.success"),
		ProviderSyncFailure: factory.Counter("ledger.provider.sync.failure"),
		WebhookReceived:     factory.Counter("ledger.webhook.received"),
		WebhookProcessed:    factory.Counter("ledger.webhook.processed"),

		// Error metrics
		StoreErrors:  factory.Counter("ledger.store.errors"),
		PluginErrors: factory.Counter("ledger.plugin.errors"),
	}
}

// Name implements plugin.Plugin.
func (m *MetricsExtension) Name() string { return "observability-metrics" }

// OnInit implements plugin.OnInit.
func (m *MetricsExtension) OnInit(_ context.Context, _ interface{}) error {
	// No initialization needed
	return nil
}

// ──────────────────────────────────────────────────
// Plan lifecycle hooks
// ──────────────────────────────────────────────────

// OnPlanCreated implements plugin.OnPlanCreated.
func (m *MetricsExtension) OnPlanCreated(_ context.Context, _ interface{}) error {
	m.PlanCreated.Inc()
	return nil
}

// OnPlanUpdated implements plugin.OnPlanUpdated.
func (m *MetricsExtension) OnPlanUpdated(_ context.Context, _, _ interface{}) error {
	m.PlanUpdated.Inc()
	return nil
}

// OnPlanArchived implements plugin.OnPlanArchived.
func (m *MetricsExtension) OnPlanArchived(_ context.Context, _ string) error {
	m.PlanArchived.Inc()
	return nil
}

// ──────────────────────────────────────────────────
// Subscription lifecycle hooks
// ──────────────────────────────────────────────────

// OnSubscriptionCreated implements plugin.OnSubscriptionCreated.
func (m *MetricsExtension) OnSubscriptionCreated(_ context.Context, _ interface{}) error {
	m.SubscriptionCreated.Inc()
	return nil
}

// OnSubscriptionChanged implements plugin.OnSubscriptionChanged.
func (m *MetricsExtension) OnSubscriptionChanged(_ context.Context, _, _, _ interface{}) error {
	// Determine if upgrade or downgrade based on some criteria
	// For now, just increment upgraded counter
	m.SubscriptionUpgraded.Inc()
	return nil
}

// OnSubscriptionCanceled implements plugin.OnSubscriptionCanceled.
func (m *MetricsExtension) OnSubscriptionCanceled(_ context.Context, _ interface{}) error {
	m.SubscriptionCanceled.Inc()
	return nil
}

// OnSubscriptionExpired implements plugin.OnSubscriptionExpired.
func (m *MetricsExtension) OnSubscriptionExpired(_ context.Context, _ interface{}) error {
	m.SubscriptionExpired.Inc()
	return nil
}

// ──────────────────────────────────────────────────
// Usage lifecycle hooks
// ──────────────────────────────────────────────────

// OnUsageIngested implements plugin.OnUsageIngested.
func (m *MetricsExtension) OnUsageIngested(_ context.Context, events []interface{}) error {
	count := float64(len(events))
	m.UsageEventsIngested.Add(count)
	m.UsageBatchSize.Observe(count)
	return nil
}

// OnUsageFlushed implements plugin.OnUsageFlushed.
func (m *MetricsExtension) OnUsageFlushed(_ context.Context, _ int, elapsed time.Duration) error {
	m.UsageFlushLatency.Observe(float64(elapsed.Milliseconds()))
	return nil
}

// ──────────────────────────────────────────────────
// Entitlement lifecycle hooks
// ──────────────────────────────────────────────────

// OnEntitlementChecked implements plugin.OnEntitlementChecked.
func (m *MetricsExtension) OnEntitlementChecked(_ context.Context, _ interface{}) error {
	m.EntitlementChecks.Inc()
	// Would need to inspect result to determine cache hit/miss and denied
	return nil
}

// OnQuotaExceeded implements plugin.OnQuotaExceeded.
func (m *MetricsExtension) OnQuotaExceeded(_ context.Context, _, _ string, _, _ int64) error {
	m.EntitlementDenied.Inc()
	return nil
}

// ──────────────────────────────────────────────────
// Invoice lifecycle hooks
// ──────────────────────────────────────────────────

// OnInvoiceGenerated implements plugin.OnInvoiceGenerated.
func (m *MetricsExtension) OnInvoiceGenerated(_ context.Context, _ interface{}) error {
	m.InvoiceGenerated.Inc()
	return nil
}

// OnInvoiceFinalized implements plugin.OnInvoiceFinalized.
func (m *MetricsExtension) OnInvoiceFinalized(_ context.Context, _ interface{}) error {
	m.InvoiceFinalized.Inc()
	return nil
}

// OnInvoicePaid implements plugin.OnInvoicePaid.
func (m *MetricsExtension) OnInvoicePaid(_ context.Context, _ interface{}) error {
	m.InvoicePaid.Inc()
	return nil
}

// ──────────────────────────────────────────────────
// Provider lifecycle hooks
// ──────────────────────────────────────────────────

// OnProviderSync implements plugin.OnProviderSync.
func (m *MetricsExtension) OnProviderSync(_ context.Context, _ string, success bool, _ error) error {
	if success {
		m.ProviderSyncSuccess.Inc()
	} else {
		m.ProviderSyncFailure.Inc()
	}
	return nil
}
