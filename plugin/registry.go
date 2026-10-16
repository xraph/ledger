package plugin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	log "github.com/xraph/go-utils/log"
	gometrics "github.com/xraph/go-utils/metrics"

	"github.com/xraph/ledger/internal/telemetry"
)

// HookFailuresMetric counts hook calls that failed, by hook and by reason. A
// Forge app scrapes it as forge_ledger_plugin_hook_failures_total.
const HookFailuresMetric = "ledger_plugin_hook_failures_total"

// defaultHookTimeout is how long the registry waits on one hook call.
const defaultHookTimeout = 5 * time.Second

// errHookTimeout is the error a hook call that outran hookTimeout reports.
var errHookTimeout = errors.New("plugin timeout")

// Registry manages all registered plugins and provides efficient dispatch.
// It uses type-cached discovery for O(1) dispatch performance.
type Registry struct {
	mu      sync.RWMutex
	plugins []Plugin
	logger  log.Logger

	// hookTimeout is how long one hook call may take; tests shorten it.
	hookTimeout time.Duration

	// hookFailures counts failed hook calls; nil records nothing.
	hookFailures *telemetry.CounterVec

	// Type-cached plugin lists for efficient dispatch
	onInit                        []OnInit
	onShutdown                    []OnShutdown
	onPlanCreated                 []OnPlanCreated
	onPlanUpdated                 []OnPlanUpdated
	onPlanArchived                []OnPlanArchived
	onFeatureCreated              []OnFeatureCreated
	onFeatureUpdated              []OnFeatureUpdated
	onFeatureDeleted              []OnFeatureDeleted
	onFeatureArchived             []OnFeatureArchived
	onSubscriptionCreated         []OnSubscriptionCreated
	onSubscriptionChanged         []OnSubscriptionChanged
	onSubscriptionCanceled        []OnSubscriptionCanceled
	onSubscriptionExpired         []OnSubscriptionExpired
	onSubscriptionCancelScheduled []OnSubscriptionCancelScheduled
	onSubscriptionTrialEnded      []OnSubscriptionTrialEnded
	onSubscriptionRenewed         []OnSubscriptionRenewed
	onInvoicePastDue              []OnInvoicePastDue
	onUsageIngested               []OnUsageIngested
	onUsageFlushed                []OnUsageFlushed
	onEntitlementChecked          []OnEntitlementChecked
	onQuotaExceeded               []OnQuotaExceeded
	onSoftLimitReached            []OnSoftLimitReached
	onInvoiceGenerated            []OnInvoiceGenerated
	onInvoiceFinalized            []OnInvoiceFinalized
	onInvoicePaid                 []OnInvoicePaid
	onInvoiceFailed               []OnInvoiceFailed
	onInvoiceVoided               []OnInvoiceVoided
	onProviderSync                []OnProviderSync
	onWebhookReceived             []OnWebhookReceived
	paymentProviders              []PaymentProviderPlugin
	pricingStrategies             map[string]PricingStrategy
	usageAggregators              map[string]UsageAggregator
	taxCalculators                []TaxCalculator
	invoiceFormatters             map[string]InvoiceFormatter
	couponValidators              []CouponValidator
}

// NewRegistry creates a new plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		logger:            log.NewNoopLogger(),
		hookTimeout:       defaultHookTimeout,
		pricingStrategies: make(map[string]PricingStrategy),
		usageAggregators:  make(map[string]UsageAggregator),
		invoiceFormatters: make(map[string]InvoiceFormatter),
	}
}

// WithLogger sets the logger for the registry.
func (r *Registry) WithLogger(logger log.Logger) *Registry {
	r.logger = logger
	return r
}

// WithMetrics counts failed hook calls on m as HookFailuresMetric, labeled
// with the hook (its interface name, such as OnInvoicePaid) and the reason:
// error, timeout or canceled. A nil m records nothing. Call it before the
// registry dispatches anything; ledger.New does.
func (r *Registry) WithMetrics(m gometrics.Metrics) *Registry {
	r.hookFailures = telemetry.NewCounterVec(m, HookFailuresMetric,
		"Plugin hook calls that failed, by hook and reason.",
		"hook", "reason")
	return r
}

