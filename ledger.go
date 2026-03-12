package ledger

import (
	"context"
	"fmt"
	log "github.com/xraph/go-utils/log"
	"sync"
	"time"

	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// Ledger is the main billing engine.
type Ledger struct {
	store   store.Store
	plugins *plugin.Registry
	logger  log.Logger

	// Background workers
	meterBuffer chan *meter.UsageEvent
	stopChan    chan struct{}
	wg          sync.WaitGroup

	// Configuration
	meterBatchSize      int
	meterFlushInterval  time.Duration
	entitlementCacheTTL time.Duration
}

// New creates a new Ledger instance.
func New(s store.Store, opts ...Option) *Ledger {
	l := &Ledger{
		store:               s,
		plugins:             plugin.NewRegistry(),
		logger:              log.NewNoopLogger(),
		meterBuffer:         make(chan *meter.UsageEvent, 10000),
		stopChan:            make(chan struct{}),
		meterBatchSize:      100,
		meterFlushInterval:  5 * time.Second,
		entitlementCacheTTL: 30 * time.Second,
	}

	for _, opt := range opts {
		opt(l)
	}

	return l
}

// Option configures a Ledger instance.
type Option func(*Ledger)

// WithLogger sets the logger.
func WithLogger(logger log.Logger) Option {
	return func(l *Ledger) {
		l.logger = logger
		l.plugins.WithLogger(logger)
	}
}

// WithPlugin registers a plugin.
func WithPlugin(p plugin.Plugin) Option {
	return func(l *Ledger) {
		_ = l.plugins.Register(p) //nolint:errcheck // best-effort plugin registration during init
	}
}

// WithMeterConfig configures metering parameters.
func WithMeterConfig(batchSize int, flushInterval time.Duration) Option {
	return func(l *Ledger) {
		l.meterBatchSize = batchSize
		l.meterFlushInterval = flushInterval
	}
}

// WithEntitlementCacheTTL sets the entitlement cache TTL.
func WithEntitlementCacheTTL(ttl time.Duration) Option {
	return func(l *Ledger) {
		l.entitlementCacheTTL = ttl
	}
}

// Start begins background workers.
func (l *Ledger) Start(ctx context.Context) error {
	// Migrate database
	if err := l.store.Migrate(ctx); err != nil {
		return err
	}

	// Initialize plugins
	l.plugins.EmitInit(ctx, l)

	// Start meter flush worker
	l.wg.Add(1)
	go l.meterFlushWorker(ctx)

	l.logger.Info("ledger started",
		log.Int("batch_size", l.meterBatchSize),
		log.Duration("flush_interval", l.meterFlushInterval),
		log.Duration("cache_ttl", l.entitlementCacheTTL),
	)

	return nil
}

// Health checks the health of the Ledger by pinging its store.
func (l *Ledger) Health(ctx context.Context) error {
	return l.store.Ping(ctx)
}

// Stop shuts down the Ledger.
func (l *Ledger) Stop() error {
	close(l.stopChan)
	l.wg.Wait()

	ctx := context.Background()
	l.plugins.EmitShutdown(ctx)

	return l.store.Close()
}

// ──────────────────────────────────────────────────
// Plan Management
// ──────────────────────────────────────────────────

// CreatePlan creates a new billing plan.
func (l *Ledger) CreatePlan(ctx context.Context, p *plan.Plan) error {
	if p.ID == (id.PlanID{}) {
		p.ID = id.NewPlanID()
	}
	p.Entity = types.NewEntity()

	if err := l.store.CreatePlan(ctx, p); err != nil {
		return err
	}

	l.plugins.EmitPlanCreated(ctx, p)
	return nil
}

// GetPlan retrieves a plan by ID.
func (l *Ledger) GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error) {
	return l.store.GetPlan(ctx, planID)
}

// GetPlanBySlug retrieves a plan by slug.
func (l *Ledger) GetPlanBySlug(ctx context.Context, slug, appID string) (*plan.Plan, error) {
	return l.store.GetPlanBySlug(ctx, slug, appID)
}

// ──────────────────────────────────────────────────
// Feature Catalog Management
// ──────────────────────────────────────────────────

