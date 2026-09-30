package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"
	"github.com/xraph/grove/migrate"
	modernsqlite "modernc.org/sqlite"

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

// compile-time interface check
var _ ledgerstore.Store = (*Store)(nil)

// Store implements store.Store using SQLite via Grove ORM.
type Store struct {
	db  *grove.DB
	sdb *sqlitedriver.SqliteDB
}

// New creates a new SQLite store backed by Grove ORM.
func New(db *grove.DB) *Store {
	return &Store{
		db:  db,
		sdb: sqlitedriver.Unwrap(db),
	}
}

// DB returns the underlying grove database for direct access.
func (s *Store) DB() *grove.DB { return s.db }

// Migrate creates the required tables and indexes using the grove orchestrator.
func (s *Store) Migrate(ctx context.Context) error {
	executor, err := migrate.NewExecutorFor(s.sdb)
	if err != nil {
		return fmt.Errorf("ledger/sqlite: create migration executor: %w", err)
	}
	orch := migrate.NewOrchestrator(executor, Migrations)
	if _, err := orch.Migrate(ctx); err != nil {
		return fmt.Errorf("ledger/sqlite: migration failed: %w", err)
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
	_, err := s.sdb.NewInsert(m).Exec(ctx)
	return err
}

func (s *Store) GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error) {
	m := new(planModel)
	err := s.sdb.NewSelect(m).
		Where("id = ?", planID.String()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrPlanNotFound
		}
		return nil, err
	}
	return fromPlanModel(m)
}

func (s *Store) GetPlanBySlug(ctx context.Context, slug, appID string) (*plan.Plan, error) {
	m := new(planModel)
	err := s.sdb.NewSelect(m).
		Where("slug = ?", slug).
		Where("app_id = ?", appID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrPlanNotFound
		}
		return nil, err
	}
	return fromPlanModel(m)
}

func (s *Store) ListPlans(ctx context.Context, appID string, opts plan.ListOpts) ([]*plan.Plan, error) {
	var models []planModel
	q := s.sdb.NewSelect(&models)

	if appID != "" {
		q = q.Where("app_id = ?", appID)
	}
	if opts.Status != "" {
		q = q.Where("status = ?", string(opts.Status))
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at ASC, id ASC")

	if err := q.Scan(ctx); err != nil {
		return nil, err
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
	res, err := s.sdb.NewUpdate(m).WherePK().Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrPlanNotFound
	}
	return nil
}

func (s *Store) DeletePlan(ctx context.Context, planID id.PlanID) error {
	res, err := s.sdb.NewDelete((*planModel)(nil)).
		Where("id = ?", planID.String()).
		Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrPlanNotFound
	}
	return nil
}

func (s *Store) ArchivePlan(ctx context.Context, planID id.PlanID) error {
	t := now()
	res, err := s.sdb.NewUpdate((*planModel)(nil)).
		Set("status = ?", string(plan.StatusArchived)).
		Set("updated_at = ?", t).
		Where("id = ?", planID.String()).
		Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrPlanNotFound
	}
	return nil
}

// ==================== Feature Store ====================

func (s *Store) CreateFeature(ctx context.Context, f *feature.Feature) error {
	m := toFeatureModel(f)
	_, err := s.sdb.NewInsert(m).Exec(ctx)
	return err
}

func (s *Store) GetFeature(ctx context.Context, featureID id.FeatureID) (*feature.Feature, error) {
	m := new(featureModel)
	err := s.sdb.NewSelect(m).
		Where("id = ?", featureID.String()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrFeatureNotFound
		}
		return nil, err
	}
	return fromFeatureModel(m)
}

func (s *Store) GetFeatureByKey(ctx context.Context, key, appID string) (*feature.Feature, error) {
	m := new(featureModel)
	err := s.sdb.NewSelect(m).
		Where("key = ?", key).
		Where("app_id = ?", appID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrFeatureNotFound
		}
		return nil, err
	}
	return fromFeatureModel(m)
}

