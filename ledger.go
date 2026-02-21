package ledger

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// Ledger is the main billing engine.
type Ledger struct {
	store   store.Store
	plugins *plugin.Registry
	logger  *slog.Logger

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
		logger:              slog.Default(),
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
func WithLogger(logger *slog.Logger) Option {
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
		"batch_size", l.meterBatchSize,
		"flush_interval", l.meterFlushInterval,
		"cache_ttl", l.entitlementCacheTTL,
	)

	return nil
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
			"error", err,
			"batch_size", len(batch),
		)
		return
	}

	elapsed := time.Since(start)
	l.plugins.EmitUsageFlushed(ctx, len(batch), elapsed)

	l.logger.Debug("flushed meter batch",
		"batch_size", len(batch),
		"elapsed_ms", elapsed.Milliseconds(),
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
