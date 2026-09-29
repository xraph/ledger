package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/feature"
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

	// Coupon application storage, keyed by subscription ID.
	couponApplications map[string][]*coupon.Application

	// Feature catalog storage
	features map[string]*feature.Feature
}

func New() *Store {
	return &Store{
		plans:              make(map[string]*plan.Plan),
		subscriptions:      make(map[string]*subscription.Subscription),
		usageEvents:        make([]meter.UsageEvent, 0),
		entitlementCache:   make(map[string]*entitlement.Result),
		cacheExpiry:        make(map[string]time.Time),
		invoices:           make(map[string]*invoice.Invoice),
		coupons:            make(map[string]*coupon.Coupon),
		couponApplications: make(map[string][]*coupon.Application),
		features:           make(map[string]*feature.Feature),
	}
}

// copyStringMap returns a fresh map with the same entries as m, or nil when m
// is nil.
func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// copyPlan returns a deep copy of p: its own Features slice (and each
// feature's Metadata), its own Pricing and Tiers, and its own Metadata. The
// store copies on every crossing, in and out, so a caller that edits a loaded
// plan and is then refused has not changed the stored one.
func copyPlan(p *plan.Plan) *plan.Plan {
	if p == nil {
		return nil
	}
	cp := *p
	cp.Metadata = copyStringMap(p.Metadata)
	if p.Features != nil {
		cp.Features = make([]plan.Feature, len(p.Features))
		for i, f := range p.Features {
			cp.Features[i] = f
			cp.Features[i].Metadata = copyStringMap(f.Metadata)
		}
	}
	if p.Pricing != nil {
		pricing := *p.Pricing
		if p.Pricing.Tiers != nil {
			pricing.Tiers = make([]plan.PriceTier, len(p.Pricing.Tiers))
			copy(pricing.Tiers, p.Pricing.Tiers)
		}
		cp.Pricing = &pricing
	}
	return &cp
}

// copyFeature returns a deep copy of f, with its own Metadata.
func copyFeature(f *feature.Feature) *feature.Feature {
	if f == nil {
		return nil
	}
	cp := *f
	cp.Metadata = copyStringMap(f.Metadata)
	return &cp
}

// copyPlans and copyFeatures copy a page of results into a fresh slice.
func copyPlans(in []*plan.Plan) []*plan.Plan {
	out := make([]*plan.Plan, len(in))
	for i, p := range in {
		out[i] = copyPlan(p)
	}
	return out
}

func copyFeatures(in []*feature.Feature) []*feature.Feature {
	out := make([]*feature.Feature, len(in))
	for i, f := range in {
		out[i] = copyFeature(f)
	}
	return out
}

// Plan Store implementation
func (s *Store) CreatePlan(_ context.Context, p *plan.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.plans[p.ID.String()]; exists {
		return ledger.ErrAlreadyExists
	}
	s.plans[p.ID.String()] = copyPlan(p)
	return nil
}

func (s *Store) GetPlan(_ context.Context, planID id.PlanID) (*plan.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if p, ok := s.plans[planID.String()]; ok {
		return copyPlan(p), nil
	}
	return nil, ledger.ErrPlanNotFound
}

func (s *Store) GetPlanBySlug(_ context.Context, slug, appID string) (*plan.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, p := range s.plans {
		if p.Slug == slug && p.AppID == appID {
			return copyPlan(p), nil
		}
	}
	return nil, ledger.ErrPlanNotFound
}

