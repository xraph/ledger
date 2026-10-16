package ledger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/internal/telemetry"
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
//
// A run that moves something, or fails, is traced as "run lifecycle" with a
// child span per step that wrote or failed. A run that moves nothing leaves no
// span, so a clock ticking every minute over an idle store adds no traces.
func (l *Ledger) Advance(ctx context.Context, now time.Time) (LifecycleReport, error) {
	return l.advance(ctx, now, false)
}

// advance is Advance, traced and timed. background marks a run of the clock's
// own worker: its span begins a trace of its own, and a run Stop cut short
// counts as stopped rather than failed.
func (l *Ledger) advance(ctx context.Context, now time.Time, background bool) (report LifecycleReport, err error) {
	start := time.Now()
	run := &lifecycleRun{parent: ctx, background: background}
	ctx = context.WithValue(ctx, lifecycleRunKey{}, run)

	// finished stays false only when a step panics. The recover lives in the
	// worker (runLifecycle), or with whoever called Advance.
	finished := false
	defer func() {
		result := resultOK
		switch {
		case !finished:
			result = resultPanic
		case err == nil:
		case background && l.stopping():
			result = resultStopped
		default:
			result = resultError
		}
		l.metrics.lifecycleRuns.ObserveSince(start, result)
		run.end(report, result, err)
	}()

	var errs [4]error
	report.PeriodsAdvanced, errs[0] = l.AdvancePeriods(ctx, now)
	report.CancelsEnacted, errs[1] = l.EnactCancels(ctx, now)
	report.TrialsEnded, errs[2] = l.EndTrials(ctx, now)
	report.InvoicesPastDue, errs[3] = l.MarkInvoicesPastDue(ctx, now)
	finished = true
	return report, errors.Join(errs[:]...)
}

// lifecycleRunKey carries the *lifecycleRun of the Advance in progress, so a
// step can start the run's span when it first writes.
type lifecycleRunKey struct{}

// lifecycleRun is the "run lifecycle" span of one Advance. It is started by
// the first step that writes or fails, never up front, so a run that moves
// nothing records nothing. Steps run one after another, so it needs no lock.
type lifecycleRun struct {
	parent     context.Context
	background bool
	ctx        context.Context
	span       trace.Span
}

// begin starts the run's span if it has not started, and returns its context.
func (r *lifecycleRun) begin() context.Context {
	if r.span == nil {
		if r.background {
			r.ctx, r.span = telemetry.StartRoot(r.parent, "run lifecycle")
		} else {
			r.ctx, r.span = telemetry.Start(r.parent, "run lifecycle")
		}
	}
	return r.ctx
}

// end finishes the run's span, if it started, with what the run changed.
func (r *lifecycleRun) end(report LifecycleReport, result string, err error) {
	if r.span == nil {
		return
	}
	r.span.SetAttributes(
		attribute.Int(attrPeriodsAdvanced, len(report.PeriodsAdvanced)),
		attribute.Int(attrCancelsEnacted, len(report.CancelsEnacted)),
		attribute.Int(attrTrialsEnded, len(report.TrialsEnded)),
		attribute.Int(attrInvoicesPastDue, len(report.InvoicesPastDue)),
	)
	switch result {
	case resultPanic:
		telemetry.Fail(r.span, "panic")
	case resultError:
		telemetry.Fail(r.span, errorType(err))
	}
	r.span.End()
}

// lifecycleStep is one step of Advance: the name of its span and its value
// for the step label of the transitions metric.
type lifecycleStep struct{ span, label string }

var (
	stepAdvancePeriods  = lifecycleStep{"advance periods", "advance_periods"}
	stepEnactCancels    = lifecycleStep{"enact cancels", "enact_cancels"}
	stepEndTrials       = lifecycleStep{"end trials", "end_trials"}
	stepMarkInvoicesDue = lifecycleStep{"mark invoices past due", "mark_invoices_past_due"}
)