func (s *Store) ListFeatures(ctx context.Context, appID string, opts feature.ListOpts) ([]*feature.Feature, error) {
	var models []featureModel
	q := s.sdb.NewSelect(&models)

	if appID != "" {
		q = q.Where("app_id = ?", appID)
	}
	if opts.Status != "" {
		q = q.Where("status = ?", string(opts.Status))
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at ASC, id ASC")

	if err := q.Scan(ctx); err != nil {
		return nil, err
	}

	result := make([]*feature.Feature, len(models))
	for i := range models {
		f, err := fromFeatureModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = f
	}
	return result, nil
}

func (s *Store) ListGlobalFeatures(ctx context.Context, opts feature.ListOpts) ([]*feature.Feature, error) {
	var models []featureModel
	q := s.sdb.NewSelect(&models).Where("app_id = ?", "")

	if opts.Status != "" {
		q = q.Where("status = ?", string(opts.Status))
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at ASC, id ASC")

	if err := q.Scan(ctx); err != nil {
		return nil, err
	}

	result := make([]*feature.Feature, len(models))
	for i := range models {
		f, err := fromFeatureModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = f
	}
	return result, nil
}

func (s *Store) UpdateFeature(ctx context.Context, f *feature.Feature) error {
	m := toFeatureModel(f)
	m.UpdatedAt = now()
	res, err := s.sdb.NewUpdate(m).WherePK().Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrFeatureNotFound
	}
	return nil
}

func (s *Store) DeleteFeature(ctx context.Context, featureID id.FeatureID) error {
	res, err := s.sdb.NewDelete((*featureModel)(nil)).
		Where("id = ?", featureID.String()).
		Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrFeatureNotFound
	}
	return nil
}

func (s *Store) ArchiveFeature(ctx context.Context, featureID id.FeatureID) error {
	t := now()
	res, err := s.sdb.NewUpdate((*featureModel)(nil)).
		Set("status = ?", string(feature.StatusArchived)).
		Set("updated_at = ?", t).
		Where("id = ?", featureID.String()).
		Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrFeatureNotFound
	}
	return nil
}

// ==================== Subscription Store ====================

func (s *Store) CreateSubscription(ctx context.Context, sub *subscription.Subscription) error {
	m := toSubscriptionModel(sub)
	_, err := s.sdb.NewInsert(m).Exec(ctx)
	return err
}

func (s *Store) GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	m := new(subscriptionModel)
	err := s.sdb.NewSelect(m).
		Where("id = ?", subID.String()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrSubscriptionNotFound
		}
		return nil, err
	}
	return fromSubscriptionModel(m)
}

func (s *Store) GetActiveSubscription(ctx context.Context, tenantID, appID string) (*subscription.Subscription, error) {
	m := new(subscriptionModel)
	err := s.sdb.NewSelect(m).
		Where("tenant_id = ?", tenantID).
		Where("app_id = ?", appID).
		Where("status IN (?, ?)", string(subscription.StatusActive), string(subscription.StatusTrialing)).
		OrderExpr("created_at DESC, id DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrNoActiveSubscription
		}
		return nil, err
	}
	return fromSubscriptionModel(m)
}