// CreateFeature creates a new catalog feature.
func (l *Ledger) CreateFeature(ctx context.Context, f *feature.Feature) error {
	if f.ID == (id.FeatureID{}) {
		f.ID = id.NewFeatureID()
	}
	f.Entity = types.NewEntity()

	if err := l.store.CreateFeature(ctx, f); err != nil {
		return err
	}

	l.plugins.EmitFeatureCreated(ctx, f)
	return nil
}

// GetFeature retrieves a catalog feature by ID.
func (l *Ledger) GetFeature(ctx context.Context, featureID id.FeatureID) (*feature.Feature, error) {
	return l.store.GetFeature(ctx, featureID)
}

// GetFeatureByKey retrieves a catalog feature by key and app scope.
func (l *Ledger) GetFeatureByKey(ctx context.Context, key string, appID string) (*feature.Feature, error) {
	return l.store.GetFeatureByKey(ctx, key, appID)
}

// ListFeatures lists catalog features for an app.
func (l *Ledger) ListFeatures(ctx context.Context, appID string, opts feature.ListOpts) ([]*feature.Feature, error) {
	return l.store.ListFeatures(ctx, appID, opts)
}

// ListGlobalFeatures lists catalog features with no app scope.
func (l *Ledger) ListGlobalFeatures(ctx context.Context, opts feature.ListOpts) ([]*feature.Feature, error) {
	return l.store.ListGlobalFeatures(ctx, opts)
}

// UpdateFeature updates a catalog feature.
func (l *Ledger) UpdateFeature(ctx context.Context, f *feature.Feature) error {
	old, err := l.store.GetFeature(ctx, f.ID)
	if err != nil {
		return err
	}

	if err := l.store.UpdateFeature(ctx, f); err != nil {
		return err
	}

	l.plugins.EmitFeatureUpdated(ctx, old, f)
	return nil
}

// DeleteFeature deletes a catalog feature.
func (l *Ledger) DeleteFeature(ctx context.Context, featureID id.FeatureID) error {
	if err := l.store.DeleteFeature(ctx, featureID); err != nil {
		return err
	}

	l.plugins.EmitFeatureDeleted(ctx, featureID.String())
	return nil
}

// ArchiveFeature archives a catalog feature.
func (l *Ledger) ArchiveFeature(ctx context.Context, featureID id.FeatureID) error {
	if err := l.store.ArchiveFeature(ctx, featureID); err != nil {
		return err
	}

	l.plugins.EmitFeatureArchived(ctx, featureID.String())
	return nil
}

// ──────────────────────────────────────────────────
// Subscription Management
// ──────────────────────────────────────────────────

// CreateSubscription creates a new subscription.
func (l *Ledger) CreateSubscription(ctx context.Context, sub *subscription.Subscription) error {
	if sub.ID == (id.SubscriptionID{}) {
		sub.ID = id.NewSubscriptionID()
	}
	sub.Entity = types.NewEntity()

	// Set initial period
	if sub.CurrentPeriodStart.IsZero() {
		sub.CurrentPeriodStart = time.Now()
		sub.CurrentPeriodEnd = time.Now().AddDate(0, 1, 0) // Monthly by default
	}

	if err := l.store.CreateSubscription(ctx, sub); err != nil {
		return err
	}

	// Invalidate entitlement cache for tenant
	_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation

	l.plugins.EmitSubscriptionCreated(ctx, sub)
	return nil
}

// GetSubscription retrieves a subscription by ID.
func (l *Ledger) GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	return l.store.GetSubscription(ctx, subID)
}

// GetActiveSubscription retrieves the active subscription for a tenant.
func (l *Ledger) GetActiveSubscription(ctx context.Context, tenantID, appID string) (*subscription.Subscription, error) {
	return l.store.GetActiveSubscription(ctx, tenantID, appID)
}

// CancelSubscription cancels a subscription.
func (l *Ledger) CancelSubscription(ctx context.Context, subID id.SubscriptionID, immediately bool) error {
	sub, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return err
	}

	cancelAt := sub.CurrentPeriodEnd
	if immediately {
		cancelAt = time.Now()
	}

	if err := l.store.CancelSubscription(ctx, subID, cancelAt); err != nil {
		return err
	}

	// Invalidate entitlement cache
	_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation

	l.plugins.EmitSubscriptionCanceled(ctx, sub)
	return nil
}

