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
// have started, and must end after the subscription was created; anything else
// is refused with ErrInvalidInput. A second live invoice for the same period is
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
func periodBelongsTo(sub *subscription.Subscription, period plan.Period, want subscription.Period, now time.Time) bool {
	if !want.Start.Before(want.End) || want.Start.After(now) || !want.End.After(sub.CreatedAt) {
		return false
	}
	if period != plan.PeriodMonthly && period != plan.PeriodYearly {
		return false
	}
	start, end := sub.CurrentPeriodStart.UTC(), sub.CurrentPeriodEnd.UTC()
	day := anchorDay(start, end)
	for range maxCatchUpPeriods {
		if start.Before(want.End) {
			return false
		}
		if start.Equal(want.End) {
			if first, ok := clampedFirstStart(sub, period, want.End); ok {
				// The anchor is lost where a year from 29 February lands on
				// the 28th, so the walk back cannot tell 29 February to 28
				// February from 28 to 28. The subscription's own creation
				// day settles it: want is that first period, or it is not a
				// period at all.
				return want.Start.Equal(first) && reproduces(want, period, start, end)
			}
			return shiftMonths(start, -periodMonths(period), day).Equal(want.Start)
		}
		start, end = shiftMonths(start, -periodMonths(period), day), start
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

// usageInPeriod totals a feature's usage over the subscription's period,
// [start, end), from the events themselves. A named past period cannot use
// store.Aggregate, which counts from the start of the calendar period at the
// time of the call. QueryUsage drops its app filter when the app id is empty,
// so events from another app are skipped here.
func (l *Ledger) usageInPeriod(ctx context.Context, sub *subscription.Subscription, pf plan.Feature) (int64, error) {
	events, err := l.store.QueryUsage(ctx, sub.TenantID, sub.AppID, meter.QueryOpts{
		FeatureKey: pf.Key,
		Start:      sub.CurrentPeriodStart,
		End:        sub.CurrentPeriodEnd,
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
	if total < 0 {
		return 0, fmt.Errorf("usage for the period totals %d, below zero", total)
	}
	return total, nil
}
