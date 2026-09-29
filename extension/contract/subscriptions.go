package contract

import (
	"context"
	"strings"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
)

func registerSubscriptions(b *binder) {
	query(b, "subscriptions.list", subscriptionsList)
	query(b, "subscriptions.detail", subscriptionsDetail)
	query(b, "subscriptions.usage", subscriptionsUsage)
	command(b, "subscriptions.create", subscriptionsCreate)
	command(b, "subscriptions.changePlan", subscriptionsChangePlan)
	command(b, "subscriptions.pause", subscriptionsPause)
	command(b, "subscriptions.resume", subscriptionsResume)
	command(b, "subscriptions.cancel", subscriptionsCancel)
	command(b, "subscriptions.syncToProvider", subscriptionsSync)
}

// loadSubscription parses an id and loads the subscription it names, refusing
// with NOT_FOUND when it belongs to another app.
func loadSubscription(ctx context.Context, eng *ledger.Ledger, sc scope, field, raw string) (*subscription.Subscription, error) {
	subID, err := parseID(field, raw, id.ParseSubscriptionID)
	if err != nil {
		return nil, err
	}
	sub, err := eng.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}
	if !sc.owns(sub.AppID) {
		return nil, notFound("subscription")
	}
	return sub, nil
}

func validSubscriptionStatus(s string) bool {
	switch subscription.Status(s) {
	case "", subscription.StatusActive, subscription.StatusTrialing, subscription.StatusPastDue,
		subscription.StatusCanceled, subscription.StatusExpired, subscription.StatusPaused:
		return true
	}
	return false
}

type SubscriptionsListInput struct {
	PageInput
	// TenantID narrows the list to one customer. Empty lists every customer in
	// the app; the app, not the tenant, is the scope.
	TenantID string `json:"tenant_id"`
	Status   string `json:"status"`
}

func subscriptionsList(ctx context.Context, eng *ledger.Ledger, sc scope, in SubscriptionsListInput) (Page[*subscription.Subscription], error) {
	if !validSubscriptionStatus(in.Status) {
		return Page[*subscription.Subscription]{}, badRequest("unknown subscription status %q", in.Status)
	}
	limit, offset := in.window()
	rows, err := eng.Store().ListSubscriptions(ctx, strings.TrimSpace(in.TenantID), sc.AppID,
		subscription.ListOpts{Status: subscription.Status(in.Status), Limit: limit + 1, Offset: offset})
	if err != nil {
		return Page[*subscription.Subscription]{}, err
	}
	return pageFrom(rows, limit, offset), nil
}

type SubscriptionDetail struct {
	Subscription   *subscription.Subscription `json:"subscription"`
	Plan           *plan.Plan                 `json:"plan"`
	AppliedCoupons []*coupon.Coupon           `json:"applied_coupons"`
}

func subscriptionsDetail(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (SubscriptionDetail, error) {
	sub, err := loadSubscription(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return SubscriptionDetail{}, err
	}
	p, err := eng.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return SubscriptionDetail{}, err
	}
	coupons, err := eng.ListAppliedCoupons(ctx, sub.ID)
	if err != nil {
		return SubscriptionDetail{}, err
	}
	if coupons == nil {
		coupons = []*coupon.Coupon{}
	}
	return SubscriptionDetail{Subscription: sub, Plan: p, AppliedCoupons: coupons}, nil
}

type FeatureUsage struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Period    string `json:"period"`
	Limit     int64  `json:"limit"`
	Used      int64  `json:"used"`
	Remaining int64  `json:"remaining"`
	SoftLimit bool   `json:"soft_limit"`
	OverLimit bool   `json:"over_limit"`
	Enabled   bool   `json:"enabled"`
}

type SubscriptionUsage struct {
	Features []FeatureUsage `json:"features"`
}

