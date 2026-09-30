package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
)

// ──────────────────────────────────────────────────
// Lifecycle Clock
// ──────────────────────────────────────────────────

const (
	// lifecycleBatch is how many rows a lifecycle step reads at a time.
	lifecycleBatch = 100
	// maxCatchUpPeriods bounds one catch-up: a hundred years of months.
	maxCatchUpPeriods = 1200
)

var (
	// runningStatuses are the subscriptions whose periods roll.
	runningStatuses = []subscription.Status{
		subscription.StatusActive, subscription.StatusTrialing, subscription.StatusPastDue,
	}
	// cancellableStatuses are the subscriptions a due cancel still ends.
	cancellableStatuses = []subscription.Status{
		subscription.StatusActive, subscription.StatusTrialing, subscription.StatusPastDue, subscription.StatusPaused,
	}
)

// LifecycleReport lists what one Advance run changed.
type LifecycleReport struct {
	PeriodsAdvanced []id.SubscriptionID
	CancelsEnacted  []id.SubscriptionID
	TrialsEnded     []id.SubscriptionID
	InvoicesPastDue []id.InvoiceID
}

// Empty reports whether the run changed nothing.
func (r LifecycleReport) Empty() bool {
	return len(r.PeriodsAdvanced)+len(r.CancelsEnacted)+len(r.TrialsEnded)+len(r.InvoicesPastDue) == 0
}

// Advance runs every lifecycle step once, as of now, in this order: it moves
// ended periods forward, enacts due cancellations, ends due trials, and marks
// overdue invoices past due. Periods go first so that a cancellation dated
// inside a missed period ends that period; the period step never moves past
// a cancellation. Cancellations go before trials so that a trial canceled
// before it ended is never announced as converted.
//
// The background worker calls it on every tick. Call it yourself from your
// own scheduler with the worker turned off, or from a test with any now. It
// is safe to run on several replicas at once: every change is a conditional
// store write, and only the run whose write lands reports it and fires its
// hook. Each step keeps going past a row that fails; the error joins them.
func (l *Ledger) Advance(ctx context.Context, now time.Time) (LifecycleReport, error) {
	var (
		report LifecycleReport
		errs   [4]error
	)
	report.PeriodsAdvanced, errs[0] = l.AdvancePeriods(ctx, now)
	report.CancelsEnacted, errs[1] = l.EnactCancels(ctx, now)
	report.TrialsEnded, errs[2] = l.EndTrials(ctx, now)
	report.InvoicesPastDue, errs[3] = l.MarkInvoicesPastDue(ctx, now)
	return report, errors.Join(errs[:]...)
}

// AdvancePeriods moves every active, trialing or past-due subscription whose
// period has ended into the period that contains now, in one write however
// many periods it missed, and never past a scheduled cancellation. Periods
// follow the plan's billing period (see billingPeriod); a plan billed "none"
// never rolls. OnSubscriptionRenewed fires once per subscription moved, with
// every period that ended in the move.
func (l *Ledger) AdvancePeriods(ctx context.Context, now time.Time) ([]id.SubscriptionID, error) {
	now = now.UTC()
	plans := map[string]*plan.Plan{}
	var moved []id.SubscriptionID
	err := eachDue(ctx, l.dueSubscriptions(subscription.DuePeriodEnd, runningStatuses, now),
		func(sub *subscription.Subscription) (bool, error) {
			changed, err := l.advancePeriod(ctx, sub, now, plans)
			if changed {
				moved = append(moved, sub.ID)
			}
			return changed, err
		})
	return moved, err
}

