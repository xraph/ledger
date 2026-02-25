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
	q := s.sdb.NewSelect(&models).Where("app_id = ?", appID)

	if opts.Status != "" {
		q = q.Where("status = ?", string(opts.Status))
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
	q := s.sdb.NewSelect(&models).
		Where("tenant_id = ?", tenantID).
		Where("app_id = ?", appID)

	if opts.Status != "" {
		q = q.Where("status = ?", string(opts.Status))
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
	_, err := s.sdb.NewUpdate(m).WherePK().Exec(ctx)
	return err
}

func (s *Store) CancelSubscription(ctx context.Context, subID id.SubscriptionID, cancelAt time.Time) error {
	t := now()
	updates := s.sdb.NewUpdate((*subscriptionModel)(nil)).
		Set("cancel_at = ?", cancelAt).
		Set("updated_at = ?", t).
		Where("id = ?", subID.String())

	if time.Now().After(cancelAt) {
		updates = updates.
			Set("status = ?", string(subscription.StatusCanceled)).
			Set("canceled_at = ?", t)
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
	_, err := s.sdb.NewInsert(&models).
		OnConflict("(idempotency_key) WHERE idempotency_key != '' DO NOTHING").
		Exec(ctx)
	return err
}

func (s *Store) Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error) {
	startOfPeriod := getStartOfPeriod(time.Now(), period)

	var total int64
	err := s.sdb.NewRaw(`
		SELECT COALESCE(SUM(quantity), 0) FROM ledger_usage_events
		WHERE tenant_id = ? AND app_id = ? AND feature_key = ? AND timestamp > ?
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
	q := s.sdb.NewSelect(&models).
		Where("tenant_id = ?", tenantID).
		Where("app_id = ?", appID)

	if opts.FeatureKey != "" {
		q = q.Where("feature_key = ?", opts.FeatureKey)
	}
	if !opts.Start.IsZero() {
		q = q.Where("timestamp >= ?", opts.Start)
	}
	if !opts.End.IsZero() {
		q = q.Where("timestamp <= ?", opts.End)
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
	res, err := s.sdb.NewDelete((*usageEventModel)(nil)).
		Where("timestamp < ?", before).
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
	q := s.sdb.NewSelect(&models).
		Where("tenant_id = ?", tenantID).
		Where("app_id = ?", appID)

	if opts.Status != "" {
		q = q.Where("status = ?", string(opts.Status))
	}
	if !opts.Start.IsZero() {
		q = q.Where("period_start >= ?", opts.Start)
	}
	if !opts.End.IsZero() {
		q = q.Where("period_end <= ?", opts.End)
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
	_, err := s.sdb.NewUpdate(m).WherePK().Exec(ctx)
	return err
}

func (s *Store) GetInvoiceByPeriod(ctx context.Context, tenantID, appID string, periodStart, periodEnd time.Time) (*invoice.Invoice, error) {
	m := new(invoiceModel)
	err := s.sdb.NewSelect(m).
		Where("tenant_id = ?", tenantID).
		Where("app_id = ?", appID).
		Where("period_start = ?", periodStart).
		Where("period_end = ?", periodEnd).
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
	err := s.sdb.NewSelect(&models).
		Where("app_id = ?", appID).
		Where("status = ?", string(invoice.StatusPending)).
		OrderExpr("created_at DESC").
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
		Set("paid_at = ?", paidAt).
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
	q := s.sdb.NewSelect(&models).Where("app_id = ?", appID)

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
	_, err := s.sdb.NewUpdate(m).WherePK().Exec(ctx)
	return err
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
