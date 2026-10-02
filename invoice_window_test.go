package ledger_test

import (
	"context"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

// windowFixture is an engine on a clock the test moves, over a memory store,
// with the metered plan (1000 api_calls included, then 3 cents each) and a
// subscription created on 15 September 2026 at 10:00, so it renews on the
// 15th and not on the 1st.
func windowFixture(t *testing.T) (l *ledger.Ledger, s *memory.Store, sub *subscription.Subscription, setNow func(time.Time)) {
	t.Helper()
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	s = memory.New()
	l = ledger.New(s, ledger.WithClock(func() time.Time { return now }))
	p := activePlanIn(t, l, "metered", "app_1")
	sub = &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(context.Background(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	return l, s, sub, func(t time.Time) { now = t }
}

func ingestCalls(t *testing.T, s *memory.Store, sub *subscription.Subscription, calls map[time.Time]int64) {
	t.Helper()
	var events []*meter.UsageEvent
	for ts, qty := range calls {
		events = append(events, &meter.UsageEvent{
			ID: id.NewUsageEventID(), TenantID: sub.TenantID, AppID: sub.AppID,
			FeatureKey: "api_calls", Quantity: qty, Timestamp: ts,
		})
	}
	if err := s.IngestBatch(context.Background(), events); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}
}

// billedCalls is the usage an invoice counted: its overage plus the 1000
// calls the plan includes.
func billedCalls(t *testing.T, inv *invoice.Invoice) int64 {
	t.Helper()
	overage := lineItemsOfType(inv, invoice.LineItemOverage)
	if len(overage) != 1 {
		t.Fatalf("overage lines %+v, want one", overage)
	}
	return overage[0].Quantity + 1000
}

// Usage that straddles a renewal on the 15th lands on exactly one of two
// consecutive invoices: the rolled period named with ForPeriod, and the new
// period billed as the current one while it runs. A calendar month would put
// 1 to 15 October on both.
func TestConsecutiveInvoicesBillUsageAcrossANon1stAnchorOnce(t *testing.T) {
	ctx := context.Background()
	l, s, sub, setNow := windowFixture(t)
	anchor := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	ingestCalls(t, s, sub, map[time.Time]int64{
		time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC):   1000,  // first period
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC):   2000,  // first period, after the 1st
		anchor.Add(-time.Nanosecond):                   4000,  // first period, its last instant
		anchor:                                         8000,  // on the renewal: the second period's
		time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC):  16000, // second period, before now
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC):   32000, // second period, after now: not yet billed
		time.Date(2026, 9, 15, 9, 59, 0, 0, time.UTC):  64000, // before the subscription existed
		time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC): 128000,
	})

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	setNow(now)
	if _, err := l.Advance(ctx, now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	first, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC), anchor))
	if err != nil {
		t.Fatalf("GenerateInvoice for the rolled period: %v", err)
	}
	current, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice for the current period: %v", err)
	}
	if !current.PeriodStart.Equal(anchor) {
		t.Fatalf("current invoice starts %v, want the renewal %v", current.PeriodStart, anchor)
	}

	if got := billedCalls(t, first); got != 7000 {
		t.Errorf("the rolled period billed %d calls, want 7000 (20 September, 1 October and its last instant)", got)
	}
	if got := billedCalls(t, current); got != 24000 {
		t.Errorf("the running period billed %d calls, want 24000 (the renewal instant and 16 October, nothing after now)", got)
	}
}

// A subscription canceled at its period end keeps that period as its current
// one for good. Its final invoice bills the period's own usage, not the
// calendar month the invoice happens to be generated in.
func TestACanceledSubscriptionsFinalPeriodBillsItsOwnUsage(t *testing.T) {
	ctx := context.Background()
	l, s, sub, setNow := windowFixture(t)
	if err := l.CancelSubscription(ctx, sub.ID, false); err != nil {
		t.Fatalf("CancelSubscription: %v", err)
	}
	ingestCalls(t, s, sub, map[time.Time]int64{
		time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC):  1000, // in the period, before October
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC):  2000, // in the period
		time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC): 4000, // after the cancel
	})

	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	setNow(now)
	if _, err := l.Advance(ctx, now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got := reload(t, l, sub); got.Status != subscription.StatusCanceled {
		t.Fatalf("status %s, want canceled", got.Status)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if !inv.PeriodEnd.Equal(time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("final invoice ends %v, want the canceled period's end", inv.PeriodEnd)
	}
	if got := billedCalls(t, inv); got != 3000 {
		t.Errorf("final period billed %d calls, want 3000: September's usage counts, and nothing after the cancel", got)
	}
}