func (l *Ledger) advancePeriod(ctx context.Context, sub *subscription.Subscription, now time.Time, plans map[string]*plan.Plan) (bool, error) {
	p, ok := plans[sub.PlanID.String()]
	if !ok {
		var err error
		if p, err = l.store.GetPlan(ctx, sub.PlanID); err != nil {
			return false, fmt.Errorf("advance subscription %s: %w", sub.ID, err)
		}
		plans[sub.PlanID.String()] = p
	}
	period := billingPeriod(p)
	switch period {
	case plan.PeriodMonthly, plan.PeriodYearly:
	case plan.PeriodNone:
		return false, nil
	default:
		return false, fmt.Errorf("advance subscription %s: plan %s has unknown billing period %q", sub.ID, p.ID, period)
	}

	start, end := sub.CurrentPeriodStart.UTC(), sub.CurrentPeriodEnd.UTC()
	if !end.After(start) {
		return false, fmt.Errorf("advance subscription %s: its period %v to %v is empty", sub.ID, start, end)
	}
	var ended []subscription.Period
	for !end.After(now) && (sub.CancelAt == nil || end.Before(*sub.CancelAt)) {
		if len(ended) == maxCatchUpPeriods {
			return false, fmt.Errorf("advance subscription %s: still behind after %d periods", sub.ID, maxCatchUpPeriods)
		}
		ended = append(ended, subscription.Period{Start: start, End: end})
		start, end = nextPeriod(start, end, period)
	}
	if len(ended) == 0 {
		return false, nil // a cancellation falls on the current end
	}

	changed, err := l.store.AdvanceSubscriptionPeriod(ctx, sub.ID, start, end, now)
	if err != nil {
		return false, fmt.Errorf("advance subscription %s: %w", sub.ID, err)
	}
	if !changed {
		return false, nil
	}
	sub.CurrentPeriodStart, sub.CurrentPeriodEnd = start, end
	sub.Touch()
	_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation
	l.plugins.EmitSubscriptionRenewed(ctx, &subscription.Renewal{Subscription: sub, Ended: ended})
	return true, nil
}

// EnactCancels cancels every subscription whose cancel_at is at or before now
// and that is not already canceled or expired, paused ones included, and sets
// canceled_at to cancel_at. OnSubscriptionCanceled fires for each.
func (l *Ledger) EnactCancels(ctx context.Context, now time.Time) ([]id.SubscriptionID, error) {
	now = now.UTC()
	var ended []id.SubscriptionID
	err := eachDue(ctx, l.dueSubscriptions(subscription.DueCancel, cancellableStatuses, now),
		func(sub *subscription.Subscription) (bool, error) {
			changed, err := l.store.EnactSubscriptionCancel(ctx, sub.ID, now)
			if err != nil {
				return false, fmt.Errorf("cancel subscription %s: %w", sub.ID, err)
			}
			if !changed || sub.CancelAt == nil {
				return changed, nil
			}
			canceledAt := *sub.CancelAt
			sub.Status = subscription.StatusCanceled
			sub.CanceledAt = &canceledAt
			sub.Touch()
			_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation
			l.plugins.EmitSubscriptionCanceled(ctx, sub)
			ended = append(ended, sub.ID)
			return true, nil
		})
	return ended, err
}

// EndTrials makes every trialing subscription whose trial_end is at or before
// now active. A paused subscription's trial is left alone. OnSubscriptionTrialEnded
// fires for each.
func (l *Ledger) EndTrials(ctx context.Context, now time.Time) ([]id.SubscriptionID, error) {
	now = now.UTC()
	var ended []id.SubscriptionID
	err := eachDue(ctx, l.dueSubscriptions(subscription.DueTrialEnd, []subscription.Status{subscription.StatusTrialing}, now),
		func(sub *subscription.Subscription) (bool, error) {
			changed, err := l.store.EndSubscriptionTrial(ctx, sub.ID, now)
			if err != nil {
				return false, fmt.Errorf("end the trial of subscription %s: %w", sub.ID, err)
			}
			if !changed {
				return false, nil
			}
			sub.Status = subscription.StatusActive
			sub.Touch()
			_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation
			l.plugins.EmitSubscriptionTrialEnded(ctx, sub)
			ended = append(ended, sub.ID)
			return true, nil
		})
	return ended, err
}

