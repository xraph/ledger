package ledger

import (
	"context"
	"fmt"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
)

// subscribablePlan loads a plan a subscription in appID may be on: it exists,
// it is in the same app, and it is active.
func (l *Ledger) subscribablePlan(ctx context.Context, planID id.PlanID, appID string) (*plan.Plan, error) {
	if planID.IsNil() {
		return nil, fmt.Errorf("%w: a subscription needs a plan", ErrInvalidInput)
	}
	p, err := l.store.GetPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	if p.AppID != appID {
		return nil, fmt.Errorf("%w: the plan belongs to another app", ErrInvalidInput)
	}
	if p.Status != plan.StatusActive {
		return nil, fmt.Errorf("%w: plan %q is %s, not active", ErrInvalidInput, p.Slug, p.Status)
	}
	return p, nil
}

// validateQuantity checks seat counts against the plan: non-negative, and only
// for the plan's seat features.
func validateQuantity(p *plan.Plan, quantity map[string]int64) error {
	for key, n := range quantity {
		if n < 0 {
			return fmt.Errorf("%w: quantity for %q is negative", ErrInvalidQuantity, key)
		}
		f := p.FindFeature(key)
		if f == nil || f.Type != plan.FeatureSeat {
			return fmt.Errorf("%w: %q is not a seat feature of plan %q", ErrInvalidInput, key, p.Slug)
		}
	}
	return nil
}

// ChangePlan moves a subscription to another active plan in the same app. The
// change takes effect from the next invoice; nothing is prorated. A nil
// quantity keeps the current seat counts.
func (l *Ledger) ChangePlan(ctx context.Context, subID id.SubscriptionID, planID id.PlanID, quantity map[string]int64) (*subscription.Subscription, error) {
	sub, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}
	switch sub.Status {
	case subscription.StatusCanceled:
		return nil, ErrSubscriptionCanceled
	case subscription.StatusExpired:
		return nil, ErrSubscriptionExpired
	}

	next, err := l.subscribablePlan(ctx, planID, sub.AppID)
	if err != nil {
		return nil, err
	}
	if quantity == nil {
		quantity = sub.Quantity
	}
	if err := validateQuantity(next, quantity); err != nil {
		return nil, err
	}

	previous, err := l.store.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return nil, err
	}

	sub.PlanID = next.ID
	sub.Quantity = quantity
	sub.Touch()
	if err := l.store.UpdateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation

	l.plugins.EmitSubscriptionChanged(ctx, sub, previous, next)
	return sub, nil
}

// PauseSubscription pauses an active or trialing subscription.
func (l *Ledger) PauseSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	return l.transitionSubscription(ctx, subID, subscription.StatusPaused,
		subscription.StatusActive, subscription.StatusTrialing)
}

// ResumeSubscription resumes a paused subscription.
func (l *Ledger) ResumeSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	return l.transitionSubscription(ctx, subID, subscription.StatusActive, subscription.StatusPaused)
}

func (l *Ledger) transitionSubscription(ctx context.Context, subID id.SubscriptionID, to subscription.Status, from ...subscription.Status) (*subscription.Subscription, error) {
	sub, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}

	allowed := false
	for _, f := range from {
		if sub.Status == f {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("%w: cannot move a %s subscription to %s", ErrInvalidInput, sub.Status, to)
	}

	sub.Status = to
	sub.Touch()
	if err := l.store.UpdateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation

	return sub, nil
}

// liveInvoiceForPeriod returns a non-voided invoice already covering the
// subscription's current period, or nil when there is none. It matches on the
// subscription as well as the period: two subscriptions for one tenant can
// share a period (SDK callers can align every period to the calendar), and
// each of them is billed on its own.
//
// It lists the period rather than calling GetInvoiceByPeriod: that method
// returns a single row, and once an invoice has been voided and regenerated
// two rows share the period, so it could hand back the voided one and let a
// third invoice through.
func (l *Ledger) liveInvoiceForPeriod(ctx context.Context, sub *subscription.Subscription) (*invoice.Invoice, error) {
	invs, err := l.store.ListInvoices(ctx, sub.TenantID, sub.AppID, invoice.ListOpts{
		Start: sub.CurrentPeriodStart,
		End:   sub.CurrentPeriodEnd,
	})
	if err != nil {
		return nil, err
	}
	for _, inv := range invs {
		if inv.SubscriptionID == sub.ID && inv.TenantID == sub.TenantID && inv.AppID == sub.AppID &&
			inv.PeriodStart.Equal(sub.CurrentPeriodStart) && inv.PeriodEnd.Equal(sub.CurrentPeriodEnd) &&
			inv.Status != invoice.StatusVoided {
			return inv, nil
		}
	}
	return nil, nil
}
