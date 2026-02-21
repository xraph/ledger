package store

import (
	"context"
	"time"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
)

// Store is the unified storage interface for all Ledger entities.
// Instead of embedding the sub-interfaces, we explicitly declare all methods
// to avoid naming conflicts.
type Store interface {
	// Plan methods
	CreatePlan(ctx context.Context, p *plan.Plan) error
	GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error)
	GetPlanBySlug(ctx context.Context, slug string, appID string) (*plan.Plan, error)
	ListPlans(ctx context.Context, appID string, opts plan.ListOpts) ([]*plan.Plan, error)
	UpdatePlan(ctx context.Context, p *plan.Plan) error
	DeletePlan(ctx context.Context, planID id.PlanID) error
	ArchivePlan(ctx context.Context, planID id.PlanID) error

	// Subscription methods
	CreateSubscription(ctx context.Context, s *subscription.Subscription) error
	GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error)
	GetActiveSubscription(ctx context.Context, tenantID string, appID string) (*subscription.Subscription, error)
	ListSubscriptions(ctx context.Context, tenantID string, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error)
	UpdateSubscription(ctx context.Context, s *subscription.Subscription) error
	CancelSubscription(ctx context.Context, subID id.SubscriptionID, cancelAt time.Time) error

	// Meter methods
	IngestBatch(ctx context.Context, events []*meter.UsageEvent) error
	Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error)
	AggregateMulti(ctx context.Context, tenantID, appID string, featureKeys []string, period plan.Period) (map[string]int64, error)
	QueryUsage(ctx context.Context, tenantID, appID string, opts meter.QueryOpts) ([]*meter.UsageEvent, error)
	PurgeUsage(ctx context.Context, before time.Time) (int64, error)

	// Entitlement methods
	GetCached(ctx context.Context, tenantID, appID, featureKey string) (*entitlement.Result, error)
	SetCached(ctx context.Context, tenantID, appID, featureKey string, result *entitlement.Result, ttl time.Duration) error
	Invalidate(ctx context.Context, tenantID, appID string) error
	InvalidateFeature(ctx context.Context, tenantID, appID, featureKey string) error

	// Invoice methods
	CreateInvoice(ctx context.Context, inv *invoice.Invoice) error
	GetInvoice(ctx context.Context, invID id.InvoiceID) (*invoice.Invoice, error)
	ListInvoices(ctx context.Context, tenantID, appID string, opts invoice.ListOpts) ([]*invoice.Invoice, error)
	UpdateInvoice(ctx context.Context, inv *invoice.Invoice) error
	GetInvoiceByPeriod(ctx context.Context, tenantID, appID string, periodStart, periodEnd time.Time) (*invoice.Invoice, error)
	ListPendingInvoices(ctx context.Context, appID string) ([]*invoice.Invoice, error)
	MarkInvoicePaid(ctx context.Context, invID id.InvoiceID, paidAt time.Time, paymentRef string) error
	MarkInvoiceVoided(ctx context.Context, invID id.InvoiceID, reason string) error

	// Coupon methods
	CreateCoupon(ctx context.Context, c *coupon.Coupon) error
	GetCoupon(ctx context.Context, code string, appID string) (*coupon.Coupon, error)
	GetCouponByID(ctx context.Context, couponID id.CouponID) (*coupon.Coupon, error)
	ListCoupons(ctx context.Context, appID string, opts coupon.ListOpts) ([]*coupon.Coupon, error)
	UpdateCoupon(ctx context.Context, c *coupon.Coupon) error
	DeleteCoupon(ctx context.Context, couponID id.CouponID) error

	// Core methods
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error
	Close() error
}
