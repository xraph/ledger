package ledger

import (
	"testing"
	"time"

	"github.com/xraph/ledger/plan"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestNextPeriod(t *testing.T) {
	cases := []struct {
		name       string
		start, end time.Time
		period     plan.Period
		want       time.Time
	}{
		{"back to the 31st after February", day(2026, 1, 31), day(2026, 2, 28), plan.PeriodMonthly, day(2026, 3, 31)},
		{"the 31st holds through March", day(2026, 2, 28), day(2026, 3, 31), plan.PeriodMonthly, day(2026, 4, 30)},
		{"and through April", day(2026, 3, 31), day(2026, 4, 30), plan.PeriodMonthly, day(2026, 5, 31)},
		{"a leap February", day(2028, 1, 31), day(2028, 2, 29), plan.PeriodMonthly, day(2028, 3, 31)},
		{"the 30th", day(2026, 1, 30), day(2026, 2, 28), plan.PeriodMonthly, day(2026, 3, 30)},
		{"after the 30th settles", day(2026, 2, 28), day(2026, 3, 30), plan.PeriodMonthly, day(2026, 4, 30)},
		{"a provider's own cadence keeps its end day", day(2026, 1, 10), day(2026, 2, 9), plan.PeriodMonthly, day(2026, 3, 9)},
		{"a leap day, yearly", day(2024, 2, 29), day(2025, 2, 28), plan.PeriodYearly, day(2026, 2, 28)},
		{
			"the time of day is kept",
			time.Date(2026, 1, 15, 13, 45, 0, 0, time.UTC), time.Date(2026, 2, 15, 13, 45, 0, 0, time.UTC),
			plan.PeriodMonthly, time.Date(2026, 3, 15, 13, 45, 0, 0, time.UTC),
		},
	}
	for _, c := range cases {
		start, end := nextPeriod(c.start, c.end, c.period)
		if !start.Equal(c.end) || !end.Equal(c.want) {
			t.Errorf("%s: got %v to %v, want %v to %v", c.name, start, end, c.end, c.want)
		}
	}
}

func TestFirstPeriodEnd(t *testing.T) {
	cases := []struct {
		start  time.Time
		period plan.Period
		want   time.Time
	}{
		{day(2026, 1, 31), plan.PeriodMonthly, day(2026, 2, 28)},
		{day(2026, 3, 10), plan.PeriodYearly, day(2027, 3, 10)},
		{day(2024, 2, 29), plan.PeriodYearly, day(2025, 2, 28)},
		{day(2026, 3, 10), plan.PeriodNone, day(2026, 4, 10)},
	}
	for _, c := range cases {
		if got := firstPeriodEnd(c.start, c.period); !got.Equal(c.want) {
			t.Errorf("%v %s: got %v, want %v", c.start, c.period, got, c.want)
		}
	}
}

func TestBillingPeriod(t *testing.T) {
	cases := []struct {
		name string
		p    *plan.Plan
		want plan.Period
	}{
		{"no pricing", &plan.Plan{}, plan.PeriodMonthly},
		{"no period", &plan.Plan{Pricing: &plan.Pricing{}}, plan.PeriodMonthly},
		{"yearly", &plan.Plan{Pricing: &plan.Pricing{BillingPeriod: plan.PeriodYearly}}, plan.PeriodYearly},
		{"none", &plan.Plan{Pricing: &plan.Pricing{BillingPeriod: plan.PeriodNone}}, plan.PeriodNone},
	}
	for _, c := range cases {
		if got := billingPeriod(c.p); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
