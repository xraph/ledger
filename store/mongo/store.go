package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
)

// Collection name constants.
const (
	colPlans         = "ledger_plans"
	colSubscriptions = "ledger_subscriptions"
	colUsageEvents   = "ledger_usage_events"
	colEntitlements  = "ledger_entitlement_cache"
	colInvoices      = "ledger_invoices"
	colCoupons       = "ledger_coupons"
)

// compile-time interface check
var _ ledgerstore.Store = (*Store)(nil)

// Store implements store.Store using MongoDB via Grove ORM.
type Store struct {
	db  *grove.DB
	mdb *mongodriver.MongoDB
}

// New creates a new MongoDB store backed by Grove ORM.
func New(db *grove.DB) *Store {
	return &Store{
		db:  db,
		mdb: mongodriver.Unwrap(db),
	}
}

// DB returns the underlying grove database for direct access.
func (s *Store) DB() *grove.DB { return s.db }

// Migrate creates indexes for all ledger collections.
func (s *Store) Migrate(ctx context.Context) error {
	indexes := migrationIndexes()

	for col, models := range indexes {
		if len(models) == 0 {
			continue
		}
		_, err := s.mdb.Collection(col).Indexes().CreateMany(ctx, models)
		if err != nil {
			return fmt.Errorf("ledger/mongo: migrate %s indexes: %w", col, err)
		}
	}
	return nil
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.Ping(ctx)
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ==================== Plan Store ====================

func (s *Store) CreatePlan(ctx context.Context, p *plan.Plan) error {
	m := toPlanModel(p)
	_, err := s.mdb.NewInsert(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: create plan: %w", err)
	}
	return nil
}

func (s *Store) GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error) {
	var m planModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{"_id": planID.String()}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrPlanNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get plan: %w", err)
	}
	return fromPlanModel(&m)
}

func (s *Store) GetPlanBySlug(ctx context.Context, slug, appID string) (*plan.Plan, error) {
	var m planModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{"slug": slug, "app_id": appID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrPlanNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get plan by slug: %w", err)
	}
	return fromPlanModel(&m)
}

func (s *Store) ListPlans(ctx context.Context, appID string, opts plan.ListOpts) ([]*plan.Plan, error) {
	var models []planModel

	filter := bson.M{"app_id": appID}
	if opts.Status != "" {
		filter["status"] = string(opts.Status)
	}

	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: "created_at", Value: 1}})

	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}
	if opts.Offset > 0 {
		q = q.Skip(int64(opts.Offset))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("ledger/mongo: list plans: %w", err)
	}

	result := make([]*plan.Plan, len(models))
	for i := range models {
		p, err := fromPlanModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = p
	}
	return result, nil
}

func (s *Store) UpdatePlan(ctx context.Context, p *plan.Plan) error {
	m := toPlanModel(p)
	m.UpdatedAt = now()

	res, err := s.mdb.NewUpdate(m).
		Filter(bson.M{"_id": m.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: update plan: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrPlanNotFound
	}
	return nil
}

func (s *Store) DeletePlan(ctx context.Context, planID id.PlanID) error {
	res, err := s.mdb.NewDelete((*planModel)(nil)).
		Filter(bson.M{"_id": planID.String()}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: delete plan: %w", err)
	}
	if res.DeletedCount() == 0 {
		return ledger.ErrPlanNotFound
	}
	return nil
}

func (s *Store) ArchivePlan(ctx context.Context, planID id.PlanID) error {
	t := now()
	res, err := s.mdb.NewUpdate((*planModel)(nil)).
		Filter(bson.M{"_id": planID.String()}).
		Set("status", string(plan.StatusArchived)).
		Set("updated_at", t).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: archive plan: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrPlanNotFound
	}
	return nil
}

// ==================== Subscription Store ====================

func (s *Store) CreateSubscription(ctx context.Context, sub *subscription.Subscription) error {
	m := toSubscriptionModel(sub)
	_, err := s.mdb.NewInsert(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: create subscription: %w", err)
	}
	return nil
}