// startStep starts a step's span: under the run's span inside Advance, which
// it starts if no step has written yet, or under ctx when the step was called
// on its own.
func startStep(ctx context.Context, step lifecycleStep) (context.Context, trace.Span) {
	parent := ctx
	if run, ok := ctx.Value(lifecycleRunKey{}).(*lifecycleRun); ok {
		parent = run.begin()
	}
	return telemetry.Start(parent, step.span)
}

// AdvancePeriods moves every active, trialing or past-due subscription whose
// period has ended into the period that contains now, in one write however
// many periods it missed, and never past a scheduled cancellation. Periods
// follow the plan's billing period (see billingPeriod); a plan billed "none"
// never rolls. OnSubscriptionRenewed fires once per subscription moved, with
// every period that ended in the move.
//
// One catch-up walks at most maxCatchUpPeriods periods, a hundred years of
// months. A subscription further behind than that, whose period ended before
// any date this engine could have set, is left where it is and reported in
// the error on every run. It needs its period set by hand in the store; the
// clock never guesses how it got there.
func (l *Ledger) AdvancePeriods(ctx context.Context, now time.Time) ([]id.SubscriptionID, error) {
	now = now.UTC()
	plans := map[string]*plan.Plan{}
	var moved []id.SubscriptionID
	err := eachDue(ctx, l, stepAdvancePeriods, l.dueSubscriptions(subscription.DuePeriodEnd, runningStatuses, now),
		func(enter func() context.Context, sub *subscription.Subscription) (bool, error) {
			changed, err := l.advancePeriod(ctx, enter, sub, now, plans)
			if changed {
				moved = append(moved, sub.ID)
			}
			return changed, err
		})
	return moved, err
}

// advancePeriod moves one subscription on. It reads on ctx, and calls enter
// for the context of its write once it knows there is one to make (see
// eachDue).
func (l *Ledger) advancePeriod(ctx context.Context, enter func() context.Context, sub *subscription.Subscription, now time.Time, plans map[string]*plan.Plan) (bool, error) {
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
	ctx = enter()

	// Pinned to the end listed here: a replica that listed the row before
	// another moved it matches nothing and announces nothing.
	changed, err := l.store.AdvanceSubscriptionPeriod(ctx, sub.ID, sub.CurrentPeriodEnd, start, end, now)
	if err != nil {
		return false, fmt.Errorf("advance subscription %s: %w", sub.ID, err)
	}
	if !changed {
		return false, nil
	}
	renewed := l.storedOr(ctx, sub, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = start, end
	})
	_ = l.store.Invalidate(ctx, renewed.TenantID, renewed.AppID) //nolint:errcheck // best-effort cache invalidation
	l.plugins.EmitSubscriptionRenewed(hookContext(ctx), &subscription.Renewal{Subscription: renewed, Ended: ended})
	return true, nil
}

