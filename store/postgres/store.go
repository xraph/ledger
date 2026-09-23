package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/migrate"

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

// Store implements store.Store using PostgreSQL via Grove ORM.
type Store struct {
	db *grove.DB
	pg *pgdriver.PgDB
}

// New creates a new PostgreSQL store backed by Grove ORM.
func New(db *grove.DB) *Store {
	return &Store{
		db: db,
		pg: pgdriver.Unwrap(db),
	}
}

// DB returns the underlying grove database for direct access.
func (s *Store) DB() *grove.DB { return s.db }

// Migrate creates the required tables and indexes using the grove orchestrator.
func (s *Store) Migrate(ctx context.Context) error {
	executor, err := migrate.NewExecutorFor(s.pg)
	if err != nil {
		return fmt.Errorf("ledger/postgres: create migration executor: %w", err)
	}
	orch := migrate.NewOrchestrator(executor, Migrations)
	if _, err := orch.Migrate(ctx); err != nil {
		return fmt.Errorf("ledger/postgres: migration failed: %w", err)
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
	_, err := s.pg.NewInsert(m).Exec(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error) {
	m := new(planModel)
	err := s.pg.NewSelect(m).
		Where("id = $1", planID.String()).
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
	err := s.pg.NewSelect(m).
		Where("slug = $1", slug).
		Where("app_id = $2", appID).
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
	q := s.pg.NewSelect(&models)

	argIdx := 0
	if appID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("app_id = $%d", argIdx), appID)
	}
	if opts.Status != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("status = $%d", argIdx), string(opts.Status))
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at ASC")

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
	res, err := s.pg.NewUpdate(m).WherePK().Exec(ctx)
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
	res, err := s.pg.NewDelete((*planModel)(nil)).
		Where("id = $1", planID.String()).
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
	res, err := s.pg.NewUpdate((*planModel)(nil)).
		Set("status = $1", string(plan.StatusArchived)).
		Set("updated_at = $2", t).
		Where("id = $3", planID.String()).
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
	_, err := s.pg.NewInsert(m).Exec(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) GetFeature(ctx context.Context, featureID id.FeatureID) (*feature.Feature, error) {
	m := new(featureModel)
	err := s.pg.NewSelect(m).
		Where("id = $1", featureID.String()).
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
	err := s.pg.NewSelect(m).
		Where("key = $1", key).
		Where("app_id = $2", appID).
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
	q := s.pg.NewSelect(&models)

	argIdx := 0
	if appID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("app_id = $%d", argIdx), appID)
	}
	if opts.Status != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("status = $%d", argIdx), string(opts.Status))
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at ASC")

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
	q := s.pg.NewSelect(&models).Where("app_id = $1", "")

	argIdx := 1
	if opts.Status != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("status = $%d", argIdx), string(opts.Status))
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at ASC")

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
	res, err := s.pg.NewUpdate(m).WherePK().Exec(ctx)
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
	res, err := s.pg.NewDelete((*featureModel)(nil)).
		Where("id = $1", featureID.String()).
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
	res, err := s.pg.NewUpdate((*featureModel)(nil)).
		Set("status = $1", string(feature.StatusArchived)).
		Set("updated_at = $2", t).
		Where("id = $3", featureID.String()).
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
	_, err := s.pg.NewInsert(m).Exec(ctx)
	return err
}

func (s *Store) GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	m := new(subscriptionModel)
	err := s.pg.NewSelect(m).
		Where("id = $1", subID.String()).
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
	err := s.pg.NewSelect(m).
		Where("tenant_id = $1", tenantID).
		Where("app_id = $2", appID).
		Where("status IN ($3, $4)", string(subscription.StatusActive), string(subscription.StatusTrialing)).
		OrderExpr("created_at DESC").
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
	q := s.pg.NewSelect(&models)

	argIdx := 0
	if tenantID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("tenant_id = $%d", argIdx), tenantID)
	}
	if appID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("app_id = $%d", argIdx), appID)
	}
	if opts.Status != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("status = $%d", argIdx), string(opts.Status))
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at DESC")

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
	_, err := s.pg.NewUpdate(m).WherePK().Exec(ctx)
	return err
}