// ──────────────────────────────────────────────────
// Usage Metering
// ──────────────────────────────────────────────────

// Meter records a usage event (non-blocking).
func (l *Ledger) Meter(ctx context.Context, featureKey string, quantity int64) error {
	// Extract tenant and app from context
	tenantID := extractTenantID(ctx)
	appID := extractAppID(ctx)

	if tenantID == "" || appID == "" {
		return ErrInvalidInput
	}

	event := &meter.UsageEvent{
		ID:         id.NewUsageEventID(),
		TenantID:   tenantID,
		AppID:      appID,
		FeatureKey: featureKey,
		Quantity:   quantity,
		Timestamp:  time.Now(),
	}

	select {
	case l.meterBuffer <- event:
		return nil
	default:
		return ErrMeterBufferFull
	}
}

// meterFlushWorker flushes usage events to the store.
func (l *Ledger) meterFlushWorker(ctx context.Context) {
	defer l.wg.Done()

	batch := make([]*meter.UsageEvent, 0, l.meterBatchSize)
	ticker := time.NewTicker(l.meterFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopChan:
			// Final flush
			if len(batch) > 0 {
				l.flushMeterBatch(ctx, batch)
			}
			return

		case event := <-l.meterBuffer:
			batch = append(batch, event)
			if len(batch) >= l.meterBatchSize {
				l.flushMeterBatch(ctx, batch)
				batch = make([]*meter.UsageEvent, 0, l.meterBatchSize)
			}

		case <-ticker.C:
			if len(batch) > 0 {
				l.flushMeterBatch(ctx, batch)
				batch = make([]*meter.UsageEvent, 0, l.meterBatchSize)
			}
		}
	}
}

func (l *Ledger) flushMeterBatch(ctx context.Context, batch []*meter.UsageEvent) {
	start := time.Now()

	if err := l.store.IngestBatch(ctx, batch); err != nil {
		l.logger.Error("failed to flush meter batch",
			log.Error(err),
			log.Int("batch_size", len(batch)),
		)
		return
	}

	elapsed := time.Since(start)
	l.plugins.EmitUsageFlushed(ctx, len(batch), elapsed)

	l.logger.Debug("flushed meter batch",
		log.Int("batch_size", len(batch)),
		log.Int64("elapsed_ms", elapsed.Milliseconds()),
	)
}

// ──────────────────────────────────────────────────
// Entitlements
// ──────────────────────────────────────────────────

// Entitled checks if the current tenant can use a feature.
func (l *Ledger) Entitled(ctx context.Context, featureKey string) (*entitlement.Result, error) {
	tenantID := extractTenantID(ctx)
	appID := extractAppID(ctx)

	if tenantID == "" || appID == "" {
		return &entitlement.Result{
			Allowed: false,
			Feature: featureKey,
			Reason:  "missing tenant or app context",
		}, nil
	}

	// Check cache first
	if cached, err := l.store.GetCached(ctx, tenantID, appID, featureKey); err == nil {
		return cached, nil
	}

	// Get active subscription
	sub, err := l.store.GetActiveSubscription(ctx, tenantID, appID)
	if err != nil {
		return &entitlement.Result{
			Allowed: false,
			Feature: featureKey,
			Reason:  "no active subscription",
		}, nil
	}

	// Get plan
	p, err := l.store.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return &entitlement.Result{
			Allowed: false,
			Feature: featureKey,
			Reason:  "plan not found",
		}, nil
	}

	// Find feature
	feature := p.FindFeature(featureKey)
	if feature == nil {
		return &entitlement.Result{
			Allowed: false,
			Feature: featureKey,
			Reason:  "feature not in plan",
		}, nil
	}

	// Boolean feature
	if feature.Type == plan.FeatureBoolean {
		result := &entitlement.Result{
			Allowed: feature.Limit > 0,
			Feature: featureKey,
			Limit:   feature.Limit,
		}
		_ = l.store.SetCached(ctx, tenantID, appID, featureKey, result, l.entitlementCacheTTL) //nolint:errcheck // best-effort cache set
		return result, nil
	}

	// Metered/seat feature
	used, err := l.store.Aggregate(ctx, tenantID, appID, featureKey, feature.Period)
	if err != nil {
		return nil, err
	}

	result := &entitlement.Result{
		Feature:   featureKey,
		Used:      used,
		Limit:     feature.Limit,
		Remaining: max(0, feature.Limit-used),
		SoftLimit: feature.SoftLimit,
	}

	switch {
	case feature.Limit == -1:
		result.Allowed = true
		result.Remaining = -1
	case used < feature.Limit:
		result.Allowed = true
	case feature.SoftLimit:
		result.Allowed = true
		result.Reason = "over soft limit"
	default:
		result.Allowed = false
		result.Reason = "quota exceeded"
		l.plugins.EmitQuotaExceeded(ctx, tenantID, featureKey, used, feature.Limit)
	}

	_ = l.store.SetCached(ctx, tenantID, appID, featureKey, result, l.entitlementCacheTTL) //nolint:errcheck // best-effort cache set
	l.plugins.EmitEntitlementChecked(ctx, result)

	return result, nil
}