func (s *Store) ListSubscriptions(ctx context.Context, tenantID, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error) {
	var models []subscriptionModel
	q := s.sdb.NewSelect(&models)

	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if appID != "" {
		q = q.Where("app_id = ?", appID)
	}
	if opts.Status != "" {
		q = q.Where("status = ?", string(opts.Status))
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at DESC, id DESC")

	if err := q.Scan(ctx); err != nil {
		return nil, err
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
	res, err := s.sdb.NewUpdate(m).WherePK().Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrSubscriptionNotFound
	}
	return nil
}

func (s *Store) CancelSubscription(ctx context.Context, subID id.SubscriptionID, immediately bool) (time.Time, error) {
	t := now()
	updates := s.sdb.NewUpdate((*subscriptionModel)(nil))
	if immediately {
		updates = updates.
			Set("cancel_at = ?", t).
			Set("status = ?", string(subscription.StatusCanceled)).
			Set("canceled_at = ?", t)
	} else {
		// The end of the period current when this statement runs, read by
		// the statement itself: a period the clock advanced since the
		// caller's read is the one that ends.
		updates = updates.Set("cancel_at = current_period_end")
	}

	var cancelAt textTime
	err := updates.
		Set("updated_at = ?", t).
		Where("id = ?", subID.String()).
		Where("status NOT IN (?, ?)", string(subscription.StatusCanceled), string(subscription.StatusExpired)).
		Returning("cancel_at").
		Scan(ctx, &cancelAt)
	if isNoRows(err) {
		return time.Time{}, s.cancelMissed(ctx, subID)
	}
	if err != nil {
		return time.Time{}, err
	}
	return cancelAt.t.UTC(), nil
}

// ==================== Meter Store ====================

func (s *Store) IngestBatch(ctx context.Context, events []*meter.UsageEvent) error {
	if len(events) == 0 {
		return nil
	}
	models := make([]usageEventModel, len(events))
	for i, e := range events {
		models[i] = *toUsageEventModel(e)
	}
	_, err := s.sdb.NewInsert(&models).
		OnConflict("(idempotency_key) WHERE idempotency_key != '' DO NOTHING").
		Exec(ctx)
	return err
}

func (s *Store) Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error) {
	// The period start is an instant, so it is bound in UTC: SQLite compares
	// these timestamps as text, and every stored one is UTC.
	//
	// It is inclusive, matching the half-open [Start, End) window QueryUsage
	// applies: an event stamped exactly at the start of the period belongs to
	// it. The start is computed from time.Now(), so a test cannot place an
	// event on that instant without an injectable clock, and none is
	// invented here.
	startOfPeriod := getStartOfPeriod(time.Now(), period).UTC()

	var total int64
	err := s.sdb.NewRaw(`
		SELECT COALESCE(SUM(quantity), 0) FROM ledger_usage_events
		WHERE tenant_id = ? AND app_id = ? AND feature_key = ? AND timestamp >= ?
	`, tenantID, appID, featureKey, startOfPeriod).Scan(ctx, &total)
	if err != nil {
		return 0, err
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

func (s *Store) QueryUsage(ctx context.Context, tenantID, appID string, opts meter.QueryOpts) ([]*meter.UsageEvent, error) {
	var models []usageEventModel
	q := s.sdb.NewSelect(&models)

	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if appID != "" {
		q = q.Where("app_id = ?", appID)
	}

	if opts.FeatureKey != "" {
		q = q.Where("feature_key = ?", opts.FeatureKey)
	}
	// The window is half-open, [Start, End): an event stamped exactly at
	// Start is inside it and one stamped exactly at End belongs to the next
	// billing period. SQLite compares these timestamps as text, so the bounds
	// are put in UTC, the zone events are stored in, before they are bound.
	if !opts.Start.IsZero() {
		q = q.Where("timestamp >= ?", opts.Start.UTC())
	}
	if !opts.End.IsZero() {
		q = q.Where("timestamp < ?", opts.End.UTC())
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("timestamp DESC, id DESC")

	if err := q.Scan(ctx); err != nil {
		return nil, err
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
	res, err := s.sdb.NewDelete((*usageEventModel)(nil)).
		Where("timestamp < ?", before.UTC()).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return rows, nil
}

// ==================== Entitlement Cache Store ====================

func (s *Store) GetCached(ctx context.Context, tenantID, appID, featureKey string) (*entitlement.Result, error) {
	m := new(entitlementCacheModel)
	cacheKey := tenantID + ":" + appID + ":" + featureKey
	err := s.sdb.NewSelect(m).
		Where("cache_key = ?", cacheKey).
		Where("expires_at > ?", time.Now().UTC()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrCacheMiss
		}
		return nil, err
	}
	return fromEntitlementCacheModel(m), nil
}

func (s *Store) SetCached(ctx context.Context, tenantID, appID, featureKey string, result *entitlement.Result, ttl time.Duration) error {
	expiresAt := time.Now().UTC().Add(ttl)
	m := toEntitlementCacheModel(tenantID, appID, featureKey, result, expiresAt)
	_, err := s.sdb.NewInsert(m).
		OnConflict("(cache_key) DO UPDATE").
		Set("allowed = EXCLUDED.allowed").
		Set("feature = EXCLUDED.feature").
		Set("used = EXCLUDED.used").
		Set("cache_limit = EXCLUDED.cache_limit").
		Set("remaining = EXCLUDED.remaining").
		Set("soft_limit = EXCLUDED.soft_limit").
		Set("reason = EXCLUDED.reason").
		Set("expires_at = EXCLUDED.expires_at").
		Set("created_at = EXCLUDED.created_at").
		Exec(ctx)
	return err
}

func (s *Store) Invalidate(ctx context.Context, tenantID, appID string) error {
	_, err := s.sdb.NewDelete((*entitlementCacheModel)(nil)).
		Where("tenant_id = ?", tenantID).
		Where("app_id = ?", appID).
		Exec(ctx)
	return err
}

func (s *Store) InvalidateFeature(ctx context.Context, tenantID, appID, featureKey string) error {
	cacheKey := tenantID + ":" + appID + ":" + featureKey
	_, err := s.sdb.NewDelete((*entitlementCacheModel)(nil)).
		Where("cache_key = ?", cacheKey).
		Exec(ctx)
	return err
}

// ==================== Invoice Store ====================

func (s *Store) CreateInvoice(ctx context.Context, inv *invoice.Invoice) error {
	m := toInvoiceModel(inv)
	_, err := s.sdb.NewInsert(m).Exec(ctx)
	return err
}

func (s *Store) GetInvoice(ctx context.Context, invID id.InvoiceID) (*invoice.Invoice, error) {
	m := new(invoiceModel)
	err := s.sdb.NewSelect(m).
		Where("id = ?", invID.String()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrInvoiceNotFound
		}
		return nil, err
	}
	return fromInvoiceModel(m)
}

func (s *Store) ListInvoices(ctx context.Context, tenantID, appID string, opts invoice.ListOpts) ([]*invoice.Invoice, error) {
	var models []invoiceModel
	q := s.sdb.NewSelect(&models)

	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if appID != "" {
		q = q.Where("app_id = ?", appID)
	}

	if opts.Status != "" {
		q = q.Where("status = ?", string(opts.Status))
	}
	if !opts.Start.IsZero() {
		q = q.Where("period_start >= ?", opts.Start.UTC())
	}
	if !opts.End.IsZero() {
		q = q.Where("period_end <= ?", opts.End.UTC())
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at DESC, id DESC")

	if err := q.Scan(ctx); err != nil {
		return nil, err
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
	res, err := s.sdb.NewUpdate(m).WherePK().Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrInvoiceNotFound
	}
	return nil
}

func (s *Store) GetInvoiceByPeriod(ctx context.Context, tenantID, appID string, periodStart, periodEnd time.Time) (*invoice.Invoice, error) {
	m := new(invoiceModel)
	err := s.sdb.NewSelect(m).
		Where("tenant_id = ?", tenantID).
		Where("app_id = ?", appID).
		Where("period_start = ?", periodStart.UTC()).
		Where("period_end = ?", periodEnd.UTC()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrInvoiceNotFound
		}
		return nil, err
	}
	return fromInvoiceModel(m)
}

func (s *Store) ListPendingInvoices(ctx context.Context, appID string) ([]*invoice.Invoice, error) {
	var models []invoiceModel
	q := s.sdb.NewSelect(&models)

	if appID != "" {
		q = q.Where("app_id = ?", appID)
	}
	err := q.Where("status = ?", string(invoice.StatusPending)).
		OrderExpr("created_at DESC, id DESC").
		Scan(ctx)
	if err != nil {
		return nil, err
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
	res, err := s.sdb.NewUpdate((*invoiceModel)(nil)).
		Set("status = ?", string(invoice.StatusPaid)).
		Set("paid_at = ?", paidAt.UTC()).
		Set("payment_ref = ?", paymentRef).
		Set("updated_at = ?", t).
		Where("id = ?", invID.String()).
		Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrInvoiceNotFound
	}
	return nil
}

func (s *Store) MarkInvoiceVoided(ctx context.Context, invID id.InvoiceID, reason string) error {
	t := now()
	res, err := s.sdb.NewUpdate((*invoiceModel)(nil)).
		Set("status = ?", string(invoice.StatusVoided)).
		Set("voided_at = ?", t).
		Set("void_reason = ?", reason).
		Set("updated_at = ?", t).
		Where("id = ?", invID.String()).
		Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrInvoiceNotFound
	}
	return nil
}

// ==================== Coupon Store ====================

func (s *Store) CreateCoupon(ctx context.Context, c *coupon.Coupon) error {
	m := toCouponModel(c)
	_, err := s.sdb.NewInsert(m).Exec(ctx)
	return err
}

func (s *Store) GetCoupon(ctx context.Context, code, appID string) (*coupon.Coupon, error) {
	m := new(couponModel)
	err := s.sdb.NewSelect(m).
		Where("code = ?", code).
		Where("app_id = ?", appID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrCouponNotFound
		}
		return nil, err
	}
	return fromCouponModel(m)
}