func (s *Store) GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	var m subscriptionModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{"_id": subID.String()}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get subscription: %w", err)
	}
	return fromSubscriptionModel(&m)
}

func (s *Store) GetActiveSubscription(ctx context.Context, tenantID, appID string) (*subscription.Subscription, error) {
	var m subscriptionModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{
			"tenant_id": tenantID,
			"app_id":    appID,
			"status":    bson.M{"$in": []string{string(subscription.StatusActive), string(subscription.StatusTrialing)}},
		}).
		Sort(bson.D{{Key: "created_at", Value: -1}}).
		Limit(1).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrNoActiveSubscription
		}
		return nil, fmt.Errorf("ledger/mongo: get active subscription: %w", err)
	}
	return fromSubscriptionModel(&m)
}

func (s *Store) ListSubscriptions(ctx context.Context, tenantID, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error) {
	var models []subscriptionModel

	filter := bson.M{"tenant_id": tenantID, "app_id": appID}
	if opts.Status != "" {
		filter["status"] = string(opts.Status)
	}

	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}
	if opts.Offset > 0 {
		q = q.Skip(int64(opts.Offset))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("ledger/mongo: list subscriptions: %w", err)
	}

	result := make([]*subscription.Subscription, len(models))
	for i := range models {
		sub, err := fromSubscriptionModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = sub
	}
	return result, nil
}

func (s *Store) UpdateSubscription(ctx context.Context, sub *subscription.Subscription) error {
	m := toSubscriptionModel(sub)
	m.UpdatedAt = now()

	_, err := s.mdb.NewUpdate(m).
		Filter(bson.M{"_id": m.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: update subscription: %w", err)
	}
	return nil
}

func (s *Store) CancelSubscription(ctx context.Context, subID id.SubscriptionID, cancelAt time.Time) error {
	t := now()
	update := s.mdb.NewUpdate((*subscriptionModel)(nil)).
		Filter(bson.M{"_id": subID.String()}).
		Set("cancel_at", cancelAt).
		Set("updated_at", t)

	if time.Now().After(cancelAt) {
		update = update.
			Set("status", string(subscription.StatusCanceled)).
			Set("canceled_at", t)
	}

	res, err := update.Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: cancel subscription: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrSubscriptionNotFound
	}
	return nil
}

// ==================== Meter Store ====================

func (s *Store) IngestBatch(ctx context.Context, events []*meter.UsageEvent) error {
	if len(events) == 0 {
		return nil
	}
	for _, e := range events {
		m := toUsageEventModel(e)
		_, err := s.mdb.NewInsert(m).Exec(ctx)
		if err != nil {
			// Skip duplicates for idempotency
			if mongo.IsDuplicateKeyError(err) {
				continue
			}
			return fmt.Errorf("ledger/mongo: ingest event: %w", err)
		}
	}
	return nil
}

