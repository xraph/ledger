package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"
)

// Registry manages all registered plugins and provides efficient dispatch.
// It uses type-cached discovery for O(1) dispatch performance.
type Registry struct {
	mu      sync.RWMutex
	plugins []Plugin
	logger  *slog.Logger

	// Type-cached plugin lists for efficient dispatch
	onInit                 []OnInit
	onShutdown             []OnShutdown
	onPlanCreated          []OnPlanCreated
	onPlanUpdated          []OnPlanUpdated
	onPlanArchived         []OnPlanArchived
	onSubscriptionCreated  []OnSubscriptionCreated
	onSubscriptionChanged  []OnSubscriptionChanged
	onSubscriptionCanceled []OnSubscriptionCanceled
	onSubscriptionExpired  []OnSubscriptionExpired
	onUsageIngested        []OnUsageIngested
	onUsageFlushed         []OnUsageFlushed
	onEntitlementChecked   []OnEntitlementChecked
	onQuotaExceeded        []OnQuotaExceeded
	onSoftLimitReached     []OnSoftLimitReached
	onInvoiceGenerated     []OnInvoiceGenerated
	onInvoiceFinalized     []OnInvoiceFinalized
	onInvoicePaid          []OnInvoicePaid
	onInvoiceFailed        []OnInvoiceFailed
	onInvoiceVoided        []OnInvoiceVoided
	onProviderSync         []OnProviderSync
	onWebhookReceived      []OnWebhookReceived
	paymentProviders       []PaymentProviderPlugin
	pricingStrategies      map[string]PricingStrategy
	usageAggregators       map[string]UsageAggregator
	taxCalculators         []TaxCalculator
	invoiceFormatters      map[string]InvoiceFormatter
	couponValidators       []CouponValidator
}

// NewRegistry creates a new plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		logger:            slog.Default(),
		pricingStrategies: make(map[string]PricingStrategy),
		usageAggregators:  make(map[string]UsageAggregator),
		invoiceFormatters: make(map[string]InvoiceFormatter),
	}
}

// WithLogger sets the logger for the registry.
func (r *Registry) WithLogger(logger *slog.Logger) *Registry {
	r.logger = logger
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
		"name", p.Name(),
		"interfaces", r.getImplementedInterfaces(p),
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
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnInit(ctx, ledger)
		}); err != nil {
			r.logger.Warn("plugin OnInit failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
	}
}

// EmitShutdown calls OnShutdown for all plugins that implement it.
func (r *Registry) EmitShutdown(ctx context.Context) {
	r.mu.RLock()
	plugins := r.onShutdown
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnShutdown(ctx)
		}); err != nil {
			r.logger.Warn("plugin OnShutdown failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
	}
}

// EmitPlanCreated emits a plan created event.
func (r *Registry) EmitPlanCreated(ctx context.Context, plan interface{}) {
	r.mu.RLock()
	plugins := r.onPlanCreated
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnPlanCreated(ctx, plan)
		}); err != nil {
			r.logger.Warn("plugin OnPlanCreated failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
	}
}

// EmitSubscriptionCreated emits a subscription created event.
func (r *Registry) EmitSubscriptionCreated(ctx context.Context, sub interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionCreated
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnSubscriptionCreated(ctx, sub)
		}); err != nil {
			r.logger.Warn("plugin OnSubscriptionCreated failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
	}
}

// EmitInvoiceGenerated emits an invoice generated event.
func (r *Registry) EmitInvoiceGenerated(ctx context.Context, inv interface{}) {
	r.mu.RLock()
	plugins := r.onInvoiceGenerated
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnInvoiceGenerated(ctx, inv)
		}); err != nil {
			r.logger.Warn("plugin OnInvoiceGenerated failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
	}
}

// EmitEntitlementChecked emits an entitlement checked event.
func (r *Registry) EmitEntitlementChecked(ctx context.Context, result interface{}) {
	r.mu.RLock()
	plugins := r.onEntitlementChecked
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnEntitlementChecked(ctx, result)
		}); err != nil {
			r.logger.Warn("plugin OnEntitlementChecked failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
	}
}

// EmitQuotaExceeded emits a quota exceeded event.
func (r *Registry) EmitQuotaExceeded(ctx context.Context, tenantID, featureKey string, used, limit int64) {
	r.mu.RLock()
	plugins := r.onQuotaExceeded
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnQuotaExceeded(ctx, tenantID, featureKey, used, limit)
		}); err != nil {
			r.logger.Warn("plugin OnQuotaExceeded failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
	}
}

// EmitSubscriptionCanceled emits a subscription canceled event.
func (r *Registry) EmitSubscriptionCanceled(ctx context.Context, sub interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionCanceled
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnSubscriptionCanceled(ctx, sub)
		}); err != nil {
			r.logger.Warn("plugin OnSubscriptionCanceled failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
	}
}

// EmitUsageFlushed emits a usage flushed event.
func (r *Registry) EmitUsageFlushed(ctx context.Context, count int, elapsed time.Duration) {
	r.mu.RLock()
	plugins := r.onUsageFlushed
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnUsageFlushed(ctx, count, elapsed)
		}); err != nil {
			r.logger.Warn("plugin OnUsageFlushed failed",
				"plugin", p.Name(),
				"error", err,
			)
		}
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

// GetPricingStrategy returns a pricing strategy by name.
func (r *Registry) GetPricingStrategy(name string) PricingStrategy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pricingStrategies[name]
}

// GetTaxCalculators returns all registered tax calculators.
func (r *Registry) GetTaxCalculators() []TaxCalculator {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]TaxCalculator, len(r.taxCalculators))
	copy(result, r.taxCalculators)
	return result
}

// callWithTimeout calls a plugin function with a timeout.
// Plugins should never block the billing pipeline.
func (r *Registry) callWithTimeout(ctx context.Context, pluginName string, fn func() error) error {
	done := make(chan error, 1)

	go func() {
		done <- fn()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		return fmt.Errorf("plugin timeout: %s", pluginName)
	case <-ctx.Done():
		return ctx.Err()
	}
}