// Register adds a plugin to the registry and caches its interfaces.
func (r *Registry) Register(p Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate
	for _, existing := range r.plugins {
		if existing.Name() == p.Name() {
			return fmt.Errorf("plugin: duplicate registration: %s", p.Name())
		}
	}

	r.plugins = append(r.plugins, p)

	// Type-switch to cache interfaces
	if v, ok := p.(OnInit); ok {
		r.onInit = append(r.onInit, v)
	}
	if v, ok := p.(OnShutdown); ok {
		r.onShutdown = append(r.onShutdown, v)
	}
	if v, ok := p.(OnPlanCreated); ok {
		r.onPlanCreated = append(r.onPlanCreated, v)
	}
	if v, ok := p.(OnPlanUpdated); ok {
		r.onPlanUpdated = append(r.onPlanUpdated, v)
	}
	if v, ok := p.(OnPlanArchived); ok {
		r.onPlanArchived = append(r.onPlanArchived, v)
	}
	if v, ok := p.(OnFeatureCreated); ok {
		r.onFeatureCreated = append(r.onFeatureCreated, v)
	}
	if v, ok := p.(OnFeatureUpdated); ok {
		r.onFeatureUpdated = append(r.onFeatureUpdated, v)
	}
	if v, ok := p.(OnFeatureDeleted); ok {
		r.onFeatureDeleted = append(r.onFeatureDeleted, v)
	}
	if v, ok := p.(OnFeatureArchived); ok {
		r.onFeatureArchived = append(r.onFeatureArchived, v)
	}
	if v, ok := p.(OnSubscriptionCreated); ok {
		r.onSubscriptionCreated = append(r.onSubscriptionCreated, v)
	}
	if v, ok := p.(OnSubscriptionChanged); ok {
		r.onSubscriptionChanged = append(r.onSubscriptionChanged, v)
	}
	if v, ok := p.(OnSubscriptionCanceled); ok {
		r.onSubscriptionCanceled = append(r.onSubscriptionCanceled, v)
	}
	if v, ok := p.(OnSubscriptionExpired); ok {
		r.onSubscriptionExpired = append(r.onSubscriptionExpired, v)
	}
	if v, ok := p.(OnSubscriptionCancelScheduled); ok {
		r.onSubscriptionCancelScheduled = append(r.onSubscriptionCancelScheduled, v)
	}
	if v, ok := p.(OnSubscriptionTrialEnded); ok {
		r.onSubscriptionTrialEnded = append(r.onSubscriptionTrialEnded, v)
	}
	if v, ok := p.(OnSubscriptionRenewed); ok {
		r.onSubscriptionRenewed = append(r.onSubscriptionRenewed, v)
	}
	if v, ok := p.(OnInvoicePastDue); ok {
		r.onInvoicePastDue = append(r.onInvoicePastDue, v)
	}
	if v, ok := p.(OnUsageIngested); ok {
		r.onUsageIngested = append(r.onUsageIngested, v)
	}
	if v, ok := p.(OnUsageFlushed); ok {
		r.onUsageFlushed = append(r.onUsageFlushed, v)
	}
	if v, ok := p.(OnEntitlementChecked); ok {
		r.onEntitlementChecked = append(r.onEntitlementChecked, v)
	}
	if v, ok := p.(OnQuotaExceeded); ok {
		r.onQuotaExceeded = append(r.onQuotaExceeded, v)
	}
	if v, ok := p.(OnSoftLimitReached); ok {
		r.onSoftLimitReached = append(r.onSoftLimitReached, v)
	}
	if v, ok := p.(OnInvoiceGenerated); ok {
		r.onInvoiceGenerated = append(r.onInvoiceGenerated, v)
	}
	if v, ok := p.(OnInvoiceFinalized); ok {
		r.onInvoiceFinalized = append(r.onInvoiceFinalized, v)
	}
	if v, ok := p.(OnInvoicePaid); ok {
		r.onInvoicePaid = append(r.onInvoicePaid, v)
	}
	if v, ok := p.(OnInvoiceFailed); ok {
		r.onInvoiceFailed = append(r.onInvoiceFailed, v)
	}
	if v, ok := p.(OnInvoiceVoided); ok {
		r.onInvoiceVoided = append(r.onInvoiceVoided, v)
	}
	if v, ok := p.(OnProviderSync); ok {
		r.onProviderSync = append(r.onProviderSync, v)
	}
	if v, ok := p.(OnWebhookReceived); ok {
		r.onWebhookReceived = append(r.onWebhookReceived, v)
	}
	if v, ok := p.(PaymentProviderPlugin); ok {
		r.paymentProviders = append(r.paymentProviders, v)
	}
	if v, ok := p.(PricingStrategy); ok {
		r.pricingStrategies[v.StrategyName()] = v
	}
	if v, ok := p.(UsageAggregator); ok {
		r.usageAggregators[v.AggregatorName()] = v
	}
	if v, ok := p.(TaxCalculator); ok {
		r.taxCalculators = append(r.taxCalculators, v)
	}
	if v, ok := p.(InvoiceFormatter); ok {
		r.invoiceFormatters[v.Format()] = v
	}
	if v, ok := p.(CouponValidator); ok {
		r.couponValidators = append(r.couponValidators, v)
	}

	r.logger.Info("plugin registered",
		log.String("name", p.Name()),
		log.Any("interfaces", r.getImplementedInterfaces(p)),
	)

	return nil
}