// subscriptionsUsage reports each plan feature's usage against its limit: one
// AggregateMulti per distinct period for metered features, the subscription's
// quantity for seats. Usage is counted over the store's calendar period, not
// the subscription's billing period.
func subscriptionsUsage(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (SubscriptionUsage, error) {
	sub, err := loadSubscription(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return SubscriptionUsage{}, err
	}
	p, err := eng.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return SubscriptionUsage{}, err
	}

	byPeriod := map[plan.Period][]string{}
	for _, f := range p.Features {
		if f.Type == plan.FeatureMetered {
			byPeriod[f.Period] = append(byPeriod[f.Period], f.Key)
		}
	}
	used := map[string]int64{}
	for period, keys := range byPeriod {
		totals, err := eng.Store().AggregateMulti(ctx, sub.TenantID, sub.AppID, keys, period)
		if err != nil {
			return SubscriptionUsage{}, err
		}
		for k, v := range totals {
			used[k] = v
		}
	}

	out := SubscriptionUsage{Features: make([]FeatureUsage, 0, len(p.Features))}
	for _, f := range p.Features {
		u := FeatureUsage{
			Key: f.Key, Name: f.Name, Type: string(f.Type), Period: string(f.Period),
			Limit: f.Limit, SoftLimit: f.SoftLimit,
		}
		switch f.Type {
		case plan.FeatureMetered:
			u.Used = used[f.Key]
		case plan.FeatureSeat:
			u.Used = sub.Quantity[f.Key]
		case plan.FeatureBoolean:
			u.Enabled = f.Limit > 0
		}
		switch {
		case f.Type == plan.FeatureBoolean || f.Limit == -1:
			u.Remaining = -1
		default:
			u.Remaining = max(0, f.Limit-u.Used)
			u.OverLimit = u.Used > f.Limit
		}
		out.Features = append(out.Features, u)
	}
	return out, nil
}

type SubscriptionCreateInput struct {
	TenantID string           `json:"tenant_id"`
	PlanID   string           `json:"plan_id"`
	Quantity map[string]int64 `json:"quantity"`
}

func subscriptionsCreate(ctx context.Context, eng *ledger.Ledger, sc scope, in SubscriptionCreateInput) (*subscription.Subscription, error) {
	tenant := strings.TrimSpace(in.TenantID)
	if tenant == "" {
		return nil, badRequest("tenant_id is required")
	}
	p, err := loadPlan(ctx, eng, sc, "plan_id", in.PlanID)
	if err != nil {
		return nil, err
	}
	sub := &subscription.Subscription{TenantID: tenant, PlanID: p.ID, AppID: sc.AppID, Quantity: in.Quantity}
	if err := eng.CreateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

type SubscriptionChangePlanInput struct {
	ID       string            `json:"id"`
	PlanID   string            `json:"plan_id"`
	Quantity *map[string]int64 `json:"quantity"`
}

func subscriptionsChangePlan(ctx context.Context, eng *ledger.Ledger, sc scope, in SubscriptionChangePlanInput) (*subscription.Subscription, error) {
	sub, err := loadSubscription(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return nil, err
	}
	p, err := loadPlan(ctx, eng, sc, "plan_id", in.PlanID)
	if err != nil {
		return nil, err
	}
	var quantity map[string]int64
	if in.Quantity != nil {
		quantity = *in.Quantity
	}
	return eng.ChangePlan(ctx, sub.ID, p.ID, quantity)
}

func subscriptionsPause(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*subscription.Subscription, error) {
	sub, err := loadSubscription(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return nil, err
	}
	return eng.PauseSubscription(ctx, sub.ID)
}

func subscriptionsResume(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*subscription.Subscription, error) {
	sub, err := loadSubscription(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return nil, err
	}
	return eng.ResumeSubscription(ctx, sub.ID)
}

type SubscriptionCancelInput struct {
	ID string `json:"id"`
	// Immediately ends the subscription now. Otherwise it ends at the close of
	// the current billing period.
	Immediately bool `json:"immediately"`
}

func subscriptionsCancel(ctx context.Context, eng *ledger.Ledger, sc scope, in SubscriptionCancelInput) (*subscription.Subscription, error) {
	sub, err := loadSubscription(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return nil, err
	}
	if err := eng.CancelSubscription(ctx, sub.ID, in.Immediately); err != nil {
		return nil, err
	}
	return eng.GetSubscription(ctx, sub.ID)
}

func subscriptionsSync(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*provider.SyncResult, error) {
	sub, err := loadSubscription(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return nil, err
	}
	res, err := eng.SyncSubscriptionToProvider(ctx, sub.ID)
	// The engine returns the result and the provider's error together when the
	// provider refuses. Passing that error on would turn a refusal into a bare
	// "internal error" and drop the result, so report the refusal as an answer:
	// Success is false and Error carries the provider's message. Only a call
	// that produced no result (the subscription is gone, or no provider is
	// configured) is an error.
	if res != nil {
		return res, nil
	}
	return nil, err
}
