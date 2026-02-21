package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
)

type Store struct {
	mu sync.RWMutex

	// Plan storage
	plans map[string]*plan.Plan

	// Subscription storage
	subscriptions map[string]*subscription.Subscription

	// Usage events storage
	usageEvents []meter.UsageEvent

	// Entitlement cache
	entitlementCache map[string]*entitlement.Result
	cacheExpiry      map[string]time.Time

	// Invoice storage
	invoices map[string]*invoice.Invoice

	// Coupon storage
	coupons map[string]*coupon.Coupon
}

func New() *Store {
	return &Store{
		plans:            make(map[string]*plan.Plan),
		subscriptions:    make(map[string]*subscription.Subscription),
		usageEvents:      make([]meter.UsageEvent, 0),
		entitlementCache: make(map[string]*entitlement.Result),
		cacheExpiry:      make(map[string]time.Time),
		invoices:         make(map[string]*invoice.Invoice),
		coupons:          make(map[string]*coupon.Coupon),
	}
}

// Plan Store implementation
func (s *Store) CreatePlan(_ context.Context, p *plan.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.plans[p.ID.String()]; exists {
		return ledger.ErrAlreadyExists
	}
	s.plans[p.ID.String()] = p
	return nil
}

func (s *Store) GetPlan(_ context.Context, planID id.PlanID) (*plan.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if p, ok := s.plans[planID.String()]; ok {
		return p, nil
	}
	return nil, ledger.ErrPlanNotFound
}

func (s *Store) GetPlanBySlug(_ context.Context, slug, appID string) (*plan.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, p := range s.plans {
		if p.Slug == slug && p.AppID == appID {
			return p, nil
		}
	}
	return nil, ledger.ErrPlanNotFound
}

func (s *Store) ListPlans(_ context.Context, appID string, opts plan.ListOpts) ([]*plan.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*plan.Plan, 0)
	for _, p := range s.plans {
		if p.AppID == appID {
			if opts.Status == "" || p.Status == opts.Status {
				result = append(result, p)
			}
		}
	}

	// Apply limit/offset
	start := opts.Offset
	if start > len(result) {
		start = len(result)
	}
	end := start + opts.Limit
	if opts.Limit == 0 || end > len(result) {
		end = len(result)
	}

	return result[start:end], nil
}

func (s *Store) UpdatePlan(_ context.Context, p *plan.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.plans[p.ID.String()]; !exists {
		return ledger.ErrPlanNotFound
	}
	s.plans[p.ID.String()] = p
	return nil
}

func (s *Store) DeletePlan(_ context.Context, planID id.PlanID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.plans, planID.String())
	return nil
}

func (s *Store) ArchivePlan(_ context.Context, planID id.PlanID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, exists := s.plans[planID.String()]; exists {
		p.Status = plan.StatusArchived
		return nil
	}
	return ledger.ErrPlanNotFound
}

// Subscription Store implementation
func (s *Store) CreateSubscription(_ context.Context, sub *subscription.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.subscriptions[sub.ID.String()]; exists {
		return ledger.ErrAlreadyExists
	}
	s.subscriptions[sub.ID.String()] = sub
	return nil
}

func (s *Store) GetSubscription(_ context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if sub, ok := s.subscriptions[subID.String()]; ok {
		return sub, nil
	}
	return nil, ledger.ErrSubscriptionNotFound
}

func (s *Store) GetActiveSubscription(_ context.Context, tenantID, appID string) (*subscription.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sub := range s.subscriptions {
		if sub.TenantID == tenantID && sub.AppID == appID &&
			(sub.Status == subscription.StatusActive || sub.Status == subscription.StatusTrialing) {
			return sub, nil
		}
	}
	return nil, ledger.ErrNoActiveSubscription
}

func (s *Store) ListSubscriptions(_ context.Context, tenantID, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*subscription.Subscription, 0)
	for _, sub := range s.subscriptions {
		if sub.TenantID == tenantID && sub.AppID == appID {
			if opts.Status == "" || sub.Status == opts.Status {
				result = append(result, sub)
			}
		}
	}
	return result, nil
}

func (s *Store) UpdateSubscription(_ context.Context, sub *subscription.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.subscriptions[sub.ID.String()] = sub
	return nil
}