// Remaining returns the remaining quota for a feature.
func (l *Ledger) Remaining(ctx context.Context, featureKey string) (int64, error) {
	result, err := l.Entitled(ctx, featureKey)
	if err != nil {
		return 0, err
	}
	return result.Remaining, nil
}

// ──────────────────────────────────────────────────
// Invoice Generation
// ──────────────────────────────────────────────────

// GenerateInvoice generates an invoice for a subscription period.
func (l *Ledger) GenerateInvoice(ctx context.Context, subID id.SubscriptionID) (*invoice.Invoice, error) {
	sub, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}

	p, err := l.store.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return nil, err
	}

	inv := &invoice.Invoice{
		Entity:         types.NewEntity(),
		ID:             id.NewInvoiceID(),
		TenantID:       sub.TenantID,
		SubscriptionID: sub.ID,
		Status:         invoice.StatusDraft,
		Currency:       p.Currency,
		Subtotal:       types.Zero(p.Currency),
		TaxAmount:      types.Zero(p.Currency),
		DiscountAmount: types.Zero(p.Currency),
		Total:          types.Zero(p.Currency),
		PeriodStart:    sub.CurrentPeriodStart,
		PeriodEnd:      sub.CurrentPeriodEnd,
		AppID:          sub.AppID,
		LineItems:      []invoice.LineItem{},
	}

	// Add base subscription fee
	if p.Pricing != nil && p.Pricing.BaseAmount.IsPositive() {
		inv.LineItems = append(inv.LineItems, invoice.LineItem{
			ID:          id.NewLineItemID(),
			InvoiceID:   inv.ID,
			Description: "Base subscription fee",
			Quantity:    1,
			UnitAmount:  p.Pricing.BaseAmount,
			Amount:      p.Pricing.BaseAmount,
			Type:        invoice.LineItemBase,
		})
		inv.Subtotal = inv.Subtotal.Add(p.Pricing.BaseAmount)
	}

	// Add metered usage charges
	for _, feature := range p.Features {
		if feature.Type == plan.FeatureMetered {
			used, err := l.store.Aggregate(ctx, sub.TenantID, sub.AppID, feature.Key, feature.Period)
			if err != nil {
				return nil, fmt.Errorf("aggregate usage for feature %q: %w", feature.Key, err)
			}
			if used > feature.Limit && feature.Limit > 0 {
				overage := used - feature.Limit
				// Would calculate overage charges based on pricing tiers
				// For now, just note the overage
				inv.LineItems = append(inv.LineItems, invoice.LineItem{
					ID:          id.NewLineItemID(),
					InvoiceID:   inv.ID,
					FeatureKey:  feature.Key,
					Description: feature.Name + " overage",
					Quantity:    overage,
					UnitAmount:  types.Zero(p.Currency),
					Amount:      types.Zero(p.Currency),
					Type:        invoice.LineItemOverage,
				})
			}
		}
	}

	// Calculate total
	inv.Total = inv.Subtotal.Add(inv.TaxAmount).Subtract(inv.DiscountAmount)

	// Save invoice
	if err := l.store.CreateInvoice(ctx, inv); err != nil {
		return nil, err
	}

	l.plugins.EmitInvoiceGenerated(ctx, inv)
	return inv, nil
}

// ──────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────

