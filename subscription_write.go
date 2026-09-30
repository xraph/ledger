package ledger

import (
	"context"
	"fmt"
	"slices"

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

	// Only plan_id and quantity are written, and only while the subscription
	// is neither canceled nor expired, so a period the lifecycle clock
	// advanced or a cancel it enacted since the read above survives.
	changed, err := l.store.ChangeSubscriptionPlan(ctx, subID, next.ID, quantity)
	if err != nil {
		return nil, err
	}
	sub, err = l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}
	if !changed {
		// The subscription stopped between the read and the write.
		switch sub.Status {
		case subscription.StatusExpired:
			return nil, ErrSubscriptionExpired
		default:
			return nil, ErrSubscriptionCanceled
		}
	}
	_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation

	l.plugins.EmitSubscriptionChanged(ctx, sub, previous, next)
	return sub, nil
}

// PauseSubscription pauses an active or trialing subscription.
func (l *Ledger) PauseSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	return l.transitionSubscription(ctx, subID, subscription.StatusPaused, l.store.PauseSubscription,
		subscription.StatusActive, subscription.StatusTrialing)
}

// ResumeSubscription resumes a paused subscription. A subscription the
// lifecycle clock canceled while it was paused stays canceled.
func (l *Ledger) ResumeSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	return l.transitionSubscription(ctx, subID, subscription.StatusActive, l.store.ResumeSubscription,
		subscription.StatusPaused)
}

// transitionSubscription moves a subscription whose status is one of from to
// the status to, through write, a conditional store write that repeats the
// same precondition and changes the status column alone. A whole-row write
// of the row read here could revive a subscription the lifecycle clock
// canceled after the read, or put back a period it advanced. When the write
// matches nothing, the status changed between the read and the write, and
// the refusal names the status it changed to.
func (l *Ledger) transitionSubscription(ctx context.Context, subID id.SubscriptionID, to subscription.Status,
	write func(context.Context, id.SubscriptionID) (bool, error), from ...subscription.Status,
) (*subscription.Subscription, error) {
	sub, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(from, sub.Status) {
		return nil, fmt.Errorf("%w: cannot move a %s subscription to %s", ErrInvalidInput, sub.Status, to)
	}

	changed, err := write(ctx, subID)
	if err != nil {
		return nil, err
	}
	sub, err = l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, fmt.Errorf("%w: cannot move a %s subscription to %s", ErrInvalidInput, sub.Status, to)
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