func (s *Store) GetCouponByID(ctx context.Context, couponID id.CouponID) (*coupon.Coupon, error) {
	m := new(couponModel)
	err := s.sdb.NewSelect(m).
		Where("id = ?", couponID.String()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, ledger.ErrCouponNotFound
		}
		return nil, err
	}
	return fromCouponModel(m)
}

func (s *Store) ListCoupons(ctx context.Context, appID string, opts coupon.ListOpts) ([]*coupon.Coupon, error) {
	var models []couponModel
	q := s.sdb.NewSelect(&models)

	if appID != "" {
		q = q.Where("app_id = ?", appID)
	}

	if opts.Active {
		t := time.Now().UTC()
		q = q.Where("(valid_from IS NULL OR valid_from <= ?)", t).
			Where("(valid_until IS NULL OR valid_until >= ?)", t)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at DESC, id DESC")

	if err := q.Scan(ctx); err != nil {
		return nil, err
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

// couponUpdateColumns lists every coupon column UpdateCoupon writes. It leaves
// out the primary key, which WherePK matches on, and times_redeemed, which only
// RedeemCoupon and IncrementCouponRedemptions may change: a caller holding a
// stale copy of the coupon must not roll the count back.
var couponUpdateColumns = []string{
	"code", "name", "type", "amount_cents", "amount_currency", "percentage",
	"currency", "max_redemptions", "valid_from", "valid_until", "app_id",
	"metadata", "created_at", "updated_at",
}

func (s *Store) UpdateCoupon(ctx context.Context, c *coupon.Coupon) error {
	m := toCouponModel(c)
	m.UpdatedAt = now()
	res, err := s.sdb.NewUpdate(m).Column(couponUpdateColumns...).WherePK().Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrCouponNotFound
	}
	return nil
}

func (s *Store) DeleteCoupon(ctx context.Context, couponID id.CouponID) error {
	res, err := s.sdb.NewDelete((*couponModel)(nil)).
		Where("id = ?", couponID.String()).
		Exec(ctx)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
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

	// Confirm the coupon exists before writing a row that references it.
	// SQLite is not enforcing a foreign key here, so this is the only
	// thing between a bad ID and an orphaned application row.
	if _, err := s.GetCouponByID(ctx, couponID); err != nil {
		return err
	}

	m := &couponApplicationModel{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       couponID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	if _, err := s.sdb.NewInsert(m).Exec(ctx); err != nil {
		if isUniqueViolation(err) {
			return ledger.ErrCouponAlreadyApplied
		}
		return err
	}

	return nil
}

func (s *Store) ListAppliedCoupons(ctx context.Context, subID id.SubscriptionID) ([]*coupon.Coupon, error) {
	if subID.IsNil() {
		return make([]*coupon.Coupon, 0), nil
	}

	var models []couponApplicationModel
	err := s.sdb.NewSelect(&models).
		Where("subscription_id = ?", subID.String()).
		OrderExpr("applied_at ASC, id ASC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}

	// Empty rather than nil: a subscription with no coupons is a valid
	// answer, not a missing one.
	result := make([]*coupon.Coupon, 0, len(models))
	for i := range models {
		couponID, parseErr := id.ParseCouponID(models[i].CouponID)
		if parseErr != nil {
			return nil, parseErr
		}

		c, getErr := s.GetCouponByID(ctx, couponID)
		if getErr != nil {
			if errors.Is(getErr, ledger.ErrCouponNotFound) {
				// A deleted coupon can leave its application row
				// behind. Skip it rather than failing the whole
				// read: the subscription is still valid and the
				// operator needs the rest of its coupons.
				continue
			}
			// Any other error (a dropped connection, a cancelled
			// context) must not be treated as "this coupon is
			// gone" - that would silently drop a discount the
			// customer is still entitled to.
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
	res, err := s.sdb.NewUpdate((*couponModel)(nil)).
		Set("times_redeemed = times_redeemed + 1").
		Set("updated_at = ?", now()).
		Where("id = ?", couponID.String()).
		Exec(ctx)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ledger.ErrCouponNotFound
	}

	return nil
}

// RedeemCoupon records a coupon's application to a subscription and
// increments its redemption count in one transaction, committing only when
// both writes land. The cap (MaxRedemptions) is enforced by a conditional
// UPDATE - `times_redeemed = times_redeemed + 1 WHERE id = ? AND
// (max_redemptions <= 0 OR times_redeemed < max_redemptions)` - rather than
// a read of TimesRedeemed followed by a separate write, so two concurrent
// transactions racing the same coupon toward its cap cannot both read the
// count before either writes it: the second transaction's UPDATE blocks
// until the first commits or rolls back, and its WHERE then re-evaluates
// against whatever count the first one left behind. SQLite has no row-level
// locking - a writing transaction holds a database-wide RESERVED/EXCLUSIVE
// lock, not a lock on this one row - but the effect on this method is the
// same: the second UPDATE waits rather than racing the first's read.
//
// The INSERT of the application row is deliberately the FIRST statement in
// the transaction, not a SELECT. A transaction that opens with a read
// starts deferred and only holds a SHARED lock; if it later needs to write,
// SQLite must upgrade that SHARED lock to RESERVED, and two transactions
// racing the same coupon can both be sitting on SHARED when they attempt
// that upgrade at the same time. SQLite treats that as an unresolvable
// mutual wait and fails it immediately with SQLITE_BUSY rather than queuing
// it behind busy_timeout - regardless of how long busy_timeout is set to,
// and regardless of DSN flags. Opening with a write instead means the
// RESERVED lock is acquired up front, with no later upgrade to fail. This
// is why the coupon's existence is no longer confirmed with a SELECT before
// the INSERT: that SELECT was the read this method used to open with.
//
// Unknown coupon and exhausted cap now share one signal - the conditional
// UPDATE affects zero rows - and are told apart only after the fact: zero
// rows means either the coupon was never there or its cap was reached, and
// an existence check run at that point (with the write lock already held
// by the INSERT above, so it cannot itself trigger a lock-upgrade BUSY)
// decides which.
func (s *Store) RedeemCoupon(ctx context.Context, subID id.SubscriptionID, couponID id.CouponID) error {
	if err := validateSubscriptionID(subID); err != nil {
		return err
	}

	tx, err := s.sdb.BeginTxQuery(ctx, nil)
	if err != nil {
		return err
	}
	// Rollback after a successful Commit does not undo it - there is
	// nothing left to roll back - but it is not a silent no-op either: on
	// sqlite the underlying *sql.Tx returns sql.ErrTxDone, which this
	// unconditional defer discards with `_ =`. That discard is deliberate
	// on every path, not just the success one: Commit itself can fail
	// partway through (for instance if the connection drops after this
	// method's own UPDATE but before the commit lands), leaving the
	// transaction open, and the same Rollback call is what cleans that up.
	defer func() { _ = tx.Rollback() }()

	appModel := &couponApplicationModel{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       couponID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	if _, err := tx.NewInsert(appModel).Exec(ctx); err != nil {
		if isUniqueViolation(err) {
			return ledger.ErrCouponAlreadyApplied
		}
		return err
	}

	res, err := tx.NewUpdate((*couponModel)(nil)).
		Set("times_redeemed = times_redeemed + 1").
		Set("updated_at = ?", now()).
		Where("id = ?", couponID.String()).
		Where("(max_redemptions <= 0 OR times_redeemed < max_redemptions)").
		Exec(ctx)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Zero rows is ambiguous by itself: either no coupon with this id
		// exists (sqlite enforces no foreign key from the application row,
		// so the insert above did not catch that), or one does and its cap
		// was reached. This SELECT runs inside the same transaction, after
		// the INSERT above has already taken the database's write lock, so
		// it is an ordinary read under a lock this transaction already
		// holds - not a second statement racing anyone for it.
		probe := new(couponModel)
		if selErr := tx.NewSelect(probe).Where("id = ?", couponID.String()).Scan(ctx); selErr != nil {
			if isNoRows(selErr) {
				return ledger.ErrCouponNotFound
			}
			return selErr
		}
		return ledger.ErrCouponExhausted
	}

	return tx.Commit()
}

// ==================== Helpers ====================

// now returns the current UTC time.
func now() time.Time {
	return time.Now().UTC()
}

// textTime scans a timestamp column read by a raw Scan, such as a RETURNING
// clause. SQLite stores these columns as TEXT and the driver hands them back
// as strings, which database/sql will not put into a time.Time; model scans
// go through grove's converter instead. The layouts are the ones grove tries,
// in its order: the first is time.Time.String(), the form the driver writes.
type textTime struct{ t time.Time }

var textTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	time.DateTime,
	time.DateOnly,
}

func (d *textTime) Scan(src any) error {
	var text string
	switch v := src.(type) {
	case time.Time:
		d.t = v
		return nil
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return fmt.Errorf("ledger/sqlite: cannot read %T as a timestamp", src)
	}
	for _, layout := range textTimeLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			d.t = t
			return nil
		}
	}
	return fmt.Errorf("ledger/sqlite: cannot parse %q as a timestamp", text)
}

// getStartOfPeriod returns the start of the calendar month or year that
// contains t, at midnight UTC. Billing periods are cut in UTC and every stored
// timestamp is UTC, so a usage window opens on the UTC calendar whatever zone
// the server runs in. A period of "none", or one it does not know, has no
// start: the zero time, which every event is after.
func getStartOfPeriod(t time.Time, period plan.Period) time.Time {
	t = t.UTC()
	switch period {
	case plan.PeriodMonthly:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case plan.PeriodYearly:
		return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Time{}
	}
}

// isNoRows checks for the standard sql.ErrNoRows sentinel.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// sqliteConstraintUnique is SQLITE_CONSTRAINT_UNIQUE, modernc.org/sqlite's
// extended result code for a UNIQUE index violation. This connection always
// has extended result codes enabled (modernc.org/sqlite turns them on for
// every connection it opens), so Code() reliably returns 2067 here rather
// than the generic SQLITE_CONSTRAINT (19). 1555
// (SQLITE_CONSTRAINT_PRIMARYKEY) is a different violation and must not
// match.
const sqliteConstraintUnique = 2067

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint
// violation, detected by the driver's typed extended result code rather
// than by matching its message text.
func isUniqueViolation(err error) bool {
	var sqliteErr *modernsqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteConstraintUnique
}

// validateSubscriptionID rejects a subscription id that is nil or carries
// the wrong prefix before any storage is touched.
func validateSubscriptionID(subID id.SubscriptionID) error {
	if subID.IsNil() || subID.Prefix() != id.PrefixSubscription {
		return fmt.Errorf("ledger/sqlite: invalid subscription id %q: %w", subID.String(), ledger.ErrInvalidInput)
	}
	return nil
}