func extractTenantID(ctx context.Context) string {
	// Would extract from context (e.g., from Forge scope)
	// For now, check context value
	if v := ctx.Value("tenant_id"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func extractAppID(ctx context.Context) string {
	// Would extract from context (e.g., from Forge scope)
	// For now, check context value
	if v := ctx.Value("app_id"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ──────────────────────────────────────────────────
// Provider Sync & Payment Methods
// ──────────────────────────────────────────────────

// HasProviders returns true if any payment provider plugins are registered.
func (l *Ledger) HasProviders() bool {
	return l.plugins.HasPaymentProviders()
}

// getProvider resolves a payment provider by name. If name is empty, returns
// the first registered provider. Returns ErrProviderNotConfigured when no
// providers are registered at all.
func (l *Ledger) getProvider(name string) (provider.Provider, error) {
	if !l.plugins.HasPaymentProviders() {
		return nil, ErrProviderNotConfigured
	}

	if name != "" {
		pp := l.plugins.GetPaymentProvider(name)
		if pp == nil {
			return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
		}
		return pp.Provider(), nil
	}

	// Default to first registered provider
	providers := l.plugins.GetPaymentProviders()
	if len(providers) == 0 {
		return nil, ErrProviderNotConfigured
	}
	return providers[0].Provider(), nil
}

// ListPaymentMethods lists payment methods from the provider for a tenant.
// Payment methods are always fetched live and never stored locally.
func (l *Ledger) ListPaymentMethods(ctx context.Context, tenantID string) ([]provider.PaymentMethod, error) {
	prov, err := l.getProvider("")
	if err != nil {
		// No providers configured — return empty list (dev mode)
		if err == ErrProviderNotConfigured {
			return nil, nil
		}
		return nil, err
	}

	return prov.ListPaymentMethods(ctx, tenantID)
}

// SyncPlanToProvider pushes a local plan to the payment provider.
func (l *Ledger) SyncPlanToProvider(ctx context.Context, planID id.PlanID) (*provider.SyncResult, error) {
	p, err := l.store.GetPlan(ctx, planID)
	if err != nil {
		return nil, err
	}

	prov, err := l.getProvider(p.ProviderName)
	if err != nil {
		return nil, err
	}

	providerID, syncErr := prov.SyncPlan(ctx, p)
	result := &provider.SyncResult{
		ProviderName: prov.Name(),
		ProviderID:   providerID,
		EntityType:   "plan",
		EntityID:     planID.String(),
		Direction:    "push",
		Success:      syncErr == nil,
	}
	if syncErr != nil {
		result.Error = syncErr.Error()
	}

	// Update local entity with provider tracking
	if syncErr == nil {
		p.ProviderID = providerID
		p.ProviderName = prov.Name()
		_ = l.store.UpdatePlan(ctx, p) //nolint:errcheck // best-effort update
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), syncErr == nil, syncErr)
	return result, syncErr
}

// SyncFeatureToProvider pushes a local feature to the payment provider.
func (l *Ledger) SyncFeatureToProvider(ctx context.Context, featureID id.FeatureID) (*provider.SyncResult, error) {
	f, err := l.store.GetFeature(ctx, featureID)
	if err != nil {
		return nil, err
	}

	prov, err := l.getProvider(f.ProviderName)
	if err != nil {
		return nil, err
	}

	providerID, syncErr := prov.SyncFeature(ctx, f)
	result := &provider.SyncResult{
		ProviderName: prov.Name(),
		ProviderID:   providerID,
		EntityType:   "feature",
		EntityID:     featureID.String(),
		Direction:    "push",
		Success:      syncErr == nil,
	}
	if syncErr != nil {
		result.Error = syncErr.Error()
	}

	if syncErr == nil {
		f.ProviderID = providerID
		f.ProviderName = prov.Name()
		_ = l.store.UpdateFeature(ctx, f) //nolint:errcheck // best-effort update
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), syncErr == nil, syncErr)
	return result, syncErr
}

// SyncSubscriptionToProvider pushes a local subscription to the payment provider.
func (l *Ledger) SyncSubscriptionToProvider(ctx context.Context, subID id.SubscriptionID) (*provider.SyncResult, error) {
	s, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}

	prov, err := l.getProvider(s.ProviderName)
	if err != nil {
		return nil, err
	}

	providerID, syncErr := prov.SyncSubscription(ctx, s)
	result := &provider.SyncResult{
		ProviderName: prov.Name(),
		ProviderID:   providerID,
		EntityType:   "subscription",
		EntityID:     subID.String(),
		Direction:    "push",
		Success:      syncErr == nil,
	}
	if syncErr != nil {
		result.Error = syncErr.Error()
	}

	if syncErr == nil {
		s.ProviderID = providerID
		s.ProviderName = prov.Name()
		_ = l.store.UpdateSubscription(ctx, s) //nolint:errcheck // best-effort update
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), syncErr == nil, syncErr)
	return result, syncErr
}