func (s *Store) CancelSubscription(_ context.Context, subID id.SubscriptionID, cancelAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sub, exists := s.subscriptions[subID.String()]; exists {
		sub.CancelAt = &cancelAt
		if time.Now().After(cancelAt) {
			sub.Status = subscription.StatusCanceled
			now := time.Now()
			sub.CanceledAt = &now
		}
		return nil
	}
	return ledger.ErrSubscriptionNotFound
}

// Meter Store implementation
func (s *Store) IngestBatch(_ context.Context, events []*meter.UsageEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range events {
		// Check for duplicate idempotency key
		if e.IdempotencyKey != "" {
			for _, existing := range s.usageEvents {
				if existing.IdempotencyKey == e.IdempotencyKey {
					continue // Skip duplicate
				}
			}
		}
		s.usageEvents = append(s.usageEvents, *e)
	}
	return nil
}

func (s *Store) Aggregate(_ context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var total int64
	now := time.Now()
	startOfPeriod := getStartOfPeriod(now, period)

	for _, event := range s.usageEvents {
		if event.TenantID == tenantID &&
			event.AppID == appID &&
			event.FeatureKey == featureKey &&
			event.Timestamp.After(startOfPeriod) {
			total += event.Quantity
		}
	}

	return total, nil
}

func (s *Store) AggregateMulti(ctx context.Context, tenantID, appID string, featureKeys []string, period plan.Period) (map[string]int64, error) {
	result := make(map[string]int64)
	for _, key := range featureKeys {
		total, err := s.Aggregate(ctx, tenantID, appID, key, period)
		if err != nil {
			return nil, err
		}
		result[key] = total
	}
	return result, nil
}

func (s *Store) QueryUsage(_ context.Context, tenantID, appID string, opts meter.QueryOpts) ([]*meter.UsageEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*meter.UsageEvent, 0)
	for i := range s.usageEvents {
		e := &s.usageEvents[i]
		if e.TenantID == tenantID && e.AppID == appID {
			if opts.FeatureKey == "" || e.FeatureKey == opts.FeatureKey {
				if (opts.Start.IsZero() || e.Timestamp.After(opts.Start)) &&
					(opts.End.IsZero() || e.Timestamp.Before(opts.End)) {
					result = append(result, e)
				}
			}
		}
	}
	return result, nil
}

func (s *Store) PurgeUsage(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var count int64
	newEvents := make([]meter.UsageEvent, 0)
	for _, e := range s.usageEvents {
		if e.Timestamp.Before(before) {
			count++
		} else {
			newEvents = append(newEvents, e)
		}
	}
	s.usageEvents = newEvents
	return count, nil
}

// Entitlement Store implementation
func (s *Store) GetCached(_ context.Context, tenantID, appID, featureKey string) (*entitlement.Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := fmt.Sprintf("%s:%s:%s", tenantID, appID, featureKey)
	if expiry, ok := s.cacheExpiry[key]; ok {
		if time.Now().Before(expiry) {
			if result, ok := s.entitlementCache[key]; ok {
				return result, nil
			}
		}
	}
	return nil, ledger.ErrCacheMiss
}

func (s *Store) SetCached(_ context.Context, tenantID, appID, featureKey string, result *entitlement.Result, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s:%s:%s", tenantID, appID, featureKey)
	s.entitlementCache[key] = result
	s.cacheExpiry[key] = time.Now().Add(ttl)
	return nil
}

func (s *Store) Invalidate(_ context.Context, tenantID, appID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prefix := fmt.Sprintf("%s:%s:", tenantID, appID)
	for key := range s.entitlementCache {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(s.entitlementCache, key)
			delete(s.cacheExpiry, key)
		}
	}
	return nil
}

func (s *Store) InvalidateFeature(_ context.Context, tenantID, appID, featureKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s:%s:%s", tenantID, appID, featureKey)
	delete(s.entitlementCache, key)
	delete(s.cacheExpiry, key)
	return nil
}

// Invoice Store implementation
func (s *Store) CreateInvoice(_ context.Context, inv *invoice.Invoice) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.invoices[inv.ID.String()] = inv
	return nil
}