// getImplementedInterfaces returns a list of interfaces implemented by the plugin.
func (r *Registry) getImplementedInterfaces(p Plugin) []string {
	var interfaces []string
	v := reflect.TypeOf(p)

	// Check each interface
	checkInterface := func(iface reflect.Type, name string) {
		if v.Implements(iface) {
			interfaces = append(interfaces, name)
		}
	}

	// List all interfaces to check
	checkInterface(reflect.TypeOf((*OnInit)(nil)).Elem(), "OnInit")
	checkInterface(reflect.TypeOf((*OnShutdown)(nil)).Elem(), "OnShutdown")
	checkInterface(reflect.TypeOf((*OnPlanCreated)(nil)).Elem(), "OnPlanCreated")
	checkInterface(reflect.TypeOf((*OnFeatureCreated)(nil)).Elem(), "OnFeatureCreated")
	checkInterface(reflect.TypeOf((*OnSubscriptionCreated)(nil)).Elem(), "OnSubscriptionCreated")
	checkInterface(reflect.TypeOf((*OnInvoiceGenerated)(nil)).Elem(), "OnInvoiceGenerated")
	checkInterface(reflect.TypeOf((*OnEntitlementChecked)(nil)).Elem(), "OnEntitlementChecked")
	checkInterface(reflect.TypeOf((*PaymentProviderPlugin)(nil)).Elem(), "PaymentProvider")
	checkInterface(reflect.TypeOf((*PricingStrategy)(nil)).Elem(), "PricingStrategy")
	checkInterface(reflect.TypeOf((*TaxCalculator)(nil)).Elem(), "TaxCalculator")

	return interfaces
}

// Get returns a plugin by name.
func (r *Registry) Get(name string) Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.plugins {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// List returns all registered plugins.
func (r *Registry) List() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Plugin, len(r.plugins))
	copy(result, r.plugins)
	return result
}

// Count returns the number of registered plugins.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.plugins)
}

// ──────────────────────────────────────────────────
// Event emission methods
// ──────────────────────────────────────────────────

// EmitInit calls OnInit for all plugins that implement it.
func (r *Registry) EmitInit(ctx context.Context, ledger interface{}) {
	r.mu.RLock()
	plugins := r.onInit
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnInit", p.Name(), func() error {
			return p.OnInit(ctx, ledger)
		})
	}
}

// EmitShutdown calls OnShutdown for all plugins that implement it.
func (r *Registry) EmitShutdown(ctx context.Context) {
	r.mu.RLock()
	plugins := r.onShutdown
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnShutdown", p.Name(), func() error {
			return p.OnShutdown(ctx)
		})
	}
}

// EmitPlanCreated emits a plan created event.
func (r *Registry) EmitPlanCreated(ctx context.Context, plan interface{}) {
	r.mu.RLock()
	plugins := r.onPlanCreated
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnPlanCreated", p.Name(), func() error {
			return p.OnPlanCreated(ctx, plan)
		})
	}
}