// SyncInvoiceToProvider pushes a local invoice to the payment provider.
func (l *Ledger) SyncInvoiceToProvider(ctx context.Context, invID id.InvoiceID) (*provider.SyncResult, error) {
	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return nil, err
	}

	prov, err := l.getProvider(inv.ProviderName)
	if err != nil {
		return nil, err
	}

	providerID, syncErr := prov.SyncInvoice(ctx, inv)
	result := &provider.SyncResult{
		ProviderName: prov.Name(),
		ProviderID:   providerID,
		EntityType:   "invoice",
		EntityID:     invID.String(),
		Direction:    "push",
		Success:      syncErr == nil,
	}
	if syncErr != nil {
		result.Error = syncErr.Error()
	}

	if syncErr == nil {
		inv.ProviderID = providerID
		inv.ProviderName = prov.Name()
		_ = l.store.UpdateInvoice(ctx, inv) //nolint:errcheck // best-effort update
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), syncErr == nil, syncErr)
	return result, syncErr
}

// ──────────────────────────────────────────────────
// Provider Import (Data Recovery)
// ──────────────────────────────────────────────────

// ImportPlanFromProvider pulls a plan from the provider and creates it locally.
func (l *Ledger) ImportPlanFromProvider(ctx context.Context, providerName, providerID string) (*plan.Plan, error) {
	prov, err := l.getProvider(providerName)
	if err != nil {
		return nil, err
	}

	p, err := prov.ImportPlan(ctx, providerID)
	if err != nil {
		l.plugins.EmitProviderSync(ctx, prov.Name(), false, err)
		return nil, fmt.Errorf("%w: import plan: %v", ErrProviderSync, err)
	}

	// Assign local IDs and metadata
	p.ID = id.NewPlanID()
	p.Entity = types.NewEntity()
	p.ProviderID = providerID
	p.ProviderName = prov.Name()

	if err := l.store.CreatePlan(ctx, p); err != nil {
		return nil, err
	}

	l.plugins.EmitPlanCreated(ctx, p)
	l.plugins.EmitProviderSync(ctx, prov.Name(), true, nil)
	return p, nil
}

// ImportFeatureFromProvider pulls a feature from the provider and creates it locally.
func (l *Ledger) ImportFeatureFromProvider(ctx context.Context, providerName, providerID string) (*feature.Feature, error) {
	prov, err := l.getProvider(providerName)
	if err != nil {
		return nil, err
	}

	f, err := prov.ImportFeature(ctx, providerID)
	if err != nil {
		l.plugins.EmitProviderSync(ctx, prov.Name(), false, err)
		return nil, fmt.Errorf("%w: import feature: %v", ErrProviderSync, err)
	}

	f.ID = id.NewFeatureID()
	f.Entity = types.NewEntity()
	f.ProviderID = providerID
	f.ProviderName = prov.Name()

	if err := l.store.CreateFeature(ctx, f); err != nil {
		return nil, err
	}

	l.plugins.EmitFeatureCreated(ctx, f)
	l.plugins.EmitProviderSync(ctx, prov.Name(), true, nil)
	return f, nil
}

// ImportSubscriptionFromProvider pulls a subscription from the provider and creates it locally.
func (l *Ledger) ImportSubscriptionFromProvider(ctx context.Context, providerName, providerID string) (*subscription.Subscription, error) {
	prov, err := l.getProvider(providerName)
	if err != nil {
		return nil, err
	}

	s, err := prov.ImportSubscription(ctx, providerID)
	if err != nil {
		l.plugins.EmitProviderSync(ctx, prov.Name(), false, err)
		return nil, fmt.Errorf("%w: import subscription: %v", ErrProviderSync, err)
	}

	s.ID = id.NewSubscriptionID()
	s.Entity = types.NewEntity()
	s.ProviderID = providerID
	s.ProviderName = prov.Name()

	if err := l.store.CreateSubscription(ctx, s); err != nil {
		return nil, err
	}

	l.plugins.EmitSubscriptionCreated(ctx, s)
	l.plugins.EmitProviderSync(ctx, prov.Name(), true, nil)
	return s, nil
}