// EnactCancels cancels every subscription whose cancel_at is at or before now
// and that is not already canceled or expired, paused ones included, and sets
// canceled_at to cancel_at. OnSubscriptionCanceled fires for each, with the
// row read back after the write: an operator's scheduled cancel landing
// between the list and the write moves cancel_at, and the stored canceled_at
// is the one that counts.
//
// A running subscription whose period ended before its cancel_at is caught up
// first, as AdvancePeriods would, so the cancel ends the period it falls in
// and the periods before it are announced. Inside one Advance the period step
// has already done that; this covers another replica's cancel step landing
// between this replica's period list and its write, and a caller running
// EnactCancels on its own. The advance is conditional like any other, so a
// row another replica already moved is left alone.
func (l *Ledger) EnactCancels(ctx context.Context, now time.Time) ([]id.SubscriptionID, error) {
	now = now.UTC()
	plans := map[string]*plan.Plan{}
	var ended []id.SubscriptionID
	err := eachDue(ctx, l, stepEnactCancels, l.dueSubscriptions(subscription.DueCancel, cancellableStatuses, now),
		func(enter func() context.Context, sub *subscription.Subscription) (bool, error) {
			ctx := enter()
			var advanceErr error
			if slices.Contains(runningStatuses, sub.Status) && !sub.CurrentPeriodEnd.After(now) {
				// An error here still lets the cancel through: its date has
				// passed, whatever is wrong with the period.
				_, advanceErr = l.advancePeriod(ctx, enter, sub, now, plans)
			}
			changed, err := l.store.EnactSubscriptionCancel(ctx, sub.ID, now)
			if err != nil {
				return false, errors.Join(advanceErr, fmt.Errorf("cancel subscription %s: %w", sub.ID, err))
			}
			if !changed {
				return false, advanceErr
			}
			canceled := l.storedOr(ctx, sub, func(x *subscription.Subscription) {
				if x.CancelAt != nil {
					canceledAt := *x.CancelAt
					x.CanceledAt = &canceledAt
				}
				x.Status = subscription.StatusCanceled
			})
			_ = l.store.Invalidate(ctx, canceled.TenantID, canceled.AppID) //nolint:errcheck // best-effort cache invalidation
			l.plugins.EmitSubscriptionCanceled(hookContext(ctx), canceled)
			ended = append(ended, sub.ID)
			return true, advanceErr
		})
	return ended, err
}