// MarkInvoicesPastDue moves every pending invoice whose due date is before now
// to past_due. The subscription is left as it is: its entitlements carry on.
// OnInvoicePastDue fires for each.
func (l *Ledger) MarkInvoicesPastDue(ctx context.Context, now time.Time) ([]id.InvoiceID, error) {
	now = now.UTC()
	var marked []id.InvoiceID
	list := func(ctx context.Context, offset int) ([]*invoice.Invoice, error) {
		return l.store.ListOverdueInvoices(ctx, invoice.OverdueOpts{Before: now, Limit: lifecycleBatch, Offset: offset})
	}
	err := eachDue(ctx, list, func(inv *invoice.Invoice) (bool, error) {
		changed, err := l.store.MarkInvoicePastDue(ctx, inv.ID, now)
		if err != nil {
			return false, fmt.Errorf("mark invoice %s past due: %w", inv.ID, err)
		}
		if !changed {
			return false, nil
		}
		// A copy: the memory store hands out its own pointer.
		pastDue := *inv
		pastDue.Status = invoice.StatusPastDue
		pastDue.Touch()
		l.plugins.EmitInvoicePastDue(ctx, &pastDue)
		marked = append(marked, inv.ID)
		return true, nil
	})
	return marked, err
}

// dueSubscriptions lists one batch of subscriptions due on field, across every app.
func (l *Ledger) dueSubscriptions(field subscription.DueField, statuses []subscription.Status, now time.Time) func(context.Context, int) ([]*subscription.Subscription, error) {
	return func(ctx context.Context, offset int) ([]*subscription.Subscription, error) {
		return l.store.ListDueSubscriptions(ctx, subscription.DueOpts{
			Field: field, Before: now, Statuses: statuses, Limit: lifecycleBatch, Offset: offset,
		})
	}
}

// eachDue visits every row a step lists, a batch at a time. A row the step
// changed leaves the due set, so the next batch starts after the rows that
// stayed: one that cannot move, one that failed, or one another replica took
// meanwhile. The last can skip a row for this run; the next run finds it.
func eachDue[T any](ctx context.Context, list func(context.Context, int) ([]T, error), apply func(T) (bool, error)) error {
	var errs []error
	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		rows, err := list(ctx, offset)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, row := range rows {
			changed, err := apply(row)
			if err != nil {
				errs = append(errs, err)
			}
			if !changed {
				offset++
			}
		}
		if len(rows) < lifecycleBatch {
			return errors.Join(errs...)
		}
	}
}

// billingPeriod is how often a plan's subscriptions roll. A plan with no
// pricing, or pricing with no period, rolls monthly, the period
// CreateSubscription has always given a new subscription.
func billingPeriod(p *plan.Plan) plan.Period {
	if p == nil || p.Pricing == nil || p.Pricing.BillingPeriod == "" {
		return plan.PeriodMonthly
	}
	return p.Pricing.BillingPeriod
}

// firstPeriodEnd is where a new subscription's first period ends: one period
// after start, on start's day of the month. Anything but yearly is a month,
// which keeps the old default for a plan billed "none".
func firstPeriodEnd(start time.Time, period plan.Period) time.Time {
	return shiftMonths(start, periodMonths(period), start.Day())
}

// nextPeriod returns the period after [start, end): it starts at end and runs
// one period on, to the subscription's anchor day.
func nextPeriod(start, end time.Time, period plan.Period) (nextStart, nextEnd time.Time) {
	return end, shiftMonths(end, periodMonths(period), anchorDay(start, end))
}

// anchorDay recovers the day of the month a subscription renews on from its
// current period, since no anchor is stored. It is the end's day, unless the
// end was clamped to the last day of a short month, which shows as a start on
// a later day. So a subscription that began on the 31st goes back to the 31st
// after February.
func anchorDay(start, end time.Time) int {
	if d := end.Day(); d == daysIn(end.Year(), end.Month()) && start.Day() > d {
		return start.Day()
	}
	return end.Day()
}

// periodMonths is one period's length in months: twelve for yearly, one for
// everything else.
func periodMonths(period plan.Period) int {
	if period == plan.PeriodYearly {
		return 12
	}
	return 1
}

// shiftMonths returns the time the given number of months after from (before
// it, when negative), on the given day of that month, clamped to the month's
// last day, at from's time of day, in UTC.
func shiftMonths(from time.Time, months, day int) time.Time {
	from = from.UTC()
	first := time.Date(from.Year(), from.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	return time.Date(first.Year(), first.Month(), min(day, daysIn(first.Year(), first.Month())),
		from.Hour(), from.Minute(), from.Second(), from.Nanosecond(), time.UTC)
}

// daysIn is the number of days in a month.
func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