func (s *Store) ListPlans(_ context.Context, appID string, opts plan.ListOpts) ([]*plan.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*plan.Plan, 0)
	for _, p := range s.plans {
		if appID == "" || p.AppID == appID {
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

	return copyPlans(result[start:end]), nil
}

func (s *Store) UpdatePlan(_ context.Context, p *plan.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.plans[p.ID.String()]; !exists {
		return ledger.ErrPlanNotFound
	}
	s.plans[p.ID.String()] = copyPlan(p)
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

// copySubscriptionQuantity returns a fresh map holding the same entries as m.
// It never returns nil, even when m is nil, so a subscription with no
// quantity-priced features reads back an empty map rather than one a caller
// could still be holding a reference to.
//
// Quantity is a map field on a struct the store otherwise hands out and takes
// in by pointer. A shallow copy of the Subscription struct still shares the
// map's backing storage with whatever the caller holds, so without this a
// caller mutating sub.Quantity after Create (or after Get) would silently
// change the stored subscription, bypassing the lock entirely.
func copySubscriptionQuantity(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// copySubscription returns a shallow copy of sub with its Quantity map
// deep-copied, so neither the store nor the caller can mutate the other's
// view of it through the shared struct.
func copySubscription(sub *subscription.Subscription) *subscription.Subscription {
	cp := *sub
	cp.Quantity = copySubscriptionQuantity(sub.Quantity)
	return &cp
}

// Subscription Store implementation
func (s *Store) CreateSubscription(_ context.Context, sub *subscription.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.subscriptions[sub.ID.String()]; exists {
		return ledger.ErrAlreadyExists
	}
	s.subscriptions[sub.ID.String()] = copySubscription(sub)
	return nil
}

func (s *Store) GetSubscription(_ context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if sub, ok := s.subscriptions[subID.String()]; ok {
		return copySubscription(sub), nil
	}
	return nil, ledger.ErrSubscriptionNotFound
}

func (s *Store) GetActiveSubscription(_ context.Context, tenantID, appID string) (*subscription.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sub := range s.subscriptions {
		if sub.TenantID == tenantID && sub.AppID == appID &&
			(sub.Status == subscription.StatusActive || sub.Status == subscription.StatusTrialing) {
			return copySubscription(sub), nil
		}
	}
	return nil, ledger.ErrNoActiveSubscription
}

func (s *Store) ListSubscriptions(_ context.Context, tenantID, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*subscription.Subscription, 0)
	for _, sub := range s.subscriptions {
		if (tenantID == "" || sub.TenantID == tenantID) && (appID == "" || sub.AppID == appID) {
			if opts.Status == "" || sub.Status == opts.Status {
				result = append(result, copySubscription(sub))
			}
		}
	}
	return result, nil
}

func (s *Store) UpdateSubscription(_ context.Context, sub *subscription.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.subscriptions[sub.ID.String()] = copySubscription(sub)
	return nil
}

func (s *Store) CancelSubscription(_ context.Context, subID id.SubscriptionID, cancelAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sub, exists := s.subscriptions[subID.String()]; exists {
		sub.CancelAt = &cancelAt
		if time.Now().After(cancelAt) {
			sub.Status = subscription.StatusCanceled
			now := time.Now().UTC()
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
		// Check for duplicate idempotency key. An empty key never counts as
		// a duplicate of another empty key: only a genuinely repeated
		// non-empty key collapses to one event, matching the partial-unique-
		// index design sqlite, postgres and mongo all use.
		if e.IdempotencyKey != "" {
			duplicate := false
			for _, existing := range s.usageEvents {
				if existing.IdempotencyKey == e.IdempotencyKey {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue // Skip duplicate
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
	// The period start is inclusive, matching the half-open [Start, End)
	// window QueryUsage applies: an event stamped exactly at the start of the
	// period belongs to it. The start is computed from time.Now(), so a test
	// cannot place an event on that instant without an injectable clock, and
	// none is invented here.
	startOfPeriod := getStartOfPeriod(now, period)

	for _, event := range s.usageEvents {
		if event.TenantID == tenantID &&
			event.AppID == appID &&
			event.FeatureKey == featureKey &&
			!event.Timestamp.Before(startOfPeriod) {
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
		if (tenantID == "" || e.TenantID == tenantID) && (appID == "" || e.AppID == appID) {
			if opts.FeatureKey == "" || e.FeatureKey == opts.FeatureKey {
				// The window is half-open, [Start, End): an event stamped
				// exactly at Start is inside it and one stamped exactly at End
				// belongs to the next billing period. A zero Start or End is
				// unbounded on that side.
				if (opts.Start.IsZero() || !e.Timestamp.Before(opts.Start)) &&
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
		if (tenantID == "" || inv.TenantID == tenantID) && (appID == "" || inv.AppID == appID) {
			// Start and End select on the period, as in the SQL and mongo
			// stores: the invoice's period must lie within [Start, End].
			if (opts.Status == "" || inv.Status == opts.Status) &&
				(opts.Start.IsZero() || !inv.PeriodStart.Before(opts.Start)) &&
				(opts.End.IsZero() || !inv.PeriodEnd.After(opts.End)) {
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
		if (appID == "" || inv.AppID == appID) && inv.Status == invoice.StatusPending {
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
		now := time.Now().UTC()
		inv.VoidedAt = &now
		inv.VoidReason = reason
		return nil
	}
	return ledger.ErrInvoiceNotFound
}

// Coupon Store implementation
//
// Every method below that crosses this store's boundary - in either
// direction - copies the coupon.Coupon value rather than sharing a
// pointer. CreateCoupon and UpdateCoupon copy the caller's coupon before
// storing it, and GetCoupon, GetCouponByID, ListCoupons and
// ListAppliedCoupons copy the stored coupon before handing it back. Once a
// *coupon.Coupon crosses in or out, this store never touches the fields of
// that specific object again - it only ever mutates its own internal
// copies, under s.mu. This is what makes it safe for a caller to read
// fields off a coupon obtained from Get without holding any lock, even
// while RedeemCoupon or IncrementCouponRedemptions is concurrently
// updating the same coupon on another goroutine: Ledger.ApplyCoupon's
// fast-path exhaustion check does exactly this, and a -race run over
// concurrent ApplyCoupon calls caught it as a real data race before these
// methods copied on every crossing.
func (s *Store) CreateCoupon(_ context.Context, c *coupon.Coupon) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := *c
	s.coupons[c.ID.String()] = &stored
	return nil
}

func (s *Store) GetCoupon(_ context.Context, code, appID string) (*coupon.Coupon, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, c := range s.coupons {
		if c.Code == code && c.AppID == appID {
			cp := *c
			return &cp, nil
		}
	}
	return nil, ledger.ErrCouponNotFound
}

func (s *Store) GetCouponByID(_ context.Context, couponID id.CouponID) (*coupon.Coupon, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if c, ok := s.coupons[couponID.String()]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, ledger.ErrCouponNotFound
}

func (s *Store) ListCoupons(_ context.Context, appID string, opts coupon.ListOpts) ([]*coupon.Coupon, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*coupon.Coupon, 0)
	now := time.Now()

	for _, c := range s.coupons {
		if appID == "" || c.AppID == appID {
			if opts.Active {
				// A coupon is valid on [ValidFrom, ValidUntil], inclusive at
				// both ends: the rule the database backends and
				// Ledger.ApplyCoupon use. Only the instant itself differs from
				// a strict comparison, and that cannot be tested without an
				// injectable clock, so no test pins it.
				if (c.ValidFrom == nil || !now.Before(*c.ValidFrom)) &&
					(c.ValidUntil == nil || !now.After(*c.ValidUntil)) {
					cp := *c
					result = append(result, &cp)
				}
			} else {
				cp := *c
				result = append(result, &cp)
			}
		}
	}
	return result, nil
}

func (s *Store) UpdateCoupon(_ context.Context, c *coupon.Coupon) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := *c
	// The redemption count belongs to RedeemCoupon. Keep whatever is stored,
	// whatever the caller's copy carries.
	if current, ok := s.coupons[c.ID.String()]; ok {
		stored.TimesRedeemed = current.TimesRedeemed
	}
	s.coupons[c.ID.String()] = &stored
	return nil
}

func (s *Store) DeleteCoupon(_ context.Context, couponID id.CouponID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.coupons, couponID.String())
	return nil
}

// couponApplicationsFor returns this subscription's applications. Test
// helper and internal reader; callers outside this file use
// ListAppliedCoupons.
func (s *Store) couponApplicationsFor(subID id.SubscriptionID) []*coupon.Application {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.couponApplications[subID.String()]
}

// ApplyCoupon is a low-level operation kept for callers and tests that need
// it separately. Engine code redeems through RedeemCoupon, which records
// the application and increments the redemption count as one unit.
func (s *Store) ApplyCoupon(_ context.Context, subID id.SubscriptionID, couponID id.CouponID) error {
	if err := validateSubscriptionID(subID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.coupons[couponID.String()]; !ok {
		return ledger.ErrCouponNotFound
	}

	key := subID.String()
	for _, a := range s.couponApplications[key] {
		if a.CouponID.String() == couponID.String() {
			return ledger.ErrCouponAlreadyApplied
		}
	}

	s.couponApplications[key] = append(s.couponApplications[key], &coupon.Application{
		ID:             id.NewCouponApplicationID(),
		CouponID:       couponID,
		SubscriptionID: subID,
		AppliedAt:      time.Now().UTC(),
	})

	return nil
}

func (s *Store) ListAppliedCoupons(_ context.Context, subID id.SubscriptionID) ([]*coupon.Coupon, error) {
	if subID.IsNil() {
		return make([]*coupon.Coupon, 0), nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Empty rather than nil: a subscription with no coupons is a valid
	// answer and not a missing one.
	result := make([]*coupon.Coupon, 0)

	for _, a := range s.couponApplications[subID.String()] {
		if c, ok := s.coupons[a.CouponID.String()]; ok {
			cp := *c
			result = append(result, &cp)
		}
	}

	return result, nil
}

// IncrementCouponRedemptions is a low-level operation kept for callers and
// tests that need it separately. Engine code redeems through RedeemCoupon,
// which increments the count and records the application as one unit.
//
// This replaces the map's entry with an updated copy rather than mutating
// the existing *coupon.Coupon in place, consistent with every other coupon
// method on this store copying on every crossing (see the comment above
// CreateCoupon). Mutating the stored object's fields in place, even under
// s.mu, would still be visible through any *coupon.Coupon a caller
// obtained from an earlier Get and is holding without a lock - and Get
// already handed that caller its own copy, so there is nothing of this
// store's for that mutation to reach.
func (s *Store) IncrementCouponRedemptions(_ context.Context, couponID id.CouponID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.coupons[couponID.String()]
	if !ok {
		return ledger.ErrCouponNotFound
	}

	updated := *c
	updated.TimesRedeemed++
	updated.Touch()
	s.coupons[couponID.String()] = &updated

	return nil
}

// RedeemCoupon records a coupon's application to a subscription and
// increments its redemption count under one lock, so a reader can never
// observe the count moved without the application row that explains it, or
// see a stale TimesRedeemed while deciding whether the coupon is exhausted.
// The cap (MaxRedemptions) is checked and the count incremented atomically
// with respect to every other call on this store: there is no window
// between reading TimesRedeemed and writing it where a concurrent call
// could also pass the cap check.
//
// Like IncrementCouponRedemptions, this swaps in an updated copy of the
// coupon rather than mutating the stored pointer in place - see that
// method's comment for why, and CreateCoupon's for the copy-on-every-
// crossing rule this store follows throughout.
func (s *Store) RedeemCoupon(_ context.Context, subID id.SubscriptionID, couponID id.CouponID) error {
	if err := validateSubscriptionID(subID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.coupons[couponID.String()]
	if !ok {
		return ledger.ErrCouponNotFound
	}

	key := subID.String()
	for _, a := range s.couponApplications[key] {
		if a.CouponID.String() == couponID.String() {
			return ledger.ErrCouponAlreadyApplied
		}
	}

	if c.MaxRedemptions > 0 && c.TimesRedeemed >= c.MaxRedemptions {
		return ledger.ErrCouponExhausted
	}

	s.couponApplications[key] = append(s.couponApplications[key], &coupon.Application{
		ID:             id.NewCouponApplicationID(),
		CouponID:       couponID,
		SubscriptionID: subID,
		AppliedAt:      time.Now().UTC(),
	})

	updated := *c
	updated.TimesRedeemed++
	updated.Touch()
	s.coupons[couponID.String()] = &updated

	return nil
}

// Feature catalog Store implementation
func (s *Store) CreateFeature(_ context.Context, f *feature.Feature) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.features[f.ID.String()]; exists {
		return ledger.ErrAlreadyExists
	}
	// Check for duplicate key within scope
	for _, existing := range s.features {
		if existing.Key == f.Key && existing.AppID == f.AppID {
			return ledger.ErrDuplicateFeature
		}
	}
	s.features[f.ID.String()] = copyFeature(f)
	return nil
}

func (s *Store) GetFeature(_ context.Context, featureID id.FeatureID) (*feature.Feature, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if f, ok := s.features[featureID.String()]; ok {
		return copyFeature(f), nil
	}
	return nil, ledger.ErrFeatureNotFound
}

func (s *Store) GetFeatureByKey(_ context.Context, key, appID string) (*feature.Feature, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, f := range s.features {
		if f.Key == key && f.AppID == appID {
			return copyFeature(f), nil
		}
	}
	return nil, ledger.ErrFeatureNotFound
}

func (s *Store) ListFeatures(_ context.Context, appID string, opts feature.ListOpts) ([]*feature.Feature, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*feature.Feature, 0)
	for _, f := range s.features {
		if appID == "" || f.AppID == appID {
			if opts.Status == "" || f.Status == opts.Status {
				result = append(result, f)
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

	return copyFeatures(result[start:end]), nil
}

func (s *Store) ListGlobalFeatures(_ context.Context, opts feature.ListOpts) ([]*feature.Feature, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*feature.Feature, 0)
	for _, f := range s.features {
		if f.AppID == "" {
			if opts.Status == "" || f.Status == opts.Status {
				result = append(result, f)
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

	return copyFeatures(result[start:end]), nil
}

func (s *Store) UpdateFeature(_ context.Context, f *feature.Feature) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.features[f.ID.String()]; !exists {
		return ledger.ErrFeatureNotFound
	}
	s.features[f.ID.String()] = copyFeature(f)
	return nil
}

func (s *Store) DeleteFeature(_ context.Context, featureID id.FeatureID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.features, featureID.String())
	return nil
}

func (s *Store) ArchiveFeature(_ context.Context, featureID id.FeatureID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if f, exists := s.features[featureID.String()]; exists {
		f.Status = feature.StatusArchived
		return nil
	}
	return ledger.ErrFeatureNotFound
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

// validateSubscriptionID rejects a subscription id that is nil or carries
// the wrong prefix before any storage is touched.
func validateSubscriptionID(subID id.SubscriptionID) error {
	if subID.IsNil() || subID.Prefix() != id.PrefixSubscription {
		return fmt.Errorf("ledger/memory: invalid subscription id %q: %w", subID.String(), ledger.ErrInvalidInput)
	}
	return nil
}

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
