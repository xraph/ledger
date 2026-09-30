package sqlite

import (
	"testing"
	"time"

	"github.com/xraph/ledger/plan"
)

// 05:00 on 1 January 2026 at UTC+14 is 15:00 on 31 December 2025 in UTC. A
// usage window opens on the UTC calendar, so the month is December and the
// year is 2025, whatever zone the time carries.
func TestGetStartOfPeriodOpensInUTC(t *testing.T) {
	in := time.Date(2026, time.January, 1, 5, 0, 0, 0, time.FixedZone("UTC+14", 14*60*60))
	cases := []struct {
		period plan.Period
		want   time.Time
	}{
		{plan.PeriodMonthly, time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC)},
		{plan.PeriodYearly, time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{plan.PeriodNone, time.Time{}},
	}
	for _, c := range cases {
		got := getStartOfPeriod(in, c.period)
		if !got.Equal(c.want) || got.Location() != time.UTC {
			t.Errorf("%s: got %v, want %v in UTC", c.period, got, c.want)
		}
	}
}
