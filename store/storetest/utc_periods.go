package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	ledgerstore "github.com/xraph/ledger/store"
)

// testAggregateOpensPeriodsInUTC pins that a usage window opens at midnight
// UTC, whatever zone the server runs in. It moves time.Local to UTC+14 for the
// two Aggregate calls only. A start built in the local zone opened the month
// fourteen hours early, or late in a UTC month in the next month altogether,
// so either the event a second before the UTC start was counted or the one on
// it was not. Moving time.Local is safe because Run never runs its subtests in
// parallel. If the UTC month turns between the fixture and the calls
// (midnight on the 1st), run it again.
func testAggregateOpensPeriodsInUTC(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	tenantID := "tenant-" + uniqueSuffix()
	monthly := "month-" + uniqueSuffix()
	yearly := "year-" + uniqueSuffix()

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	yearStart := time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	event := func(key string, at time.Time, qty int64) *meter.UsageEvent {
		return &meter.UsageEvent{
			ID: id.NewUsageEventID(), TenantID: tenantID, AppID: appID,
			FeatureKey: key, Quantity: qty, Timestamp: at,
		}
	}
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{
		event(monthly, monthStart.Add(-time.Second), 100), event(monthly, monthStart, 1),
		event(yearly, yearStart.Add(-time.Second), 100), event(yearly, yearStart, 1),
	}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = time.FixedZone("UTC+14", 14*60*60)
	gotMonth, errMonth := s.Aggregate(ctx, tenantID, appID, monthly, plan.PeriodMonthly)
	gotYear, errYear := s.Aggregate(ctx, tenantID, appID, yearly, plan.PeriodYearly)
	time.Local = saved

	if errMonth != nil || errYear != nil {
		t.Fatalf("Aggregate: monthly %v, yearly %v", errMonth, errYear)
	}
	if gotMonth != 1 {
		t.Errorf("monthly total %d, want 1: the month opens at %v, so the event a second earlier is last month's", gotMonth, monthStart)
	}
	if gotYear != 1 {
		t.Errorf("yearly total %d, want 1: the year opens at %v, so the event a second earlier is last year's", gotYear, yearStart)
	}
}
