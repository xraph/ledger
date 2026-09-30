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

// testAggregateOpensPeriodsInUTC pins, end to end through each store, that a
// usage window opens at midnight UTC: an event a second before the UTC month
// or year start belongs to the period before, and one on it is counted. The
// arithmetic itself is pinned without a database by each store's unit test of
// getStartOfPeriod, which feeds it explicit UTC+14 times. This subtest does
// not move time.Local to reproduce a server zone: that write races every
// goroutine reading the clock, the mongo driver's monitors among them, and
// fails the suite under -race. If the UTC month turns between the fixture and
// the calls (midnight on the 1st), run it again.
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

	gotMonth, errMonth := s.Aggregate(ctx, tenantID, appID, monthly, plan.PeriodMonthly)
	gotYear, errYear := s.Aggregate(ctx, tenantID, appID, yearly, plan.PeriodYearly)

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