func (s *Store) Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error) {
	startOfPeriod := getStartOfPeriod(time.Now(), period)

	pipeline := bson.A{
		bson.M{
			"$match": bson.M{
				"tenant_id":   tenantID,
				"app_id":      appID,
				"feature_key": featureKey,
				"timestamp":   bson.M{"$gt": startOfPeriod},
			},
		},
		bson.M{
			"$group": bson.M{
				"_id":   nil,
				"total": bson.M{"$sum": "$quantity"},
			},
		},
	}

	cursor, err := s.mdb.Collection(colUsageEvents).Aggregate(ctx, pipeline)
	if err != nil {
		return 0, fmt.Errorf("ledger/mongo: aggregate: %w", err)
	}
	defer cursor.Close(ctx)

	var results []struct {
		Total int64 `bson:"total"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return 0, fmt.Errorf("ledger/mongo: aggregate decode: %w", err)
	}

	if len(results) == 0 {
		return 0, nil
	}
	return results[0].Total, nil
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

func (s *Store) QueryUsage(ctx context.Context, tenantID, appID string, opts meter.QueryOpts) ([]*meter.UsageEvent, error) {
	var models []usageEventModel

	filter := bson.M{"tenant_id": tenantID, "app_id": appID}
	if opts.FeatureKey != "" {
		filter["feature_key"] = opts.FeatureKey
	}
	if !opts.Start.IsZero() {
		if _, ok := filter["timestamp"]; !ok {
			filter["timestamp"] = bson.M{}
		}
		if ts, ok := filter["timestamp"].(bson.M); ok {
			ts["$gte"] = opts.Start
		}
	}
	if !opts.End.IsZero() {
		if _, ok := filter["timestamp"]; !ok {
			filter["timestamp"] = bson.M{}
		}
		if ts, ok := filter["timestamp"].(bson.M); ok {
			ts["$lte"] = opts.End
		}
	}

	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: "timestamp", Value: -1}})

	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}
	if opts.Offset > 0 {
		q = q.Skip(int64(opts.Offset))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("ledger/mongo: query usage: %w", err)
	}

	result := make([]*meter.UsageEvent, len(models))
	for i := range models {
		evt, err := fromUsageEventModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = evt
	}
	return result, nil
}

func (s *Store) PurgeUsage(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.mdb.NewDelete((*usageEventModel)(nil)).
		Filter(bson.M{"timestamp": bson.M{"$lt": before}}).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("ledger/mongo: purge usage: %w", err)
	}
	return res.DeletedCount(), nil
}

// ==================== Entitlement Cache Store ====================

func (s *Store) GetCached(ctx context.Context, tenantID, appID, featureKey string) (*entitlement.Result, error) {
	var m entitlementCacheModel
	cacheKey := tenantID + ":" + appID + ":" + featureKey
	err := s.mdb.NewFind(&m).
		Filter(bson.M{
			"_id":        cacheKey,
			"expires_at": bson.M{"$gt": time.Now().UTC()},
		}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrCacheMiss
		}
		return nil, fmt.Errorf("ledger/mongo: get cached: %w", err)
	}
	return fromEntitlementCacheModel(&m), nil
}

func (s *Store) SetCached(ctx context.Context, tenantID, appID, featureKey string, result *entitlement.Result, ttl time.Duration) error {
	expiresAt := time.Now().UTC().Add(ttl)
	m := toEntitlementCacheModel(tenantID, appID, featureKey, result, expiresAt)

	_, err := s.mdb.NewUpdate(m).
		Filter(bson.M{"_id": m.CacheKey}).
		SetUpdate(bson.M{"$set": bson.M{
			"_id":         m.CacheKey,
			"tenant_id":   m.TenantID,
			"app_id":      m.AppID,
			"feature_key": m.FeatureKey,
			"allowed":     m.Allowed,
			"feature":     m.Feature,
			"used":        m.Used,
			"cache_limit": m.Limit,
			"remaining":   m.Remaining,
			"soft_limit":  m.SoftLimit,
			"reason":      m.Reason,
			"expires_at":  m.ExpiresAt,
			"created_at":  m.CreatedAt,
		}}).
		Upsert().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: set cached: %w", err)
	}
	return nil
}

func (s *Store) Invalidate(ctx context.Context, tenantID, appID string) error {
	_, err := s.mdb.NewDelete((*entitlementCacheModel)(nil)).
		Filter(bson.M{"tenant_id": tenantID, "app_id": appID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: invalidate: %w", err)
	}
	return nil
}

func (s *Store) InvalidateFeature(ctx context.Context, tenantID, appID, featureKey string) error {
	cacheKey := tenantID + ":" + appID + ":" + featureKey
	_, err := s.mdb.NewDelete((*entitlementCacheModel)(nil)).
		Filter(bson.M{"_id": cacheKey}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: invalidate feature: %w", err)
	}
	return nil
}

// ==================== Invoice Store ====================

func (s *Store) CreateInvoice(ctx context.Context, inv *invoice.Invoice) error {
	m := toInvoiceModel(inv)
	_, err := s.mdb.NewInsert(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: create invoice: %w", err)
	}
	return nil
}

func (s *Store) GetInvoice(ctx context.Context, invID id.InvoiceID) (*invoice.Invoice, error) {
	var m invoiceModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{"_id": invID.String()}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get invoice: %w", err)
	}
	return fromInvoiceModel(&m)
}

func (s *Store) ListInvoices(ctx context.Context, tenantID, appID string, opts invoice.ListOpts) ([]*invoice.Invoice, error) {
	var models []invoiceModel

	filter := bson.M{"tenant_id": tenantID, "app_id": appID}
	if opts.Status != "" {
		filter["status"] = string(opts.Status)
	}
	if !opts.Start.IsZero() {
		filter["period_start"] = bson.M{"$gte": opts.Start}
	}
	if !opts.End.IsZero() {
		filter["period_end"] = bson.M{"$lte": opts.End}
	}

	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}
	if opts.Offset > 0 {
		q = q.Skip(int64(opts.Offset))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("ledger/mongo: list invoices: %w", err)
	}

	result := make([]*invoice.Invoice, len(models))
	for i := range models {
		inv, err := fromInvoiceModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = inv
	}
	return result, nil
}

func (s *Store) UpdateInvoice(ctx context.Context, inv *invoice.Invoice) error {
	m := toInvoiceModel(inv)
	m.UpdatedAt = now()

	_, err := s.mdb.NewUpdate(m).
		Filter(bson.M{"_id": m.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: update invoice: %w", err)
	}
	return nil
}

func (s *Store) GetInvoiceByPeriod(ctx context.Context, tenantID, appID string, periodStart, periodEnd time.Time) (*invoice.Invoice, error) {
	var m invoiceModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{
			"tenant_id":    tenantID,
			"app_id":       appID,
			"period_start": periodStart,
			"period_end":   periodEnd,
		}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get invoice by period: %w", err)
	}
	return fromInvoiceModel(&m)
}

func (s *Store) ListPendingInvoices(ctx context.Context, appID string) ([]*invoice.Invoice, error) {
	var models []invoiceModel

	err := s.mdb.NewFind(&models).
		Filter(bson.M{"app_id": appID, "status": string(invoice.StatusPending)}).
		Sort(bson.D{{Key: "created_at", Value: -1}}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger/mongo: list pending invoices: %w", err)
	}

	result := make([]*invoice.Invoice, len(models))
	for i := range models {
		inv, err := fromInvoiceModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = inv
	}
	return result, nil
}

func (s *Store) MarkInvoicePaid(ctx context.Context, invID id.InvoiceID, paidAt time.Time, paymentRef string) error {
	t := now()
	res, err := s.mdb.NewUpdate((*invoiceModel)(nil)).
		Filter(bson.M{"_id": invID.String()}).
		Set("status", string(invoice.StatusPaid)).
		Set("paid_at", paidAt).
		Set("payment_ref", paymentRef).
		Set("updated_at", t).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: mark invoice paid: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrInvoiceNotFound
	}
	return nil
}

func (s *Store) MarkInvoiceVoided(ctx context.Context, invID id.InvoiceID, reason string) error {
	t := now()
	res, err := s.mdb.NewUpdate((*invoiceModel)(nil)).
		Filter(bson.M{"_id": invID.String()}).
		Set("status", string(invoice.StatusVoided)).
		Set("voided_at", t).
		Set("void_reason", reason).
		Set("updated_at", t).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: mark invoice voided: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrInvoiceNotFound
	}
	return nil
}

// ==================== Coupon Store ====================

func (s *Store) CreateCoupon(ctx context.Context, c *coupon.Coupon) error {
	m := toCouponModel(c)
	_, err := s.mdb.NewInsert(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: create coupon: %w", err)
	}
	return nil
}

func (s *Store) GetCoupon(ctx context.Context, code, appID string) (*coupon.Coupon, error) {
	var m couponModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{"code": code, "app_id": appID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrCouponNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get coupon: %w", err)
	}
	return fromCouponModel(&m)
}

func (s *Store) GetCouponByID(ctx context.Context, couponID id.CouponID) (*coupon.Coupon, error) {
	var m couponModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{"_id": couponID.String()}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrCouponNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get coupon by id: %w", err)
	}
	return fromCouponModel(&m)
}

func (s *Store) ListCoupons(ctx context.Context, appID string, opts coupon.ListOpts) ([]*coupon.Coupon, error) {
	var models []couponModel

	filter := bson.M{"app_id": appID}
	if opts.Active {
		t := time.Now().UTC()
		filter["$and"] = bson.A{
			bson.M{"$or": bson.A{
				bson.M{"valid_from": bson.M{"$exists": false}},
				bson.M{"valid_from": nil},
				bson.M{"valid_from": bson.M{"$lte": t}},
			}},
			bson.M{"$or": bson.A{
				bson.M{"valid_until": bson.M{"$exists": false}},
				bson.M{"valid_until": nil},
				bson.M{"valid_until": bson.M{"$gte": t}},
			}},
		}
	}

	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}
	if opts.Offset > 0 {
		q = q.Skip(int64(opts.Offset))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("ledger/mongo: list coupons: %w", err)
	}

	result := make([]*coupon.Coupon, len(models))
	for i := range models {
		c, err := fromCouponModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = c
	}
	return result, nil
}

func (s *Store) UpdateCoupon(ctx context.Context, c *coupon.Coupon) error {
	m := toCouponModel(c)
	m.UpdatedAt = now()

	_, err := s.mdb.NewUpdate(m).
		Filter(bson.M{"_id": m.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: update coupon: %w", err)
	}
	return nil
}

func (s *Store) DeleteCoupon(ctx context.Context, couponID id.CouponID) error {
	res, err := s.mdb.NewDelete((*couponModel)(nil)).
		Filter(bson.M{"_id": couponID.String()}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: delete coupon: %w", err)
	}
	if res.DeletedCount() == 0 {
		return ledger.ErrCouponNotFound
	}
	return nil
}

// ==================== Helpers ====================

// now returns the current UTC time.
func now() time.Time {
	return time.Now().UTC()
}

// getStartOfPeriod returns the start of the given period.
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

// isNoDocuments checks if an error wraps mongo.ErrNoDocuments.
func isNoDocuments(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}

// migrationIndexes returns the index definitions for all ledger collections.
func migrationIndexes() map[string][]mongo.IndexModel {
	return map[string][]mongo.IndexModel{
		colPlans: {
			{
				Keys:    bson.D{{Key: "slug", Value: 1}, {Key: "app_id", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "created_at", Value: 1}}},
		},
		colSubscriptions: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "created_at", Value: -1}}},
			{Keys: bson.D{{Key: "plan_id", Value: 1}}},
		},
		colUsageEvents: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "feature_key", Value: 1}, {Key: "timestamp", Value: -1}}},
			{Keys: bson.D{{Key: "timestamp", Value: -1}}},
			{
				Keys:    bson.D{{Key: "idempotency_key", Value: 1}},
				Options: options.Index().SetUnique(true).SetSparse(true),
			},
		},
		colEntitlements: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}}},
			{Keys: bson.D{{Key: "expires_at", Value: 1}}},
		},
		colInvoices: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "created_at", Value: -1}}},
			{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "app_id", Value: 1}, {Key: "period_start", Value: 1}, {Key: "period_end", Value: 1}}},
			{Keys: bson.D{{Key: "subscription_id", Value: 1}}},
		},
		colCoupons: {
			{
				Keys:    bson.D{{Key: "code", Value: 1}, {Key: "app_id", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "created_at", Value: -1}}},
		},
	}
}