func (s *Store) GetInvoice(_ context.Context, invID id.InvoiceID) (*invoice.Invoice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if inv, ok := s.invoices[invID.String()]; ok {
		return inv, nil
	}
	return nil, ledger.ErrInvoiceNotFound
}

func (s *Store) ListInvoices(_ context.Context, tenantID, appID string, opts invoice.ListOpts) ([]*invoice.Invoice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*invoice.Invoice, 0)
	for _, inv := range s.invoices {
		if inv.TenantID == tenantID && inv.AppID == appID {
			if opts.Status == "" || inv.Status == opts.Status {
				result = append(result, inv)
			}
		}
	}
	return result, nil
}

func (s *Store) UpdateInvoice(_ context.Context, inv *invoice.Invoice) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.invoices[inv.ID.String()] = inv
	return nil
}

func (s *Store) GetInvoiceByPeriod(_ context.Context, tenantID, appID string, periodStart, periodEnd time.Time) (*invoice.Invoice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, inv := range s.invoices {
		if inv.TenantID == tenantID && inv.AppID == appID &&
			inv.PeriodStart.Equal(periodStart) && inv.PeriodEnd.Equal(periodEnd) {
			return inv, nil
		}
	}
	return nil, ledger.ErrInvoiceNotFound
}

func (s *Store) ListPendingInvoices(_ context.Context, appID string) ([]*invoice.Invoice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*invoice.Invoice, 0)
	for _, inv := range s.invoices {
		if inv.AppID == appID && inv.Status == invoice.StatusPending {
			result = append(result, inv)
		}
	}
	return result, nil
}

func (s *Store) MarkInvoicePaid(_ context.Context, invID id.InvoiceID, paidAt time.Time, paymentRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if inv, ok := s.invoices[invID.String()]; ok {
		inv.Status = invoice.StatusPaid
		inv.PaidAt = &paidAt
		inv.PaymentRef = paymentRef
		return nil
	}
	return ledger.ErrInvoiceNotFound
}

func (s *Store) MarkInvoiceVoided(_ context.Context, invID id.InvoiceID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if inv, ok := s.invoices[invID.String()]; ok {
		inv.Status = invoice.StatusVoided
		now := time.Now()
		inv.VoidedAt = &now
		inv.VoidReason = reason
		return nil
	}
	return ledger.ErrInvoiceNotFound
}

// Coupon Store implementation
func (s *Store) CreateCoupon(_ context.Context, c *coupon.Coupon) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.coupons[c.ID.String()] = c
	return nil
}

func (s *Store) GetCoupon(_ context.Context, code, appID string) (*coupon.Coupon, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, c := range s.coupons {
		if c.Code == code && c.AppID == appID {
			return c, nil
		}
	}
	return nil, ledger.ErrCouponNotFound
}

func (s *Store) GetCouponByID(_ context.Context, couponID id.CouponID) (*coupon.Coupon, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if c, ok := s.coupons[couponID.String()]; ok {
		return c, nil
	}
	return nil, ledger.ErrCouponNotFound
}

func (s *Store) ListCoupons(_ context.Context, appID string, opts coupon.ListOpts) ([]*coupon.Coupon, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*coupon.Coupon, 0)
	now := time.Now()

	for _, c := range s.coupons {
		if c.AppID == appID {
			if opts.Active {
				if (c.ValidFrom == nil || now.After(*c.ValidFrom)) &&
					(c.ValidUntil == nil || now.Before(*c.ValidUntil)) {
					result = append(result, c)
				}
			} else {
				result = append(result, c)
			}
		}
	}
	return result, nil
}

func (s *Store) UpdateCoupon(_ context.Context, c *coupon.Coupon) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.coupons[c.ID.String()] = c
	return nil
}

func (s *Store) DeleteCoupon(_ context.Context, couponID id.CouponID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.coupons, couponID.String())
	return nil
}

// Store management
func (s *Store) Migrate(_ context.Context) error {
	return nil // No migration needed for memory store
}

func (s *Store) Ping(_ context.Context) error {
	return nil // Always available
}

func (s *Store) Close() error {
	return nil // Nothing to close
}

// Helper functions
func getStartOfPeriod(t time.Time, period plan.Period) time.Time {
	switch period {
	case plan.PeriodMonthly:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	case plan.PeriodYearly:
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	default:
		return time.Time{}
	}
}