func (s *Store) CancelSubscription(ctx context.Context, subID id.SubscriptionID, cancelAt time.Time) error {
	t := now()
	updates := s.pg.NewUpdate((*subscriptionModel)(nil)).
		Set("cancel_at = $1", cancelAt).
		Set("updated_at = $2", t).
		Where("id = $3", subID.String())

	if time.Now().After(cancelAt) {
		updates = updates.
			Set("status = $4", string(subscription.StatusCanceled)).
			Set("canceled_at = $5", t)
	}

	res, err := updates.Exec(ctx)
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

// ==================== Meter Store ====================

func (s *Store) IngestBatch(ctx context.Context, events []*meter.UsageEvent) error {
	if len(events) == 0 {
		return nil
	}
	models := make([]usageEventModel, len(events))
	for i, e := range events {
		models[i] = *toUsageEventModel(e)
	}
	_, err := s.pg.NewInsert(&models).
		OnConflict("(idempotency_key) WHERE idempotency_key != '' DO NOTHING").
		Exec(ctx)
	return err
}

func (s *Store) Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error) {
	startOfPeriod := getStartOfPeriod(time.Now(), period)

	var total int64
	err := s.pg.NewRaw(`
		SELECT COALESCE(SUM(quantity), 0) FROM ledger_usage_events
		WHERE tenant_id = $1 AND app_id = $2 AND feature_key = $3 AND timestamp > $4
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
	q := s.pg.NewSelect(&models)

	argIdx := 0
	if tenantID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("tenant_id = $%d", argIdx), tenantID)
	}
	if appID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("app_id = $%d", argIdx), appID)
	}
	if opts.FeatureKey != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("feature_key = $%d", argIdx), opts.FeatureKey)
	}
	if !opts.Start.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("timestamp >= $%d", argIdx), opts.Start)
	}
	if !opts.End.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("timestamp <= $%d", argIdx), opts.End)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("timestamp DESC")

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
	res, err := s.pg.NewDelete((*usageEventModel)(nil)).
		Where("timestamp < $1", before).
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
	err := s.pg.NewSelect(m).
		Where("cache_key = $1", cacheKey).
		Where("expires_at > $2", time.Now().UTC()).
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
	_, err := s.pg.NewInsert(m).
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
	_, err := s.pg.NewDelete((*entitlementCacheModel)(nil)).
		Where("tenant_id = $1", tenantID).
		Where("app_id = $2", appID).
		Exec(ctx)
	return err
}

func (s *Store) InvalidateFeature(ctx context.Context, tenantID, appID, featureKey string) error {
	cacheKey := tenantID + ":" + appID + ":" + featureKey
	_, err := s.pg.NewDelete((*entitlementCacheModel)(nil)).
		Where("cache_key = $1", cacheKey).
		Exec(ctx)
	return err
}

// ==================== Invoice Store ====================

func (s *Store) CreateInvoice(ctx context.Context, inv *invoice.Invoice) error {
	m := toInvoiceModel(inv)
	_, err := s.pg.NewInsert(m).Exec(ctx)
	return err
}

func (s *Store) GetInvoice(ctx context.Context, invID id.InvoiceID) (*invoice.Invoice, error) {
	m := new(invoiceModel)
	err := s.pg.NewSelect(m).
		Where("id = $1", invID.String()).
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
	q := s.pg.NewSelect(&models)

	argIdx := 0
	if tenantID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("tenant_id = $%d", argIdx), tenantID)
	}
	if appID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("app_id = $%d", argIdx), appID)
	}
	if opts.Status != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("status = $%d", argIdx), string(opts.Status))
	}
	if !opts.Start.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("period_start >= $%d", argIdx), opts.Start)
	}
	if !opts.End.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("period_end <= $%d", argIdx), opts.End)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at DESC")

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
	_, err := s.pg.NewUpdate(m).WherePK().Exec(ctx)
	return err
}

func (s *Store) GetInvoiceByPeriod(ctx context.Context, tenantID, appID string, periodStart, periodEnd time.Time) (*invoice.Invoice, error) {
	m := new(invoiceModel)
	err := s.pg.NewSelect(m).
		Where("tenant_id = $1", tenantID).
		Where("app_id = $2", appID).
		Where("period_start = $3", periodStart).
		Where("period_end = $4", periodEnd).
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
	q := s.pg.NewSelect(&models)

	argIdx := 0
	if appID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("app_id = $%d", argIdx), appID)
	}
	argIdx++
	q = q.Where(fmt.Sprintf("status = $%d", argIdx), string(invoice.StatusPending))

	err := q.OrderExpr("created_at DESC").
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
	res, err := s.pg.NewUpdate((*invoiceModel)(nil)).
		Set("status = $1", string(invoice.StatusPaid)).
		Set("paid_at = $2", paidAt).
		Set("payment_ref = $3", paymentRef).
		Set("updated_at = $4", t).
		Where("id = $5", invID.String()).
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
	res, err := s.pg.NewUpdate((*invoiceModel)(nil)).
		Set("status = $1", string(invoice.StatusVoided)).
		Set("voided_at = $2", t).
		Set("void_reason = $3", reason).
		Set("updated_at = $4", t).
		Where("id = $5", invID.String()).
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
	_, err := s.pg.NewInsert(m).Exec(ctx)
	return err
}

func (s *Store) GetCoupon(ctx context.Context, code, appID string) (*coupon.Coupon, error) {
	m := new(couponModel)
	err := s.pg.NewSelect(m).
		Where("code = $1", code).
		Where("app_id = $2", appID).
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
	err := s.pg.NewSelect(m).
		Where("id = $1", couponID.String()).
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
	q := s.pg.NewSelect(&models)

	argIdx := 0
	if appID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("app_id = $%d", argIdx), appID)
	}
	if opts.Active {
		t := time.Now().UTC()
		argIdx++
		q = q.Where(fmt.Sprintf("(valid_from IS NULL OR valid_from <= $%d)", argIdx), t)
		argIdx++
		q = q.Where(fmt.Sprintf("(valid_until IS NULL OR valid_until >= $%d)", argIdx), t)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	q = q.OrderExpr("created_at DESC")

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

func (s *Store) UpdateCoupon(ctx context.Context, c *coupon.Coupon) error {
	m := toCouponModel(c)
	m.UpdatedAt = now()
	_, err := s.pg.NewUpdate(m).WherePK().Exec(ctx)
	return err
}

func (s *Store) DeleteCoupon(ctx context.Context, couponID id.CouponID) error {
	res, err := s.pg.NewDelete((*couponModel)(nil)).
		Where("id = $1", couponID.String()).
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
	if _, err := s.GetCouponByID(ctx, couponID); err != nil {
		return err
	}

	m := &couponApplicationModel{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       couponID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	if _, err := s.pg.NewInsert(m).Exec(ctx); err != nil {
		switch {
		case isUniqueViolation(err):
			return ledger.ErrCouponAlreadyApplied
		case isForeignKeyViolation(err):
			// The coupon existed at the check above but was deleted
			// before this insert landed.
			return ledger.ErrCouponNotFound
		default:
			return err
		}
	}

	return nil
}

func (s *Store) ListAppliedCoupons(ctx context.Context, subID id.SubscriptionID) ([]*coupon.Coupon, error) {
	if subID.IsNil() {
		return make([]*coupon.Coupon, 0), nil
	}

	var models []couponApplicationModel
	err := s.pg.NewSelect(&models).
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
	res, err := s.pg.NewUpdate((*couponModel)(nil)).
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
// (max_redemptions = 0 OR times_redeemed < max_redemptions)` - rather than
// a read of TimesRedeemed followed by a separate write, so two concurrent
// transactions racing the same coupon toward its cap cannot both read the
// count before either writes it: Postgres takes a row lock on the first
// UPDATE to reach the row and blocks the second until it commits, at which
// point the second's WHERE re-evaluates against the already-incremented
// count under read-committed semantics.
//
// The INSERT of the application row is the FIRST statement in the
// transaction, matching sqlite's RedeemCoupon (the two are kept
// structurally identical on purpose). Postgres itself has no equivalent to
// sqlite's lock-upgrade BUSY, but opening with a read here would still mean
// two different statement shapes to reason about across backends for no
// benefit, and the FK on ledger_coupon_applications.coupon_id already gives
// this insert a cheap, correct way to catch an unknown coupon without a
// SELECT: it fails with a 23503 foreign-key violation, mapped below to
// ErrCouponNotFound.
//
// Unknown coupon and exhausted cap share one signal - the conditional
// UPDATE affects zero rows - and are told apart only if that happens: on
// postgres this is normally unreachable for an unknown coupon (the FK
// above already refused the insert), so the existence check here is a
// defensive fallback for the case where the coupon was deleted between
// this transaction's insert and its update, not the primary way unknown
// coupons are caught.
func (s *Store) RedeemCoupon(ctx context.Context, subID id.SubscriptionID, couponID id.CouponID) error {
	if err := validateSubscriptionID(subID); err != nil {
		return err
	}

	tx, err := s.pg.BeginTxQuery(ctx, nil)
	if err != nil {
		return err
	}
	// Rollback after a successful Commit does not undo it - there is
	// nothing left to roll back - but it is not a silent no-op either: the
	// underlying pgx transaction returns pgx.ErrTxClosed, which this
	// unconditional defer discards with `_ =`. That discard is deliberate
	// on every path, not just the success one: Commit itself can fail
	// partway through, leaving the transaction open, and the same
	// Rollback call is what cleans that up.
	defer func() { _ = tx.Rollback() }()

	appModel := &couponApplicationModel{
		ID:             id.NewCouponApplicationID().String(),
		CouponID:       couponID.String(),
		SubscriptionID: subID.String(),
		AppliedAt:      now(),
	}
	if _, err := tx.NewInsert(appModel).Exec(ctx); err != nil {
		switch {
		case isUniqueViolation(err):
			return ledger.ErrCouponAlreadyApplied
		case isForeignKeyViolation(err):
			// No coupon with this id exists.
			return ledger.ErrCouponNotFound
		default:
			return err
		}
	}

	res, err := tx.NewUpdate((*couponModel)(nil)).
		Set("times_redeemed = times_redeemed + 1").
		Set("updated_at = ?", now()).
		Where("id = ?", couponID.String()).
		Where("(max_redemptions = 0 OR times_redeemed < max_redemptions)").
		Exec(ctx)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// The FK above already refused an unknown coupon's insert, so this
		// is normally reachable only when the cap was hit. The existence
		// check here only matters for the rare case of a concurrent delete
		// landing between this transaction's insert and this update.
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

// isNoRows checks for the standard sql.ErrNoRows sentinel.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// PostgreSQL SQLSTATE error codes. See
// https://www.postgresql.org/docs/current/errcodes-appendix.html.
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// isUniqueViolation reports whether err is a PostgreSQL unique constraint
// violation, detected by the driver's typed SQLSTATE code rather than by
// matching its message text.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// isForeignKeyViolation reports whether err is a PostgreSQL foreign key
// violation, detected by SQLSTATE.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation
}

// validateSubscriptionID rejects a subscription id that is nil or carries
// the wrong prefix before any storage is touched.
func validateSubscriptionID(subID id.SubscriptionID) error {
	if subID.IsNil() || subID.Prefix() != id.PrefixSubscription {
		return fmt.Errorf("ledger/postgres: invalid subscription id %q: %w", subID.String(), ledger.ErrInvalidInput)
	}
	return nil
}