// EmitFeatureCreated emits a feature created event.
func (r *Registry) EmitFeatureCreated(ctx context.Context, feature interface{}) {
	r.mu.RLock()
	plugins := r.onFeatureCreated
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnFeatureCreated", p.Name(), func() error {
			return p.OnFeatureCreated(ctx, feature)
		})
	}
}

// EmitFeatureUpdated emits a feature updated event.
func (r *Registry) EmitFeatureUpdated(ctx context.Context, oldFeature, newFeature interface{}) {
	r.mu.RLock()
	plugins := r.onFeatureUpdated
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnFeatureUpdated", p.Name(), func() error {
			return p.OnFeatureUpdated(ctx, oldFeature, newFeature)
		})
	}
}

// EmitFeatureDeleted emits a feature deleted event.
func (r *Registry) EmitFeatureDeleted(ctx context.Context, featureID string) {
	r.mu.RLock()
	plugins := r.onFeatureDeleted
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnFeatureDeleted", p.Name(), func() error {
			return p.OnFeatureDeleted(ctx, featureID)
		})
	}
}

// EmitFeatureArchived emits a feature archived event.
func (r *Registry) EmitFeatureArchived(ctx context.Context, featureID string) {
	r.mu.RLock()
	plugins := r.onFeatureArchived
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnFeatureArchived", p.Name(), func() error {
			return p.OnFeatureArchived(ctx, featureID)
		})
	}
}

// EmitSubscriptionCreated emits a subscription created event.
func (r *Registry) EmitSubscriptionCreated(ctx context.Context, sub interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionCreated
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnSubscriptionCreated", p.Name(), func() error {
			return p.OnSubscriptionCreated(ctx, sub)
		})
	}
}

// EmitInvoiceGenerated emits an invoice generated event.
func (r *Registry) EmitInvoiceGenerated(ctx context.Context, inv interface{}) {
	r.mu.RLock()
	plugins := r.onInvoiceGenerated
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnInvoiceGenerated", p.Name(), func() error {
			return p.OnInvoiceGenerated(ctx, inv)
		})
	}
}

// EmitEntitlementChecked emits an entitlement checked event.
func (r *Registry) EmitEntitlementChecked(ctx context.Context, result interface{}) {
	r.mu.RLock()
	plugins := r.onEntitlementChecked
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnEntitlementChecked", p.Name(), func() error {
			return p.OnEntitlementChecked(ctx, result)
		})
	}
}

// EmitQuotaExceeded emits a quota exceeded event.
func (r *Registry) EmitQuotaExceeded(ctx context.Context, tenantID, featureKey string, used, limit int64) {
	r.mu.RLock()
	plugins := r.onQuotaExceeded
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnQuotaExceeded", p.Name(), func() error {
			return p.OnQuotaExceeded(ctx, tenantID, featureKey, used, limit)
		})
	}
}

// EmitSubscriptionCanceled emits a subscription canceled event.
func (r *Registry) EmitSubscriptionCanceled(ctx context.Context, sub interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionCanceled
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnSubscriptionCanceled", p.Name(), func() error {
			return p.OnSubscriptionCanceled(ctx, sub)
		})
	}
}

// EmitUsageFlushed emits a usage flushed event.
func (r *Registry) EmitUsageFlushed(ctx context.Context, count int, elapsed time.Duration) {
	r.mu.RLock()
	plugins := r.onUsageFlushed
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnUsageFlushed", p.Name(), func() error {
			return p.OnUsageFlushed(ctx, count, elapsed)
		})
	}
}

// GetPaymentProviders returns all registered payment provider plugins.
func (r *Registry) GetPaymentProviders() []PaymentProviderPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]PaymentProviderPlugin, len(r.paymentProviders))
	copy(result, r.paymentProviders)
	return result
}

// GetInvoiceFormatter returns the formatter registered for format, or nil.
func (r *Registry) GetInvoiceFormatter(format string) InvoiceFormatter {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.invoiceFormatters[format]
}

// InvoiceFormats returns every registered formatter's format, sorted.
func (r *Registry) InvoiceFormats() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0, len(r.invoiceFormatters))
	for format := range r.invoiceFormatters {
		out = append(out, format)
	}
	sort.Strings(out)
	return out
}

