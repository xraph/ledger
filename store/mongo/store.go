package mongo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
)

// Collection name constants.
const (
	colPlans              = "ledger_plans"
	colSubscriptions      = "ledger_subscriptions"
	colUsageEvents        = "ledger_usage_events"
	colEntitlements       = "ledger_entitlement_cache"
	colInvoices           = "ledger_invoices"
	colCoupons            = "ledger_coupons"
	colCouponApplications = "ledger_coupon_applications"
	colFeatures           = "ledger_features"
)

// Index name constants for ledger_usage_events.idempotency_key.
//
// oldIdempotencyKeyIndexName is mongo's default auto-generated name for the
// original `{idempotency_key: 1}` unique+sparse index (field name and
// direction joined by "_"). A sparse index only skips documents missing the
// field entirely; it still enforces uniqueness among documents that carry
// it, even when every one of them stores an empty string (the insert path
// this repo goes through always writes idempotency_key, never omits it) -
// so the second keyless usage event ever ingested collides with the first
// and is silently dropped as "already ingested". newIdempotencyKeyIndexName
// replaces it with a PARTIAL unique index that only indexes non-empty keys,
// matching the design sqlite and postgres already use (a partial unique
// index filtered on a non-empty key, enforced with ON CONFLICT DO NOTHING).
// It has a new name because mongo refuses to create an index with the same
// keys as an existing one but different options.
const (
	oldIdempotencyKeyIndexName = "idempotency_key_1"
	newIdempotencyKeyIndexName = "idempotency_key_unique_partial"
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

// Migrate creates indexes for all ledger collections, then drops any index
// a new one has superseded (currently just the old sparse idempotency_key
// index on ledger_usage_events, replaced by a partial unique one).
//
// It creates every new index BEFORE dropping anything superseded, never the
// other way around: verified live against MongoDB 7.0.41 that a partial
// index can be created while an old index with the same key pattern still
// exists, so there is no need to ever leave a collection with a moment of
// zero uniqueness protection. Dropping first would open exactly that gap -
// a duplicate key written into it would defeat the very check being
// installed, permanently, since the create that would normally catch it
// never runs against that data. A collection whose superseded index failed
// to get dropped (index-not-found aside) simply keeps both the old and new
// index; a collection whose NEW index failed to get created (most likely
// because existing documents already violate it, an E11000) is reported as
// a failure via the joined error, and its data is never touched.
//
// An index listed in migrationUniqueIndexes is created in its own call,
// separate from the rest of its collection's indexes (which share one
// CreateMany): a duplicate-key failure building that one index must not
// also prevent the collection's other, unrelated indexes from landing.
//
// Every collection is attempted, in a fixed (sorted) order, even after an
// earlier one fails: a bad collection must not leave every collection after
// it in the iteration order unindexed. All failures are returned together
// via errors.Join.
func (s *Store) Migrate(ctx context.Context) error {
	indexes := migrationIndexes()
	uniqueIndexes := migrationUniqueIndexes()
	cols := unionSortedIndexKeys(indexes, uniqueIndexes)

	var errs []error
	created := make(map[string]bool, len(cols))

	for _, col := range cols {
		ok := true

		if models := indexes[col]; len(models) > 0 {
			if _, err := s.mdb.Collection(col).Indexes().CreateMany(ctx, models); err != nil {
				if isDuplicateKeyError(err) {
					errs = append(errs, duplicateKeyMigrateError(col, err))
				} else {
					errs = append(errs, fmt.Errorf("ledger/mongo: migrate: create %s indexes: %w", col, err))
				}
				ok = false
			}
		}

		// Each index that might collide with pre-existing duplicate data is
		// created in its OWN call, separate from the collection's other
		// indexes above: a duplicate-key failure building THIS index alone
		// must not also prevent the collection's other, perfectly fine
		// indexes from being created.
		for _, m := range uniqueIndexes[col] {
			if _, err := s.mdb.Collection(col).Indexes().CreateOne(ctx, m); err != nil {
				if isDuplicateKeyError(err) {
					errs = append(errs, duplicateKeyMigrateError(col, err))
				} else {
					errs = append(errs, fmt.Errorf("ledger/mongo: migrate: create %s indexes: %w", col, err))
				}
				ok = false
			}
		}

		created[col] = ok
	}

	supersededByCol := migrationSupersededIndexes()
	supersededCols := make([]string, 0, len(supersededByCol))
	for col := range supersededByCol {
		supersededCols = append(supersededCols, col)
	}
	sort.Strings(supersededCols)

	for _, col := range supersededCols {
		if !created[col] {
			// This collection's replacement index didn't get created above
			// (or there was nothing to create for it, which can't happen
			// here since every superseded-index collection also appears in
			// migrationIndexes()). Leave its old index alone rather than
			// dropping protection a replacement never took over.
			continue
		}
		for _, name := range supersededByCol[col] {
			if err := s.mdb.Collection(col).Indexes().DropOne(ctx, name); err != nil {
				if !isIndexNotFound(err) {
					errs = append(errs, fmt.Errorf("ledger/mongo: migrate: drop superseded %s index on %q: %w", name, col, err))
				}
			}
		}
	}

	return errors.Join(errs...)
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

	filter := bson.M{}
	if appID != "" {
		filter["app_id"] = appID
	}
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

	filter := bson.M{}
	if tenantID != "" {
		filter["tenant_id"] = tenantID
	}
	if appID != "" {
		filter["app_id"] = appID
	}
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

	if !cancelAt.After(time.Now()) {
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
	// The period start is inclusive, matching the half-open [Start, End)
	// window QueryUsage applies: an event stamped exactly at the start of the
	// period belongs to it. The start is computed from time.Now(), so a test
	// cannot place an event on that instant without an injectable clock, and
	// none is invented here.
	startOfPeriod := getStartOfPeriod(time.Now(), period)

	pipeline := bson.A{
		bson.M{
			"$match": bson.M{
				"tenant_id":   tenantID,
				"app_id":      appID,
				"feature_key": featureKey,
				"timestamp":   bson.M{"$gte": startOfPeriod},
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

	filter := bson.M{}
	if tenantID != "" {
		filter["tenant_id"] = tenantID
	}
	if appID != "" {
		filter["app_id"] = appID
	}
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
			// Half-open window, [Start, End): an event exactly at End belongs
			// to the next billing period.
			ts["$lt"] = opts.End
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

	filter := bson.M{}
	if tenantID != "" {
		filter["tenant_id"] = tenantID
	}
	if appID != "" {
		filter["app_id"] = appID
	}
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

	filter := bson.M{"status": string(invoice.StatusPending)}
	if appID != "" {
		filter["app_id"] = appID
	}

	err := s.mdb.NewFind(&models).
		Filter(filter).
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

	filter := bson.M{}
	if appID != "" {
		filter["app_id"] = appID
	}
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

	// Every field but _id and times_redeemed. The redemption count belongs to
	// RedeemCoupon and IncrementCouponRedemptions, so a stale copy of the
	// coupon cannot roll it back. A nil validity bound is written as null,
	// which is how an edit clears it.
	_, err := s.mdb.NewUpdate(m).
		Filter(bson.M{"_id": m.ID}).
		SetUpdate(bson.M{"$set": bson.M{
			"code":            m.Code,
			"name":            m.Name,
			"type":            m.Type,
			"amount_cents":    m.AmountCents,
			"amount_currency": m.AmountCurrency,
			"percentage":      m.Percentage,
			"currency":        m.Currency,
			"max_redemptions": m.MaxRedemptions,
			"valid_from":      m.ValidFrom,
			"valid_until":     m.ValidUntil,
			"app_id":          m.AppID,
			"metadata":        m.Metadata,
			"created_at":      m.CreatedAt,
			"updated_at":      m.UpdatedAt,
		}}).
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

// ApplyCoupon is a low-level operation kept for callers and tests that need
// it separately. Engine code redeems through RedeemCoupon, which records
// the application and increments the redemption count as one unit.
func (s *Store) ApplyCoupon(ctx context.Context, subID id.SubscriptionID, couponID id.CouponID) error {
	if err := validateSubscriptionID(subID); err != nil {
		return err
	}

	if _, err := s.GetCouponByID(ctx, couponID); err != nil {
		return err
	}

	m := &couponApplicationDoc{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       couponID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	if _, err := s.mdb.NewInsert(m).Exec(ctx); err != nil {
		if isDuplicateKeyError(err) {
			return ledger.ErrCouponAlreadyApplied
		}
		return fmt.Errorf("ledger/mongo: apply coupon: %w", err)
	}

	return nil
}

func (s *Store) ListAppliedCoupons(ctx context.Context, subID id.SubscriptionID) ([]*coupon.Coupon, error) {
	if subID.IsNil() {
		return make([]*coupon.Coupon, 0), nil
	}

	var docs []couponApplicationDoc
	err := s.mdb.NewFind(&docs).
		Filter(bson.M{"subscription_id": subID.String()}).
		Sort(bson.D{{Key: "applied_at", Value: 1}, {Key: "_id", Value: 1}}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger/mongo: list applied coupons: %w", err)
	}

	// Empty rather than nil: a subscription with no coupons is a valid
	// answer, not a missing one.
	result := make([]*coupon.Coupon, 0, len(docs))
	for i := range docs {
		couponID, parseErr := id.ParseCouponID(docs[i].CouponID)
		if parseErr != nil {
			return nil, parseErr
		}

		c, getErr := s.GetCouponByID(ctx, couponID)
		if getErr != nil {
			if errors.Is(getErr, ledger.ErrCouponNotFound) {
				// A deleted coupon can leave its application behind.
				// Skip it rather than failing the read.
				continue
			}
			// Any other error (a dropped connection, a cancelled
			// context) must not be treated as "this coupon is gone"
			// - that would silently drop a discount the customer is
			// still entitled to.
			return nil, getErr
		}
		result = append(result, c)
	}

	return result, nil
}

// IncrementCouponRedemptions is a low-level operation kept for callers and
// tests that need it separately. Engine code redeems through RedeemCoupon,
// which increments the count and records the application as one unit.
func (s *Store) IncrementCouponRedemptions(ctx context.Context, couponID id.CouponID) error {
	res, err := s.mdb.NewUpdate((*couponModel)(nil)).
		Filter(bson.M{"_id": couponID.String()}).
		SetUpdate(bson.M{
			"$inc": bson.M{"times_redeemed": 1},
			"$set": bson.M{"updated_at": now()},
		}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: increment coupon redemptions: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrCouponNotFound
	}

	return nil
}

// RedeemCoupon records a coupon's application to a subscription and
// increments its redemption count.
//
// This is compensating, not transactional: MongoDB multi-document
// transactions need a replica set, and the harness this store is tested
// against cannot assume one is available, so this method cannot open a
// session transaction the way the SQL backends open a database
// transaction. Instead it performs the two writes in sequence and undoes
// the first by hand when the second doesn't land as a success: insert the
// application row, then run the redemption-count increment as a single
// filtered update whose filter itself encodes the cap
// (`{max_redemptions: {$lte: 0}} OR {$expr: {$lt: [times_redeemed,
// max_redemptions]}}`), which MongoDB evaluates and applies atomically
// against that one document. A document update in MongoDB is always atomic
// per document, so the increment step alone is race-free the same way the
// SQL backends' conditional UPDATE is; what is not race-free is the gap
// between the two writes.
//
// That gap has real, observable consequences, not just a theoretical one:
//   - ListAppliedCoupons can return the application row before the count
//     is incremented, or after it was inserted but before a failed
//     increment's compensating delete has run.
//   - An invoice generated for this subscription inside that window
//     receives the coupon's discount even though the redemption was never
//     counted against the cap.
//   - A process crash inside the window (after the insert, before the
//     increment or its compensating delete) leaves the application row
//     permanently attached with no matching count, and nothing in this
//     store detects or repairs that on its own.
//   - An error from the increment step is itself ambiguous: MongoDB may
//     have applied it server-side and failed to report success back (a
//     dropped connection after the write, for instance), in which case the
//     compensating delete below runs against a coupon whose count already
//     moved, and this method still reports failure.
//
// Given no replica set to transact against, this is the closest available
// approximation, and it is unverified against a live MongoDB server that
// also runs the rest of this store's conformance suite concurrently - the
// RedeemCoupon subtests have been run once against a real mongod in a
// scratch database (see the task report), but not under sustained
// concurrent load the way sqlite and postgres have via -race.
func (s *Store) RedeemCoupon(ctx context.Context, subID id.SubscriptionID, couponID id.CouponID) error {
	if err := validateSubscriptionID(subID); err != nil {
		return err
	}

	if _, err := s.GetCouponByID(ctx, couponID); err != nil {
		return err
	}

	appDoc := &couponApplicationDoc{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       couponID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	if _, err := s.mdb.NewInsert(appDoc).Exec(ctx); err != nil {
		if isDuplicateKeyError(err) {
			return ledger.ErrCouponAlreadyApplied
		}
		return fmt.Errorf("ledger/mongo: redeem coupon: insert application: %w", err)
	}

	res, err := s.mdb.NewUpdate((*couponModel)(nil)).
		Filter(bson.M{
			"_id": couponID.String(),
			"$or": []bson.M{
				{"max_redemptions": bson.M{"$lte": 0}},
				{"$expr": bson.M{"$lt": bson.A{"$times_redeemed", "$max_redemptions"}}},
			},
		}).
		SetUpdate(bson.M{
			"$inc": bson.M{"times_redeemed": 1},
			"$set": bson.M{"updated_at": now()},
		}).
		Exec(ctx)
	if err != nil {
		return s.compensate(ctx, appDoc.ID, fmt.Errorf("ledger/mongo: redeem coupon: increment: %w", err))
	}
	if res.MatchedCount() == 0 {
		// Ambiguous, the same way the SQL backends' zero-rows result is:
		// either the cap was reached, or the coupon was deleted between
		// the existence check at the top of this method and this update -
		// there is no transaction here to prevent that. Re-check before
		// deciding which error to return.
		if _, getErr := s.GetCouponByID(ctx, couponID); getErr != nil {
			if errors.Is(getErr, ledger.ErrCouponNotFound) {
				return s.compensate(ctx, appDoc.ID, ledger.ErrCouponNotFound)
			}
			return s.compensate(ctx, appDoc.ID, getErr)
		}
		return s.compensate(ctx, appDoc.ID, ledger.ErrCouponExhausted)
	}

	return nil
}

// compensate deletes the application row RedeemCoupon inserted earlier in
// the same call and returns primary - unless the delete itself fails, in
// which case it returns an error that wraps both primary and the delete
// failure, so a caller checking errors.Is(err, ledger.ErrCouponExhausted)
// (or ErrCouponNotFound) still gets true even though the compensating
// delete also failed and left the application row behind.
func (s *Store) compensate(ctx context.Context, applicationID string, primary error) error {
	if delErr := s.deleteCouponApplication(ctx, applicationID); delErr != nil {
		return fmt.Errorf("ledger/mongo: redeem coupon: %w, and the compensating delete of the application row also failed: %w", primary, delErr)
	}
	return primary
}

// deleteCouponApplication removes a single coupon application row by its
// own id. It backs RedeemCoupon's compensating delete after an increment
// that failed or hit the cap.
//
// It runs with a context detached from the caller's (context.WithoutCancel)
// and a fresh 5-second timeout, not ctx itself. The most likely reason the
// increment above failed is that ctx was already cancelled or had timed
// out - in which case running the compensating delete on that same ctx
// would fail immediately too, leaving the application row permanently
// attached with no matching count: exactly the defect RedeemCoupon exists
// to prevent. The cleanup this method performs matters more than honoring
// a cancellation that has already doomed the request it was serving.
func (s *Store) deleteCouponApplication(ctx context.Context, applicationID string) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	_, err := s.mdb.NewDelete((*couponApplicationDoc)(nil)).
		Filter(bson.M{"_id": applicationID}).
		Exec(cctx)
	return err
}

// ==================== Feature Catalog Store ====================

func (s *Store) CreateFeature(ctx context.Context, f *feature.Feature) error {
	m := toFeatureCatalogModel(f)
	_, err := s.mdb.NewInsert(m).Exec(ctx)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ledger.ErrDuplicateFeature
		}
		return fmt.Errorf("ledger/mongo: create feature: %w", err)
	}
	return nil
}

func (s *Store) GetFeature(ctx context.Context, featureID id.FeatureID) (*feature.Feature, error) {
	var m featureCatalogModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{"_id": featureID.String()}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrFeatureNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get feature: %w", err)
	}
	return fromFeatureCatalogModel(&m)
}

func (s *Store) GetFeatureByKey(ctx context.Context, key, appID string) (*feature.Feature, error) {
	var m featureCatalogModel
	err := s.mdb.NewFind(&m).
		Filter(bson.M{"key": key, "app_id": appID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, ledger.ErrFeatureNotFound
		}
		return nil, fmt.Errorf("ledger/mongo: get feature by key: %w", err)
	}
	return fromFeatureCatalogModel(&m)
}

func (s *Store) ListFeatures(ctx context.Context, appID string, opts feature.ListOpts) ([]*feature.Feature, error) {
	var models []featureCatalogModel

	filter := bson.M{}
	if appID != "" {
		filter["app_id"] = appID
	}
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
		return nil, fmt.Errorf("ledger/mongo: list features: %w", err)
	}

	result := make([]*feature.Feature, len(models))
	for i := range models {
		f, err := fromFeatureCatalogModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = f
	}
	return result, nil
}

func (s *Store) ListGlobalFeatures(ctx context.Context, opts feature.ListOpts) ([]*feature.Feature, error) {
	var models []featureCatalogModel

	filter := bson.M{"app_id": ""}
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
		return nil, fmt.Errorf("ledger/mongo: list global features: %w", err)
	}

	result := make([]*feature.Feature, len(models))
	for i := range models {
		f, err := fromFeatureCatalogModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = f
	}
	return result, nil
}

func (s *Store) UpdateFeature(ctx context.Context, f *feature.Feature) error {
	m := toFeatureCatalogModel(f)
	m.UpdatedAt = now()

	res, err := s.mdb.NewUpdate(m).
		Filter(bson.M{"_id": m.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: update feature: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrFeatureNotFound
	}
	return nil
}

func (s *Store) DeleteFeature(ctx context.Context, featureID id.FeatureID) error {
	res, err := s.mdb.NewDelete((*featureCatalogModel)(nil)).
		Filter(bson.M{"_id": featureID.String()}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: delete feature: %w", err)
	}
	if res.DeletedCount() == 0 {
		return ledger.ErrFeatureNotFound
	}
	return nil
}

func (s *Store) ArchiveFeature(ctx context.Context, featureID id.FeatureID) error {
	t := now()
	res, err := s.mdb.NewUpdate((*featureCatalogModel)(nil)).
		Filter(bson.M{"_id": featureID.String()}).
		Set("status", string(feature.StatusArchived)).
		Set("updated_at", t).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: archive feature: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrFeatureNotFound
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

// isDuplicateKeyError reports whether err is a MongoDB duplicate key error
// (E11000), detected by the official driver's typed classification rather
// than by matching its message text.
func isDuplicateKeyError(err error) bool {
	return mongo.IsDuplicateKeyError(err)
}

// isIndexNotFound reports whether err is mongo's "index not found" (code 27)
// or "namespace not found" (code 26) server error. dropIndexes returns
// IndexNotFound when the collection exists but the named index does not,
// and NamespaceNotFound when the collection itself does not exist at all.
// Migrate creates every new index before dropping anything superseded, so
// by the time it drops a superseded index the collection has necessarily
// already been created (building an index implicitly creates its
// collection) - meaning IndexNotFound is the case Migrate actually
// exercises in normal operation, on both a fresh database (which never had
// the superseded index) and one already migrated under the new scheme
// (which no longer has it). NamespaceNotFound is tolerated too, defensively,
// in case a collection somehow still doesn't exist by drop time; either
// way, Migrate treats "there is nothing left to drop" as success.
func isIndexNotFound(err error) bool {
	if err == nil {
		return false
	}
	var se mongo.ServerError
	if errors.As(err, &se) {
		return se.HasErrorCode(27) || se.HasErrorCode(26)
	}
	return false
}

// validateSubscriptionID rejects a subscription id that is nil or carries
// the wrong prefix before any storage is touched.
func validateSubscriptionID(subID id.SubscriptionID) error {
	if subID.IsNil() || subID.Prefix() != id.PrefixSubscription {
		return fmt.Errorf("ledger/mongo: invalid subscription id %q: %w", subID.String(), ledger.ErrInvalidInput)
	}
	return nil
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
			// The idempotency_key partial unique index lives in
			// migrationUniqueIndexes, not here: see that function for why.
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
		colCouponApplications: {
			{
				Keys:    bson.D{{Key: "coupon_id", Value: 1}, {Key: "subscription_id", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{Keys: bson.D{{Key: "subscription_id", Value: 1}}},
		},
		colFeatures: {
			{
				Keys:    bson.D{{Key: "key", Value: 1}, {Key: "app_id", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "app_id", Value: 1}, {Key: "created_at", Value: 1}}},
		},
	}
}

// migrationSupersededIndexes lists, per collection, the names of indexes
// that an entry in migrationIndexes has replaced and Migrate should drop -
// but only after the replacement above has been created successfully, and
// only by name, never by deleting documents.
func migrationSupersededIndexes() map[string][]string {
	return map[string][]string{
		colUsageEvents: {oldIdempotencyKeyIndexName},
	}
}

// migrationUniqueIndexes returns, per collection, indexes that Migrate
// creates in their OWN CreateOne call rather than bundled into
// migrationIndexes' single CreateMany for that collection.
//
// This is specifically for an index whose build can fail against
// pre-existing data with an E11000 - the idempotency_key partial unique
// index is exactly that case, replacing an old sparse index that a
// database may carry duplicate-under-the-new-rules data past. Isolating it
// in its own call means a duplicate-key failure building THIS index alone
// does not also block ledger_usage_events' other, unrelated query indexes
// from being created.
func migrationUniqueIndexes() map[string][]mongo.IndexModel {
	return map[string][]mongo.IndexModel{
		colUsageEvents: {
			{
				Keys: bson.D{{Key: "idempotency_key", Value: 1}},
				Options: options.Index().
					SetName(newIdempotencyKeyIndexName).
					SetUnique(true).
					// $gt "" matches only non-empty strings: type bracketing
					// in mongo's BSON comparison order excludes null and
					// missing values from a $gt "" match, so this indexes
					// (and enforces uniqueness over) exactly the documents
					// with a genuine idempotency key, same as sqlite/postgres'
					// `WHERE idempotency_key != ''` partial unique index.
					SetPartialFilterExpression(bson.M{"idempotency_key": bson.M{"$gt": ""}}),
			},
		},
	}
}

// unionSortedIndexKeys returns every collection name appearing in any of
// the given maps, sorted, so Migrate visits collections in a fixed,
// deterministic order regardless of which map(s) mention them.
func unionSortedIndexKeys(maps ...map[string][]mongo.IndexModel) []string {
	seen := make(map[string]struct{})
	for _, m := range maps {
		for col := range m {
			seen[col] = struct{}{}
		}
	}
	cols := make([]string, 0, len(seen))
	for col := range seen {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	return cols
}

// duplicateKeyMigrateError builds the error Migrate returns when building
// an index fails because existing documents already hold duplicate values
// for it (a typed E11000, per isDuplicateKeyError).
//
// It must not claim an old index was "left in place" to protect the data:
// that is never true when this error can fire. A genuinely superseded old
// index (the sparse idempotency_key one, while it still exists) already
// enforces uniqueness over every document carrying a real key, which makes
// building the new index impossible to fail this way in the first place -
// so whenever this error DOES fire, either there was never an old index
// protecting this collection, or it was already gone by the time the
// conflicting documents were written. The same branch is reached for
// colPlans, colCoupons, colCouponApplications and colFeatures' own unique
// indexes too, none of which ever had a superseded predecessor at all - so
// the message says only what is true and checkable in every case: which
// collection, which index (when the raw driver error names one, which an
// index-build E11000 always does), that nothing was deleted, and what to
// do next.
func duplicateKeyMigrateError(col string, err error) error {
	if name := duplicateKeyIndexName(err); name != "" {
		return fmt.Errorf(
			"ledger/mongo: migrate: existing documents in %q hold duplicate values for %q; "+
				"no data was changed; resolve the duplicates and re-run Migrate: %w",
			col, name, err)
	}
	return fmt.Errorf(
		"ledger/mongo: migrate: existing documents in %q hold duplicate values under a new "+
			"unique index; no data was changed; resolve the duplicates and re-run Migrate: %w",
		col, err)
}

// duplicateKeyIndexName extracts the offending index's name from a mongo
// duplicate-key error's own message. An index-build E11000 always includes
// "index: <name> dup key: {...}" in its text (this is the server's own
// error format, not something this repo constructs), so this is read
// straight from the driver's error rather than tracked separately and
// risking drift from what the server actually rejected. Returns "" if the
// shape doesn't match, in which case the caller's message degrades to
// naming just the collection.
func duplicateKeyIndexName(err error) string {
	const marker = "index: "
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return ""
	}
	rest := msg[i+len(marker):]
	end := strings.IndexAny(rest, " \t\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
