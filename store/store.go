package store

import (
	"context"
	"time"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/feature"
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

	// Feature catalog methods
	CreateFeature(ctx context.Context, f *feature.Feature) error
	GetFeature(ctx context.Context, featureID id.FeatureID) (*feature.Feature, error)
	GetFeatureByKey(ctx context.Context, key string, appID string) (*feature.Feature, error)
	ListFeatures(ctx context.Context, appID string, opts feature.ListOpts) ([]*feature.Feature, error)
	ListGlobalFeatures(ctx context.Context, opts feature.ListOpts) ([]*feature.Feature, error)
	UpdateFeature(ctx context.Context, f *feature.Feature) error
	DeleteFeature(ctx context.Context, featureID id.FeatureID) error
	ArchiveFeature(ctx context.Context, featureID id.FeatureID) error

	// Subscription methods
	CreateSubscription(ctx context.Context, s *subscription.Subscription) error
	GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error)
	GetActiveSubscription(ctx context.Context, tenantID string, appID string) (*subscription.Subscription, error)
	ListSubscriptions(ctx context.Context, tenantID string, appID string, opts subscription.ListOpts) ([]*subscription.Subscription, error)
	UpdateSubscription(ctx context.Context, s *subscription.Subscription) error
	// CancelSubscription cancels a subscription and returns the cancel_at it
	// stored, in UTC. Immediately, it sets cancel_at and canceled_at to now
	// and the status to canceled. Otherwise it schedules the cancel for the
	// end of the period that is current when the write lands: the statement
	// copies current_period_end into cancel_at from the row itself, never a
	// value the caller read, so a period the lifecycle clock advanced in
	// between is the one that ends. A scheduled cancel never changes the
	// status; the clock's EnactSubscriptionCancel does, once cancel_at has
	// passed. Both refuse a subscription that is already canceled or expired,
	// with ledger.ErrSubscriptionCanceled or ledger.ErrSubscriptionExpired, in
	// the same conditional write, so an operator's cancel racing the clock's
	// enactment never cancels a subscription twice.
	CancelSubscription(ctx context.Context, subID id.SubscriptionID, immediately bool) (time.Time, error)

	// Operator writes that can race the lifecycle clock. Each one writes only
	// the columns it names in one conditional statement whose WHERE repeats
	// the engine's precondition, never a whole row read earlier, so it cannot
	// put back a period the clock advanced or revive a subscription the clock
	// canceled. The bool ones report whether a row matched; a missing row is
	// false with no error, and the caller re-reads to learn why.
	//
	// PauseSubscription sets status to paused and paused_at to at when the
	// status is active or trialing.
	PauseSubscription(ctx context.Context, subID id.SubscriptionID, at time.Time) (bool, error)
	// ResumeSubscription applies r when the status is paused and paused_at
	// still equals r.PausedAt (is null when r.PausedAt is nil): it sets the
	// status to r.Status, the current period to [r.PeriodStart,
	// r.PeriodEnd), trial_end to r.TrialEnd unless that is nil, resumed_at
	// to r.At, and clears paused_at. The lifecycle clock never writes a
	// paused row's period or trial, so nothing it does can be lost here.
	ResumeSubscription(ctx context.Context, subID id.SubscriptionID, r subscription.Resume) (bool, error)
	// ChangeSubscriptionPlan sets plan_id and quantity when the status is
	// neither canceled nor expired. A nil quantity is stored as empty.
	ChangeSubscriptionPlan(ctx context.Context, subID id.SubscriptionID, planID id.PlanID, quantity map[string]int64) (bool, error)
	// SetSubscriptionProvider records the provider's id and name for a
	// subscription, or returns ledger.ErrSubscriptionNotFound.
	SetSubscriptionProvider(ctx context.Context, subID id.SubscriptionID, providerID, providerName string) error

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
	// SetInvoiceProvider records the provider's id and name for an invoice,
	// and nothing else, so a provider sync cannot put back a status the
	// lifecycle clock or an operator wrote meanwhile. It returns
	// ledger.ErrInvoiceNotFound for a missing invoice.
	SetInvoiceProvider(ctx context.Context, invID id.InvoiceID, providerID, providerName string) error

	// Coupon methods
	CreateCoupon(ctx context.Context, c *coupon.Coupon) error
	GetCoupon(ctx context.Context, code string, appID string) (*coupon.Coupon, error)
	GetCouponByID(ctx context.Context, couponID id.CouponID) (*coupon.Coupon, error)
	ListCoupons(ctx context.Context, appID string, opts coupon.ListOpts) ([]*coupon.Coupon, error)
	UpdateCoupon(ctx context.Context, c *coupon.Coupon) error
	DeleteCoupon(ctx context.Context, couponID id.CouponID) error
	// ApplyCoupon and IncrementCouponRedemptions are low-level operations
	// kept for backends and tests that need them separately. Engine code
	// redeems through RedeemCoupon, which performs both as one unit.
	ApplyCoupon(ctx context.Context, subID id.SubscriptionID, couponID id.CouponID) error
	ListAppliedCoupons(ctx context.Context, subID id.SubscriptionID) ([]*coupon.Coupon, error)
	IncrementCouponRedemptions(ctx context.Context, couponID id.CouponID) error
	// RedeemCoupon records a coupon's application to a subscription and
	// increments its redemption count as a single unit: either both land or
	// neither does. The redemption cap (MaxRedemptions) is enforced by a
	// conditional increment inside that unit, not by a read followed by a
	// separate write, so it holds under concurrent callers racing the same
	// coupon toward its cap. A MaxRedemptions of zero or less means
	// unlimited on every backend.
	RedeemCoupon(ctx context.Context, subID id.SubscriptionID, couponID id.CouponID) error

	// Lifecycle clock methods. The two List methods find the rows a clock
	// step has to visit, across every app when AppID is empty. Each
	// transition changes one row in a single conditional write that repeats
	// its precondition, touches only the columns it names, and reports
	// whether a row matched, so two replicas running the clock at once never
	// apply a transition twice and never overwrite an operator's concurrent
	// write to other columns. A missing row is false with no error.
	ListDueSubscriptions(ctx context.Context, opts subscription.DueOpts) ([]*subscription.Subscription, error)
	ListOverdueInvoices(ctx context.Context, opts invoice.OverdueOpts) ([]*invoice.Invoice, error)
	// EndSubscriptionTrial makes a trialing subscription whose trial_end is at
	// or before now active.
	EndSubscriptionTrial(ctx context.Context, subID id.SubscriptionID, now time.Time) (bool, error)
	// EnactSubscriptionCancel cancels a subscription whose cancel_at is at or
	// before now and that is not already canceled or expired, and sets
	// canceled_at to cancel_at, the moment the cancellation took effect.
	EnactSubscriptionCancel(ctx context.Context, subID id.SubscriptionID, now time.Time) (bool, error)
	// AdvanceSubscriptionPeriod sets the current period to [start, end) when
	// the subscription is active, trialing or past due, its current period
	// ended at or before now, end is later than that current end, and no
	// cancellation falls at or before the current end.
	AdvanceSubscriptionPeriod(ctx context.Context, subID id.SubscriptionID, start, end, now time.Time) (bool, error)
	// MarkInvoicePastDue moves a pending invoice whose due_date is before now
	// to past_due.
	MarkInvoicePastDue(ctx context.Context, invID id.InvoiceID, now time.Time) (bool, error)

	// Core methods
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error
	Close() error
}