// GetPricingStrategy returns a pricing strategy by name.
func (r *Registry) GetPricingStrategy(name string) PricingStrategy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pricingStrategies[name]
}

// GetUsageAggregator returns the usage aggregator registered under name, or nil.
func (r *Registry) GetUsageAggregator(name string) UsageAggregator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usageAggregators[name]
}

// GetTaxCalculators returns all registered tax calculators.
func (r *Registry) GetTaxCalculators() []TaxCalculator {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]TaxCalculator, len(r.taxCalculators))
	copy(result, r.taxCalculators)
	return result
}

// GetCouponValidators returns every registered coupon validator.
func (r *Registry) GetCouponValidators() []CouponValidator {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]CouponValidator, len(r.couponValidators))
	copy(out, r.couponValidators)

	return out
}

// GetPaymentProvider returns a payment provider plugin by provider name.
func (r *Registry) GetPaymentProvider(name string) PaymentProviderPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.paymentProviders {
		if p.Provider().Name() == name {
			return p
		}
	}
	return nil
}

// HasPaymentProviders returns true if any payment provider plugins are registered.
func (r *Registry) HasPaymentProviders() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.paymentProviders) > 0
}

// EmitPlanUpdated emits a plan updated event.
func (r *Registry) EmitPlanUpdated(ctx context.Context, oldPlan, newPlan interface{}) {
	r.mu.RLock()
	plugins := r.onPlanUpdated
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnPlanUpdated", p.Name(), func() error {
			return p.OnPlanUpdated(ctx, oldPlan, newPlan)
		})
	}
}

// EmitPlanArchived emits a plan archived event.
func (r *Registry) EmitPlanArchived(ctx context.Context, planID string) {
	r.mu.RLock()
	plugins := r.onPlanArchived
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnPlanArchived", p.Name(), func() error {
			return p.OnPlanArchived(ctx, planID)
		})
	}
}

// EmitSubscriptionChanged emits a subscription changed event.
func (r *Registry) EmitSubscriptionChanged(ctx context.Context, sub, oldPlan, newPlan interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionChanged
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnSubscriptionChanged", p.Name(), func() error {
			return p.OnSubscriptionChanged(ctx, sub, oldPlan, newPlan)
		})
	}
}

// EmitSubscriptionExpired emits a subscription expired event.
func (r *Registry) EmitSubscriptionExpired(ctx context.Context, sub interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionExpired
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnSubscriptionExpired", p.Name(), func() error {
			return p.OnSubscriptionExpired(ctx, sub)
		})
	}
}

// EmitSubscriptionCancelScheduled emits a cancellation recorded for a later date.
func (r *Registry) EmitSubscriptionCancelScheduled(ctx context.Context, sub interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionCancelScheduled
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnSubscriptionCancelScheduled", p.Name(), func() error {
			return p.OnSubscriptionCancelScheduled(ctx, sub)
		})
	}
}

// EmitSubscriptionTrialEnded emits a trial ended by the lifecycle clock.
func (r *Registry) EmitSubscriptionTrialEnded(ctx context.Context, sub interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionTrialEnded
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnSubscriptionTrialEnded", p.Name(), func() error {
			return p.OnSubscriptionTrialEnded(ctx, sub)
		})
	}
}

// EmitSubscriptionRenewed emits a subscription moved into a new period, as a
// *subscription.Renewal.
func (r *Registry) EmitSubscriptionRenewed(ctx context.Context, renewal interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionRenewed
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnSubscriptionRenewed", p.Name(), func() error {
			return p.OnSubscriptionRenewed(ctx, renewal)
		})
	}
}

// EmitInvoicePastDue emits an invoice the lifecycle clock marked past due.
func (r *Registry) EmitInvoicePastDue(ctx context.Context, inv interface{}) {
	r.mu.RLock()
	plugins := r.onInvoicePastDue
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnInvoicePastDue", p.Name(), func() error {
			return p.OnInvoicePastDue(ctx, inv)
		})
	}
}