// EndTrials makes every trialing subscription whose trial_end is at or before
// now active. A paused subscription's trial is left alone. OnSubscriptionTrialEnded
// fires for each.
func (l *Ledger) EndTrials(ctx context.Context, now time.Time) ([]id.SubscriptionID, error) {
	now = now.UTC()
	var ended []id.SubscriptionID
	err := eachDue(ctx, l, stepEndTrials, l.dueSubscriptions(subscription.DueTrialEnd, []subscription.Status{subscription.StatusTrialing}, now),
		func(enter func() context.Context, sub *subscription.Subscription) (bool, error) {
			ctx := enter()
			changed, err := l.store.EndSubscriptionTrial(ctx, sub.ID, now)
			if err != nil {
				return false, fmt.Errorf("end the trial of subscription %s: %w", sub.ID, err)
			}
			if !changed {
				return false, nil
			}
			active := l.storedOr(ctx, sub, func(x *subscription.Subscription) {
				x.Status = subscription.StatusActive
			})
			_ = l.store.Invalidate(ctx, active.TenantID, active.AppID) //nolint:errcheck // best-effort cache invalidation
			l.plugins.EmitSubscriptionTrialEnded(hookContext(ctx), active)
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
	err := eachDue(ctx, l, stepMarkInvoicesDue, list, func(enter func() context.Context, inv *invoice.Invoice) (bool, error) {
		ctx := enter()
		changed, err := l.store.MarkInvoicePastDue(ctx, inv.ID, now)
		if err != nil {
			return false, fmt.Errorf("mark invoice %s past due: %w", inv.ID, err)
		}
		if !changed {
			return false, nil
		}
		// The invoice as stored, falling back to the listed one with its new
		// status. A copy either way: the memory store hands out its own
		// pointer.
		pastDue := *inv
		pastDue.Status = invoice.StatusPastDue
		pastDue.Touch()
		if stored, readErr := l.store.GetInvoice(ctx, inv.ID); readErr == nil {
			pastDue = *stored
		}
		l.plugins.EmitInvoicePastDue(hookContext(ctx), &pastDue)
		marked = append(marked, inv.ID)
		return true, nil
	})
	return marked, err
}

// storedOr reads a subscription back after a write matched, so a hook
// announces the row as stored: its updated_at, and whatever another writer
// changed beside the write, such as a plan change landing between the list
// and the write. If the read fails the write still happened, so the listed
// row stands in, with apply making the change the write made.
func (l *Ledger) storedOr(ctx context.Context, listed *subscription.Subscription, apply func(*subscription.Subscription)) *subscription.Subscription {
	if stored, err := l.store.GetSubscription(ctx, listed.ID); err == nil {
		return stored
	}
	fallback := *listed
	apply(&fallback)
	fallback.Touch()
	return &fallback
}

// hookContext is the context a lifecycle hook runs on: the run's values,
// without its deadline or its cancel. The hook fires after its store write has
// landed, and no later run will announce that transition again, so a run
// that hits its deadline, or a Stop during a deploy, must not cut the
// announcement off, nor fail the GenerateInvoice a billing plugin calls from
// it. The registry still stops waiting on a hook after its own timeout, but
// the hook carries on, on this live context, and may still finish; only its
// error goes unseen.
//
// Delivery stays at most once and in-process: a crash between the write and
// the hook, or a hook that fails, loses the announcement for good. A billing
// plugin reconciles by listing its subscriptions and invoicing, with
// ForPeriod, every ended period that has no invoice; ErrAlreadyExists marks
// the ones already billed.
func hookContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
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
//
// apply calls enter right before it writes, and does its writes and fires its
// hooks on the context enter returns. The first call starts the step's span
// (and the run's, inside Advance), so a step whose rows all stay where they
// are, such as a subscription on a plan billed "none" whose period has ended,
// records no span. A row that fails starts it too, as does a failed list.
// Each row moved, and each row that failed to move, counts on the
// transitions metric.
func eachDue[T any](ctx context.Context, l *Ledger, step lifecycleStep, list func(context.Context, int) ([]T, error), apply func(enter func() context.Context, row T) (bool, error)) (err error) {
	var (
		errs            []error
		offset          int
		span            trace.Span
		changed, failed int
	)
	stepCtx := ctx
	enter := func() context.Context {
		if span == nil {
			stepCtx, span = startStep(ctx, step)
		}
		return stepCtx
	}
	defer func() {
		if span == nil {
			return
		}
		span.SetAttributes(attribute.Int(attrStepChanged, changed), attribute.Int(attrStepFailed, failed))
		// A run that was canceled (Stop does that) with no row failing has
		// not failed.
		if err != nil && (failed > 0 || !errors.Is(err, context.Canceled)) {
			telemetry.Fail(span, errorType(err))
		}
		span.End()
	}()

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(append(errs, ctxErr)...)
		}
		rows, listErr := list(stepCtx, offset)
		if listErr != nil {
			enter()
			return errors.Join(append(errs, listErr)...)
		}
		for _, row := range rows {
			moved, rowErr := apply(enter, row)
			switch {
			case moved:
				changed++
				l.metrics.lifecycleTransitions.Inc(step.label, resultOK)
			case rowErr != nil:
				failed++
				l.metrics.lifecycleTransitions.Inc(step.label, resultError)
			}
			if rowErr != nil {
				enter()
				errs = append(errs, rowErr)
			}
			if !moved {
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
// which keeps the old default for a plan billed "none". Days are UTC days.
func firstPeriodEnd(start time.Time, period plan.Period) time.Time {
	start = start.UTC()
	return shiftMonths(start, periodMonths(period), start.Day())
}

// nextPeriod returns the period after [start, end): it starts at end and runs
// one period on, to the subscription's anchor day. Days are UTC days.
func nextPeriod(start, end time.Time, period plan.Period) (nextStart, nextEnd time.Time) {
	start, end = start.UTC(), end.UTC()
	return end, shiftMonths(end, periodMonths(period), anchorDay(start, end))
}

// anchorDay recovers the day of the month a subscription renews on from its
// current period, since no anchor is stored. It is the end's day, unless the
// end was clamped to the last day of a short month, which shows as a start on
// a later day. So a subscription that began on the 31st goes back to the 31st
// after February. Days are UTC days.
func anchorDay(start, end time.Time) int {
	start, end = start.UTC(), end.UTC()
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
