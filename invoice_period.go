package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
)

// InvoiceOption configures GenerateInvoice.
type InvoiceOption func(*invoiceConfig)

type invoiceConfig struct {
	period *subscription.Period
}

// ForPeriod makes GenerateInvoice bill a period the subscription has already
// had, not its current one: the period the lifecycle clock just rolled over,
// say, or one a catch-up skipped (OnSubscriptionRenewed lists them). The period
// must be the current one or one before it on the subscription's cadence, must
// have started, and must end after the subscription was created; anything
// else is refused with ErrInvalidInput. A period a resume stretched counts
// with its stretched end, and the walk crosses it (see periodBelongsTo); a
// period from before an older stretch, which Ledger no longer remembers, is
// refused. A paused subscription's current period is not finished, so it
// cannot be invoiced until it is resumed and its end passes. A second live invoice for the same period is
// refused with ErrAlreadyExists, as for the current period. Nothing calls this
// on its own: Ledger never bills a period automatically.
//
// Only the usage is the period's own. The base fee, the seats, the coupons and
// the prices come from the subscription and its plan as they are now, because
// ChangePlan leaves the period alone and Ledger keeps no history of any of
// them. A plan whose billing period changed no longer lines up with the periods
// before the change, and those are refused.
func ForPeriod(start, end time.Time) InvoiceOption {
	return func(c *invoiceConfig) {
		c.period = &subscription.Period{Start: start.UTC(), End: end.UTC()}
	}
}

// namedPeriod returns the period an invoice bills when the caller named one
// other than the current period, and nil when it bills the current period.
func namedPeriod(sub *subscription.Subscription, p *plan.Plan, opts []InvoiceOption, now time.Time) (*subscription.Period, error) {
	var cfg invoiceConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.period == nil {
		return nil, nil
	}
	want := *cfg.period
	if want.Start.Equal(sub.CurrentPeriodStart) && want.End.Equal(sub.CurrentPeriodEnd) {
		return nil, nil
	}
	if !periodBelongsTo(sub, billingPeriod(p), want, now) {
		return nil, fmt.Errorf("%w: subscription %s had no billing period from %s to %s",
			ErrInvalidInput, sub.ID, want.Start.Format(time.RFC3339), want.End.Format(time.RFC3339))
	}
	return &want, nil
}

// periodBelongsTo reports whether want is a period the subscription had before
// its current one. No history is stored, so it walks back from the current
// period one billing period at a time, on the anchor day the clock renews on,
// until it reaches the period that ends where want ends. The period must also
// have started by now and ended after the subscription was created. A plan
// billed "none", or one whose period Ledger does not know, has only its
// current period.
//
// A resume stretches a period, which breaks the cadence: the periods after a
// stretched one follow the clock's anchor from its new end (whatever
// nextPeriod gives), and the ones before it ran on the cadence that led to
// its original end. Only the most recent stretch is remembered
// (Subscription.Stretch). The periods after it are rebuilt forwards with
// nextPeriod, exactly as the clock built them, because a walk back from a
// later period can land beside the real one when an anchor was lost (a yearly
// stretch ending on 29 February settles on the 28th). The periods before it
// walk back on the old cadence, or, when an older stretch's end is the Floor,
// are rebuilt forwards from there. Below the Floor nothing can be proved.
// Anything that cannot be proved is refused, never guessed.
func periodBelongsTo(sub *subscription.Subscription, period plan.Period, want subscription.Period, now time.Time) bool {
	// The start-before-end check is a cheap early exit: the walk below can
	// never match a period that is empty or runs backwards, so it is redundant.
	if !want.Start.Before(want.End) || want.Start.After(now) || !want.End.After(sub.CreatedAt) {
		return false
	}
	if period != plan.PeriodMonthly && period != plan.PeriodYearly {
		return false
	}
	current := subscription.Period{Start: sub.CurrentPeriodStart.UTC(), End: sub.CurrentPeriodEnd.UTC()}
	st := sub.Stretch
	if st == nil {
		return walkBack(sub, period, want, current.Start, current.End, true)
	}

	stretched := subscription.Period{Start: st.Start.UTC(), End: st.End.UTC()}
	// The stretched period and every one the clock built after it, up to the
	// current one. If they do not lead there, the record does not describe
	// this subscription, and nothing is proved.
	after, ok := forwardTo(stretched, current, period)
	if !ok {
		return false
	}
	for _, p := range after {
		if p.Start.Equal(want.Start) && p.End.Equal(want.End) {
			return true
		}
	}
	if want.End.After(stretched.Start) {
		return false
	}

	before := subscription.Period{Start: stretched.Start, End: st.OriginalEnd.UTC()}
	if st.Floor == nil {
		return walkBack(sub, period, want, before.Start, before.End, true)
	}
	floor := st.Floor.UTC()
	if want.Start.Before(floor) {
		return false
	}
	return onFloorCadence(floor, before, period, want)
}

// forwardTo lists the periods the clock builds from first, with nextPeriod,
// up to and including target, and reports whether it reaches target exactly.
func forwardTo(first, target subscription.Period, period plan.Period) ([]subscription.Period, bool) {
	var out []subscription.Period
	p := first
	for range maxCatchUpPeriods {
		out = append(out, p)
		if !p.Start.Before(target.Start) {
			return out, p.Start.Equal(target.Start) && p.End.Equal(target.End)
		}
		p.Start, p.End = nextPeriod(p.Start, p.End, period)
	}
	return nil, false
}