// EmitSoftLimitReached emits a soft limit reached event.
func (r *Registry) EmitSoftLimitReached(ctx context.Context, tenantID, featureKey string, used, limit int64) {
	r.mu.RLock()
	plugins := r.onSoftLimitReached
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnSoftLimitReached", p.Name(), func() error {
			return p.OnSoftLimitReached(ctx, tenantID, featureKey, used, limit)
		})
	}
}

// EmitUsageIngested emits a usage ingested event.
func (r *Registry) EmitUsageIngested(ctx context.Context, events []interface{}) {
	r.mu.RLock()
	plugins := r.onUsageIngested
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnUsageIngested", p.Name(), func() error {
			return p.OnUsageIngested(ctx, events)
		})
	}
}

// EmitInvoiceFinalized emits an invoice finalized event.
func (r *Registry) EmitInvoiceFinalized(ctx context.Context, inv interface{}) {
	r.mu.RLock()
	plugins := r.onInvoiceFinalized
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnInvoiceFinalized", p.Name(), func() error {
			return p.OnInvoiceFinalized(ctx, inv)
		})
	}
}

// EmitInvoicePaid emits an invoice paid event.
func (r *Registry) EmitInvoicePaid(ctx context.Context, inv interface{}) {
	r.mu.RLock()
	plugins := r.onInvoicePaid
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnInvoicePaid", p.Name(), func() error {
			return p.OnInvoicePaid(ctx, inv)
		})
	}
}

// EmitInvoiceFailed emits an invoice payment failed event.
func (r *Registry) EmitInvoiceFailed(ctx context.Context, inv interface{}, payErr error) {
	r.mu.RLock()
	plugins := r.onInvoiceFailed
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnInvoiceFailed", p.Name(), func() error {
			return p.OnInvoiceFailed(ctx, inv, payErr)
		})
	}
}

// EmitInvoiceVoided emits an invoice voided event.
func (r *Registry) EmitInvoiceVoided(ctx context.Context, inv interface{}, reason string) {
	r.mu.RLock()
	plugins := r.onInvoiceVoided
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnInvoiceVoided", p.Name(), func() error {
			return p.OnInvoiceVoided(ctx, inv, reason)
		})
	}
}

// EmitProviderSync emits a provider sync event.
func (r *Registry) EmitProviderSync(ctx context.Context, providerName string, success bool, syncErr error) {
	r.mu.RLock()
	plugins := r.onProviderSync
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnProviderSync", p.Name(), func() error {
			return p.OnProviderSync(ctx, providerName, success, syncErr)
		})
	}
}

// EmitWebhookReceived emits a webhook received event.
func (r *Registry) EmitWebhookReceived(ctx context.Context, providerName string, payload []byte) {
	r.mu.RLock()
	plugins := r.onWebhookReceived
	r.mu.RUnlock()

	for _, p := range plugins {
		r.dispatch(ctx, "OnWebhookReceived", p.Name(), func() error {
			return p.OnWebhookReceived(ctx, providerName, payload)
		})
	}
}

// dispatch calls one plugin's hook through callWithTimeout. A failure is
// logged and counted, never returned: plugins must not block or fail the
// billing pipeline.
func (r *Registry) dispatch(ctx context.Context, hook, pluginName string, fn func() error) {
	reason, err := r.callWithTimeout(ctx, pluginName, fn)
	if err == nil {
		return
	}
	r.hookFailures.Inc(hook, reason)
	r.logger.Warn("plugin "+hook+" failed",
		log.String("plugin", pluginName),
		log.Error(err),
	)
}

// callWithTimeout calls a plugin function with a timeout, and says why a call
// failed: the hook returned an error, it outran the timeout, or ctx ended
// first.
// Plugins should never block the billing pipeline.
func (r *Registry) callWithTimeout(ctx context.Context, pluginName string, fn func() error) (reason string, err error) {
	done := make(chan error, 1)

	go func() {
		done <- fn()
	}()

	select {
	case hookErr := <-done:
		if hookErr != nil {
			return "error", hookErr
		}
		return "", nil
	case <-time.After(r.hookTimeout):
		return "timeout", fmt.Errorf("%w: %s", errHookTimeout, pluginName)
	case <-ctx.Done():
		return "canceled", ctx.Err()
	}
}
