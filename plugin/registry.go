package plugin

import (
	"context"
	"fmt"
	log "github.com/xraph/go-utils/log"
	"reflect"
	"sync"
	"time"
)

// Registry manages all registered plugins and provides efficient dispatch.
// It uses type-cached discovery for O(1) dispatch performance.
type Registry struct {
	mu      sync.RWMutex
	plugins []Plugin
	logger  log.Logger

	// Type-cached plugin lists for efficient dispatch
	onInit                 []OnInit
	onShutdown             []OnShutdown
	onPlanCreated          []OnPlanCreated
	onPlanUpdated          []OnPlanUpdated
	onPlanArchived         []OnPlanArchived
	onFeatureCreated       []OnFeatureCreated
	onFeatureUpdated       []OnFeatureUpdated
	onFeatureDeleted       []OnFeatureDeleted
	onFeatureArchived      []OnFeatureArchived
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
		logger:            log.NewNoopLogger(),
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
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnInit(ctx, ledger)
		}); err != nil {
			r.logger.Warn("plugin OnInit failed",
				log.String("plugin", p.Name()),
				log.Error(err),
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
				log.String("plugin", p.Name()),
				log.Error(err),
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
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitFeatureCreated emits a feature created event.
func (r *Registry) EmitFeatureCreated(ctx context.Context, feature interface{}) {
	r.mu.RLock()
	plugins := r.onFeatureCreated
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnFeatureCreated(ctx, feature)
		}); err != nil {
			r.logger.Warn("plugin OnFeatureCreated failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitFeatureUpdated emits a feature updated event.
func (r *Registry) EmitFeatureUpdated(ctx context.Context, oldFeature, newFeature interface{}) {
	r.mu.RLock()
	plugins := r.onFeatureUpdated
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnFeatureUpdated(ctx, oldFeature, newFeature)
		}); err != nil {
			r.logger.Warn("plugin OnFeatureUpdated failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitFeatureDeleted emits a feature deleted event.
func (r *Registry) EmitFeatureDeleted(ctx context.Context, featureID string) {
	r.mu.RLock()
	plugins := r.onFeatureDeleted
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnFeatureDeleted(ctx, featureID)
		}); err != nil {
			r.logger.Warn("plugin OnFeatureDeleted failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitFeatureArchived emits a feature archived event.
func (r *Registry) EmitFeatureArchived(ctx context.Context, featureID string) {
	r.mu.RLock()
	plugins := r.onFeatureArchived
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnFeatureArchived(ctx, featureID)
		}); err != nil {
			r.logger.Warn("plugin OnFeatureArchived failed",
				log.String("plugin", p.Name()),
				log.Error(err),
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
				log.String("plugin", p.Name()),
				log.Error(err),
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
				log.String("plugin", p.Name()),
				log.Error(err),
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
				log.String("plugin", p.Name()),
				log.Error(err),
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
				log.String("plugin", p.Name()),
				log.Error(err),
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
				log.String("plugin", p.Name()),
				log.Error(err),
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
				log.String("plugin", p.Name()),
				log.Error(err),
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
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnPlanUpdated(ctx, oldPlan, newPlan)
		}); err != nil {
			r.logger.Warn("plugin OnPlanUpdated failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitPlanArchived emits a plan archived event.
func (r *Registry) EmitPlanArchived(ctx context.Context, planID string) {
	r.mu.RLock()
	plugins := r.onPlanArchived
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnPlanArchived(ctx, planID)
		}); err != nil {
			r.logger.Warn("plugin OnPlanArchived failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitSubscriptionChanged emits a subscription changed event.
func (r *Registry) EmitSubscriptionChanged(ctx context.Context, sub interface{}, oldPlan, newPlan interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionChanged
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnSubscriptionChanged(ctx, sub, oldPlan, newPlan)
		}); err != nil {
			r.logger.Warn("plugin OnSubscriptionChanged failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitSubscriptionExpired emits a subscription expired event.
func (r *Registry) EmitSubscriptionExpired(ctx context.Context, sub interface{}) {
	r.mu.RLock()
	plugins := r.onSubscriptionExpired
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnSubscriptionExpired(ctx, sub)
		}); err != nil {
			r.logger.Warn("plugin OnSubscriptionExpired failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitSoftLimitReached emits a soft limit reached event.
func (r *Registry) EmitSoftLimitReached(ctx context.Context, tenantID, featureKey string, used, limit int64) {
	r.mu.RLock()
	plugins := r.onSoftLimitReached
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnSoftLimitReached(ctx, tenantID, featureKey, used, limit)
		}); err != nil {
			r.logger.Warn("plugin OnSoftLimitReached failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitUsageIngested emits a usage ingested event.
func (r *Registry) EmitUsageIngested(ctx context.Context, events []interface{}) {
	r.mu.RLock()
	plugins := r.onUsageIngested
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnUsageIngested(ctx, events)
		}); err != nil {
			r.logger.Warn("plugin OnUsageIngested failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitInvoiceFinalized emits an invoice finalized event.
func (r *Registry) EmitInvoiceFinalized(ctx context.Context, inv interface{}) {
	r.mu.RLock()
	plugins := r.onInvoiceFinalized
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnInvoiceFinalized(ctx, inv)
		}); err != nil {
			r.logger.Warn("plugin OnInvoiceFinalized failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitInvoicePaid emits an invoice paid event.
func (r *Registry) EmitInvoicePaid(ctx context.Context, inv interface{}) {
	r.mu.RLock()
	plugins := r.onInvoicePaid
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnInvoicePaid(ctx, inv)
		}); err != nil {
			r.logger.Warn("plugin OnInvoicePaid failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitInvoiceFailed emits an invoice payment failed event.
func (r *Registry) EmitInvoiceFailed(ctx context.Context, inv interface{}, payErr error) {
	r.mu.RLock()
	plugins := r.onInvoiceFailed
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnInvoiceFailed(ctx, inv, payErr)
		}); err != nil {
			r.logger.Warn("plugin OnInvoiceFailed failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitInvoiceVoided emits an invoice voided event.
func (r *Registry) EmitInvoiceVoided(ctx context.Context, inv interface{}, reason string) {
	r.mu.RLock()
	plugins := r.onInvoiceVoided
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnInvoiceVoided(ctx, inv, reason)
		}); err != nil {
			r.logger.Warn("plugin OnInvoiceVoided failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitProviderSync emits a provider sync event.
func (r *Registry) EmitProviderSync(ctx context.Context, providerName string, success bool, syncErr error) {
	r.mu.RLock()
	plugins := r.onProviderSync
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnProviderSync(ctx, providerName, success, syncErr)
		}); err != nil {
			r.logger.Warn("plugin OnProviderSync failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
}

// EmitWebhookReceived emits a webhook received event.
func (r *Registry) EmitWebhookReceived(ctx context.Context, providerName string, payload []byte) {
	r.mu.RLock()
	plugins := r.onWebhookReceived
	r.mu.RUnlock()

	for _, p := range plugins {
		if err := r.callWithTimeout(ctx, p.Name(), func() error {
			return p.OnWebhookReceived(ctx, providerName, payload)
		}); err != nil {
			r.logger.Warn("plugin OnWebhookReceived failed",
				log.String("plugin", p.Name()),
				log.Error(err),
			)
		}
	}
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
