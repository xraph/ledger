package ledger

import (
	"context"
	"fmt"
	"slices"
	"time"

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
	if qtyErr := validateQuantity(next, quantity); qtyErr != nil {
		return nil, qtyErr
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

// PauseSubscription pauses an active or trialing subscription and records
// when, in paused_at. While it is paused the lifecycle clock leaves its
// period and its trial alone, so the billing cycle stands still until
// ResumeSubscription.
func (l *Ledger) PauseSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	pause := func(ctx context.Context, subID id.SubscriptionID) (bool, error) {
		return l.store.PauseSubscription(ctx, subID, l.stamp())
	}
	return l.transitionSubscription(ctx, subID, subscription.StatusPaused, pause,
		subscription.StatusActive, subscription.StatusTrialing)
}

// resumeAttempts bounds how often ResumeSubscription starts again when a
// second pause lands between its read and its write.
const resumeAttempts = 3

// ResumeSubscription resumes a paused subscription. A subscription the
// lifecycle clock canceled while it was paused stays canceled.
//
// A pause stops the billing clock, so the resume stretches the period by the
// length of the pause: current_period_end moves on by now minus paused_at,
// and current_period_start stays. The stretched period carries one base fee
// and is billed once its new end passes, like any other; the lifecycle clock
// lists it in Renewal.Ended then, and the periods after it follow the
// clock's anchor from its new end, usually that end's day of the month. A
// cancel scheduled for the period end moves with it. Ledger remembers the
// most recent stretch (Subscription.Stretch), so ForPeriod can still prove
// the periods on both sides of it.
//
// A trial still running when the subscription was paused resumes as a trial,
// and its end moves on by the same length, so the customer gets the trial
// days they had left; the clock ends it, and fires OnSubscriptionTrialEnded,
// once that later date passes.
//
// A subscription paused before Ledger recorded paused_at has no pause start.
// Its period and its trial end stay where they were, and it resumes as a
// trial only if that end is still ahead.
//
// Everything is one conditional store write that lands only while the
// subscription is still paused and still carries the paused_at read here.
func (l *Ledger) ResumeSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	for range resumeAttempts {
		sub, err := l.store.GetSubscription(ctx, subID)
		if err != nil {
			return nil, err
		}
		if sub.Status != subscription.StatusPaused {
			return nil, fmt.Errorf("%w: cannot move a %s subscription to %s", ErrInvalidInput, sub.Status, subscription.StatusActive)
		}

		changed, err := l.store.ResumeSubscription(ctx, subID, resumeOf(sub, l.stamp()))
		if err != nil {
			return nil, err
		}
		sub, err = l.store.GetSubscription(ctx, subID)
		if err != nil {
			return nil, err
		}
		if changed {
			_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation
			return sub, nil
		}
		if sub.Status != subscription.StatusPaused {
			return nil, fmt.Errorf("%w: cannot move a %s subscription to %s", ErrInvalidInput, sub.Status, subscription.StatusActive)
		}
		// Resumed and paused again since the read: start over from the new
		// pause.
	}
	return nil, fmt.Errorf("%w: subscription %s was resumed and paused again while this resume was being worked out; try again", ErrInvalidInput, subID)
}

// resumeOf works out what resuming sub at now writes: the period end and a
// running trial's end moved on by the length of the pause, the stretch
// record, and trialing or active.
func resumeOf(sub *subscription.Subscription, now time.Time) subscription.Resume {
	now = now.UTC()
	r := subscription.Resume{PausedAt: sub.PausedAt, Status: subscription.StatusActive}
	pauseStart := now // unknown for a pause from before paused_at existed
	if sub.PausedAt != nil {
		pauseStart = sub.PausedAt.UTC()
	}
	paused := now.Sub(pauseStart)

	if paused > 0 {
		start, end := sub.CurrentPeriodStart.UTC(), sub.CurrentPeriodEnd.UTC()
		stretched := end.Add(paused)
		r.PeriodEnd = &stretched
		st := subscription.Stretch{Start: start, End: stretched, OriginalEnd: end}
		if prev := sub.Stretch; prev != nil {
			if prev.Start.Equal(start) {
				// A second pause in the same period: the cadence before it
				// still led to the first original end.
				st.OriginalEnd, st.Floor = prev.OriginalEnd, prev.Floor
			} else {
				// The cadence this period sits on began at the previous
				// stretch's end, and nothing before that is remembered.
				floor := prev.End
				st.Floor = &floor
			}
		}
		r.Stretch = &st
	}

	if sub.TrialEnd == nil || !sub.TrialEnd.After(pauseStart) {
		return r // no trial, or it had ended before the pause
	}
	r.Status = subscription.StatusTrialing
	if paused > 0 {
		trialEnd := sub.TrialEnd.UTC().Add(paused)
		r.TrialEnd = &trialEnd
	}
	return r
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