// onFloorCadence reports whether want is one of the periods from floor up to
// next, the period the cadence led to. That cadence began at floor, the end
// of an older stretch, on an anchor day Ledger did not keep: floor's own day,
// or, when floor is a month's last day, any later day the older stretch's
// start could have had. Each candidate is rebuilt forwards as the clock
// would have, and want counts only if every candidate that reaches next
// exactly contains it.
func onFloorCadence(floor time.Time, next subscription.Period, period plan.Period, want subscription.Period) bool {
	days := []int{floor.Day()}
	if floor.Day() == daysIn(floor.Year(), floor.Month()) {
		for d := floor.Day() + 1; d <= 31; d++ {
			days = append(days, d)
		}
	}
	reached := 0
	for _, day := range days {
		first := subscription.Period{Start: floor, End: shiftMonths(floor, periodMonths(period), day)}
		chain, ok := forwardTo(first, next, period)
		if !ok {
			continue
		}
		reached++
		found := false
		for _, p := range chain {
			if p.Start.Equal(want.Start) && p.End.Equal(want.End) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return reached > 0
}

// walkBack reports whether want is one of the periods before [start, end) on
// that period's own cadence. fromCreation says the cadence runs back to the
// subscription's creation, where the 29 February rule applies.
func walkBack(sub *subscription.Subscription, period plan.Period, want subscription.Period, start, end time.Time, fromCreation bool) bool {
	months, day := periodMonths(period), anchorDay(start, end)
	for range maxCatchUpPeriods {
		if start.Before(want.End) {
			return false
		}
		if start.Equal(want.End) {
			// Only a yearly plan loses its anchor: a monthly plan's anchorDay
			// always recovers the 29th to the 31st from the period itself, so
			// the walk is exact and the rule below would only refuse real
			// periods, such as an import whose provider period started a
			// few days before the creation day.
			if first, ok := clampedFirstStart(sub, period, want.End); ok && fromCreation && period == plan.PeriodYearly {
				// The anchor is lost where a year from 29 February lands on
				// the 28th, so the walk back cannot tell 29 February to 28
				// February from 28 to 28. The subscription's own creation
				// day settles it: want is that first period, or it is not a
				// period at all.
				return want.Start.Equal(first) && reproduces(want, period, start, end)
			}
			return shiftMonths(start, -months, day).Equal(want.Start)
		}
		start, end = shiftMonths(start, -months, day), start
	}
	return false
}

// clampedFirstStart returns where the subscription's first period started when
// it began on a day the month it ended in does not have: a yearly subscription
// created on 29 February has a first period ending on the 28th. The start is
// the subscription's creation day, one period before end, and ok is false
// unless end is a month's last day and the creation day is later than it.
func clampedFirstStart(sub *subscription.Subscription, period plan.Period, end time.Time) (first time.Time, ok bool) {
	created := sub.CreatedAt.UTC()
	if end.Day() != daysIn(end.Year(), end.Month()) || created.Day() <= end.Day() {
		return time.Time{}, false
	}
	first = shiftMonths(end, -periodMonths(period), created.Day())
	if !first.Before(end) || first.Year() != created.Year() || first.YearDay() != created.YearDay() {
		return time.Time{}, false
	}
	return first, true
}

// reproduces reports whether the period after want is [nextStart, nextEnd):
// the check that a candidate past period leads to the one the clock reached.
func reproduces(want subscription.Period, period plan.Period, nextStart, nextEnd time.Time) bool {
	s, e := nextPeriod(want.Start, want.End, period)
	return s.Equal(nextStart) && e.Equal(nextEnd)
}

// usageWindow is the part of a billed period whose usage an invoice counts,
// [start, end) once the period has ended and [start, now) while it is still
// running. Every invoice reads this one window, whether it bills the current
// period or names one, so consecutive invoices never count an event twice and
// never skip one. A calendar window (store.Aggregate) would overlap the
// periods of any subscription that does not renew on the 1st.
func usageWindow(billed subscription.Period, now time.Time) subscription.Period {
	if now.Before(billed.End) {
		billed.End = now
	}
	return billed
}

// usageInPeriod totals a feature's usage over window, from the events
// themselves. QueryUsage drops its app filter when the app id is empty, so
// events from another app are skipped here. A window that ends at or before
// its start holds no usage.
func (l *Ledger) usageInPeriod(ctx context.Context, sub *subscription.Subscription, pf plan.Feature, window subscription.Period) (int64, error) {
	if !window.End.After(window.Start) {
		return 0, nil
	}
	events, err := l.store.QueryUsage(ctx, sub.TenantID, sub.AppID, meter.QueryOpts{
		FeatureKey: pf.Key,
		Start:      window.Start,
		End:        window.End,
	})
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range events {
		if e.AppID == sub.AppID {
			total += e.Quantity
		}
	}
	// Meter accepts negative quantities (a correction, say), so the sum can
	// come back below zero. That is refused rather than billed as nothing: a
	// silent zero would hide an overage along with whatever made the total
	// negative.
	if total < 0 {
		return 0, fmt.Errorf("usage for the period totals %d, below zero", total)
	}
	return total, nil
}