// ImportInvoiceFromProvider pulls an invoice from the provider and creates it locally.
func (l *Ledger) ImportInvoiceFromProvider(ctx context.Context, providerName, providerID string) (*invoice.Invoice, error) {
	prov, err := l.getProvider(providerName)
	if err != nil {
		return nil, err
	}

	inv, err := prov.ImportInvoice(ctx, providerID)
	if err != nil {
		l.plugins.EmitProviderSync(ctx, prov.Name(), false, err)
		return nil, fmt.Errorf("%w: import invoice: %v", ErrProviderSync, err)
	}

	inv.ID = id.NewInvoiceID()
	inv.Entity = types.NewEntity()
	inv.ProviderID = providerID
	inv.ProviderName = prov.Name()

	if err := l.store.CreateInvoice(ctx, inv); err != nil {
		return nil, err
	}

	l.plugins.EmitInvoiceGenerated(ctx, inv)
	l.plugins.EmitProviderSync(ctx, prov.Name(), true, nil)
	return inv, nil
}

// ──────────────────────────────────────────────────
// Webhook Reconciliation
// ──────────────────────────────────────────────────

// HandleWebhook routes an incoming webhook payload to the correct provider.
func (l *Ledger) HandleWebhook(ctx context.Context, providerName string, payload []byte) (*provider.WebhookResult, error) {
	prov, err := l.getProvider(providerName)
	if err != nil {
		return nil, err
	}

	l.plugins.EmitWebhookReceived(ctx, prov.Name(), payload)

	result, err := prov.HandleWebhook(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderWebhook, err)
	}

	return result, nil
}

// ──────────────────────────────────────────────────
// Invoice Lifecycle
// ──────────────────────────────────────────────────

// FinalizeInvoice transitions an invoice from draft to pending.
func (l *Ledger) FinalizeInvoice(ctx context.Context, invID id.InvoiceID) error {
	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return err
	}

	if inv.Status != invoice.StatusDraft {
		return ErrInvoiceFinalized
	}

	inv.Status = invoice.StatusPending
	now := time.Now()
	dueDate := now.AddDate(0, 0, 30) // 30-day payment terms
	inv.DueDate = &dueDate

	if err := l.store.UpdateInvoice(ctx, inv); err != nil {
		return err
	}

	l.plugins.EmitInvoiceFinalized(ctx, inv)
	return nil
}

// MarkInvoicePaid marks an invoice as paid.
func (l *Ledger) MarkInvoicePaid(ctx context.Context, invID id.InvoiceID, paidAt time.Time, paymentRef string) error {
	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return err
	}

	if inv.Status == invoice.StatusPaid {
		return ErrInvoicePaid
	}
	if inv.Status == invoice.StatusVoided {
		return ErrInvoiceVoided
	}

	if err := l.store.MarkInvoicePaid(ctx, invID, paidAt, paymentRef); err != nil {
		return err
	}

	inv.Status = invoice.StatusPaid
	inv.PaidAt = &paidAt
	inv.PaymentRef = paymentRef
	l.plugins.EmitInvoicePaid(ctx, inv)
	return nil
}

// MarkInvoiceVoided marks an invoice as voided with a reason.
func (l *Ledger) MarkInvoiceVoided(ctx context.Context, invID id.InvoiceID, reason string) error {
	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return err
	}

	if inv.Status == invoice.StatusPaid {
		return ErrInvoicePaid
	}
	if inv.Status == invoice.StatusVoided {
		return ErrInvoiceVoided
	}

	if err := l.store.MarkInvoiceVoided(ctx, invID, reason); err != nil {
		return err
	}

	now := time.Now()
	inv.Status = invoice.StatusVoided
	inv.VoidedAt = &now
	inv.VoidReason = reason
	l.plugins.EmitInvoiceVoided(ctx, inv, reason)
	return nil
}
