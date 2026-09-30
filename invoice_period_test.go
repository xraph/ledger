package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

// Every period a catch-up ended can be invoiced once, each with its own usage.
func TestGenerateInvoiceForEveryEndedPeriod(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	ev := &lifecycleEvents{}
	l := ledger.New(s, ledger.WithPlugin(ev))
	p := activePlanIn(t, l, "metered", "app_1") // 1000 api_calls included, then 3 cents each
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 1, 31), at(2026, 2, 28)
	})
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{{
		ID: id.NewUsageEventID(), TenantID: sub.TenantID, AppID: sub.AppID,
		FeatureKey: "api_calls", Quantity: 1500, Timestamp: at(2026, 3, 15),
	}}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}
	if _, err := l.Advance(ctx, at(2026, 5, 15)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(ev.renewals) != 1 || len(ev.renewals[0].Ended) != 3 {
		t.Fatalf("renewals %+v, want one listing three ended periods", ev.renewals)
	}

	for _, period := range ev.renewals[0].Ended {
		inv, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(period.Start, period.End))
		if err != nil {
			t.Fatalf("GenerateInvoice for %v to %v: %v", period.Start, period.End, err)
		}
		if !inv.PeriodStart.Equal(period.Start) || !inv.PeriodEnd.Equal(period.End) {
			t.Errorf("invoice period %v to %v, want %v to %v", inv.PeriodStart, inv.PeriodEnd, period.Start, period.End)
		}
		overage := lineItemsOfType(inv, invoice.LineItemOverage)
		if period.Start.Equal(at(2026, 2, 28)) {
			if len(overage) != 1 || overage[0].Quantity != 500 {
				t.Errorf("the period holding 1500 calls bills overage %+v, want 500 over the allowance", overage)
			}
		} else if len(overage) != 0 {
			t.Errorf("period %v bills overage %+v, want none: its usage is its own", period.Start, overage)
		}
	}

	first := ev.renewals[0].Ended[0]
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(first.Start, first.End)); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Errorf("a second invoice for an ended period: got %v, want ErrAlreadyExists", err)
	}
	current, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil || !current.PeriodStart.Equal(at(2026, 4, 30)) || !current.PeriodEnd.Equal(at(2026, 5, 31)) {
		t.Errorf("with no period: %+v, %v; want the current period, 30 April to 31 May", current, err)
	}
}

func TestGenerateInvoiceRefusesAPeriodTheSubscriptionNeverHad(t *testing.T) {
	ctx := context.Background()
	l, s, _, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 4, 30), at(2026, 5, 31)
	})
	once := planBilled(t, l, "once", plan.PeriodNone)
	oneOff := seedSub(t, s, once, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 3, 1), at(2026, 4, 1)
	})

	for _, c := range []struct {
		name       string
		sub        *subscription.Subscription
		start, end time.Time
	}{
		{"off the anchor day", sub, at(2026, 3, 1), at(2026, 4, 1)},
		{"after the current period", sub, at(2026, 5, 31), at(2026, 6, 30)},
		{"backwards", sub, at(2026, 4, 30), at(2026, 3, 31)},
		{"before the subscription existed", sub, at(2025, 11, 30), at(2025, 12, 31)},
		{"an earlier period of a plan billed once", oneOff, at(2026, 2, 1), at(2026, 3, 1)},
	} {
		if _, err := l.GenerateInvoice(ctx, c.sub.ID, ledger.ForPeriod(c.start, c.end)); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Errorf("%s: got %v, want ErrInvalidInput", c.name, err)
		}
	}

	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 3, 31), at(2026, 4, 30))); err != nil {
		t.Errorf("the period before the current one: %v", err)
	}
	inv, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 4, 30), at(2026, 5, 31)))
	if err != nil || !inv.PeriodEnd.Equal(at(2026, 5, 31)) {
		t.Errorf("naming the current period: %+v, %v", inv, err)
	}
}

// A named period totals its events over [start, end): one on the start belongs
// to it, one on the end to the next period, and another app's never count.
func TestGenerateInvoiceForAPeriodCountsItsOwnEventsOnly(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)
	p := activePlanIn(t, l, "metered", "app_1")
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 4, 30), at(2026, 5, 31)
	})
	event := func(app string, qty int64, ts time.Time) *meter.UsageEvent {
		return &meter.UsageEvent{
			ID: id.NewUsageEventID(), TenantID: sub.TenantID, AppID: app,
			FeatureKey: "api_calls", Quantity: qty, Timestamp: ts,
		}
	}
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{
		event("app_1", 600, at(2026, 3, 31)), // on the start: in
		event("app_1", 600, at(2026, 4, 29)), // in
		event("app_1", 900, at(2026, 4, 30)), // on the end: the next period's
		event("app_2", 5000, at(2026, 4, 1)), // another app's
		event("app_1", 700, at(2026, 3, 30)), // the period before
	}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 3, 31), at(2026, 4, 30)))
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	overage := lineItemsOfType(inv, invoice.LineItemOverage)
	if len(overage) != 1 || overage[0].Quantity != 200 {
		t.Errorf("overage %+v, want 200 over the allowance (1200 calls in the period)", overage)
	}
}

// A yearly plan walks back a year at a time, on its anchor day. One created on
// 29 February rolls to the 28th and the store keeps no anchor, so the walk back
// must still find the real first period (29 February to 28 February) and must
// refuse the phantom one that starts a day early.
func TestGenerateInvoiceForAYearlyPlansPreviousYear(t *testing.T) {
	ctx := context.Background()
	l, s, ev, _ := lifecycleFixture(t)
	yearly := planBilled(t, l, "annual", plan.PeriodYearly)
	sub := seedSub(t, s, yearly, func(x *subscription.Subscription) {
		x.CreatedAt = at(2024, 2, 29)
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2024, 2, 29), at(2025, 2, 28)
	})
	if _, err := l.Advance(ctx, at(2026, 3, 1)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(ev.renewals) != 1 || len(ev.renewals[0].Ended) != 2 {
		t.Fatalf("renewals %+v, want one listing two ended years", ev.renewals)
	}

	for _, period := range ev.renewals[0].Ended {
		if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(period.Start, period.End)); err != nil {
			t.Errorf("the period the hook listed, %v to %v: %v", period.Start, period.End, err)
		}
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2024, 2, 28), at(2025, 2, 28))); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("the first year a day early: got %v, want ErrInvalidInput", err)
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 1, 28), at(2026, 2, 28))); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("a month of a yearly plan: got %v, want ErrInvalidInput", err)
	}
}

// A subscription created mid-month has a first period the walk back reaches,
// and its neighbours that never existed are refused.
func TestGenerateInvoiceForAFirstPeriodStartedMidMonth(t *testing.T) {
	ctx := context.Background()
	l, s, _, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CreatedAt = at(2026, 1, 15)
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 3, 15), at(2026, 4, 15)
	})

	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 1, 15), at(2026, 2, 15))); err != nil {
		t.Errorf("the first period: %v", err)
	}
	for name, want := range map[string][2]time.Time{
		"the period before the first":     {at(2025, 12, 15), at(2026, 1, 15)},
		"a calendar month off the anchor": {at(2026, 1, 1), at(2026, 2, 1)},
		"two periods in one":              {at(2026, 1, 15), at(2026, 3, 15)},
	} {
		if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(want[0], want[1])); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Errorf("%s: got %v, want ErrInvalidInput", name, err)
		}
	}
}

// Events recorded for another app never count, even when the subscription has
// no app of its own: QueryUsage drops its app filter then, so the engine
// filters the events itself.
func TestGenerateInvoiceForAPeriodSkipsOtherAppsForAnAppLessSubscription(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	l := ledger.New(s)
	p := activePlanIn(t, l, "metered", "app_1")
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.AppID = ""
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2026, 4, 30), at(2026, 5, 31)
	})
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{
		{ID: id.NewUsageEventID(), TenantID: sub.TenantID, AppID: "", FeatureKey: "api_calls", Quantity: 1500, Timestamp: at(2026, 4, 1)},
		{ID: id.NewUsageEventID(), TenantID: sub.TenantID, AppID: "app_2", FeatureKey: "api_calls", Quantity: 5000, Timestamp: at(2026, 4, 2)},
	}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2026, 3, 31), at(2026, 4, 30)))
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	overage := lineItemsOfType(inv, invoice.LineItemOverage)
	if len(overage) != 1 || overage[0].Quantity != 500 {
		t.Errorf("overage %+v, want 500 over the allowance: the other app's 5000 calls are not this subscription's", overage)
	}
}

// A period that has not started cannot be billed, even when it lines up.
func TestGenerateInvoiceRefusesAPeriodThatHasNotStarted(t *testing.T) {
	ctx := context.Background()
	l, s, _, p := lifecycleFixture(t)
	sub := seedSub(t, s, p, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = at(2099, 2, 28), at(2099, 3, 31)
	})
	if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(at(2099, 1, 31), at(2099, 2, 28))); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("a period that starts in the future: got %v, want ErrInvalidInput", err)
	}
}

// A monthly subscription imported or backdated on the 29th to the 31st can have
// a provider period that starts a few days before its creation day and ends on
// the last day of the next month. The monthly anchor recovers without help, so
// those real periods are accepted and their neighbours are not.
func TestGenerateInvoiceForAMonthlyPeriodAroundAMonthEnd(t *testing.T) {
	ctx := context.Background()
	type span struct{ start, end time.Time }
	for _, c := range []struct {
		name    string
		created time.Time
		current span
		real    span
		refused []span
	}{
		{
			"created 31 Jan, provider period 28 Jan to 28 Feb", at(2026, 1, 31),
			span{at(2026, 2, 28), at(2026, 3, 28)},
			span{at(2026, 1, 28), at(2026, 2, 28)},
			[]span{{at(2025, 12, 28), at(2026, 1, 28)}, {at(2026, 1, 31), at(2026, 2, 28)}, {at(2026, 1, 1), at(2026, 2, 1)}},
		},
		{
			"created 31 Mar, provider period 30 Mar to 30 Apr", at(2026, 3, 31),
			span{at(2026, 4, 30), at(2026, 5, 30)},
			span{at(2026, 3, 30), at(2026, 4, 30)},
			[]span{{at(2026, 2, 28), at(2026, 3, 30)}, {at(2026, 3, 31), at(2026, 4, 30)}, {at(2026, 3, 1), at(2026, 4, 1)}},
		},
	} {
		l, s, _, p := lifecycleFixture(t)
		sub := seedSub(t, s, p, func(x *subscription.Subscription) {
			x.CreatedAt = c.created
			x.CurrentPeriodStart, x.CurrentPeriodEnd = c.current.start, c.current.end
		})
		for _, n := range c.refused {
			if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(n.start, n.end)); !errors.Is(err, ledger.ErrInvalidInput) {
				t.Errorf("%s: neighbour %v to %v: got %v, want ErrInvalidInput", c.name, n.start, n.end, err)
			}
		}
		if _, err := l.GenerateInvoice(ctx, sub.ID, ledger.ForPeriod(c.real.start, c.real.end)); err != nil {
			t.Errorf("%s: the real period: %v", c.name, err)
		}
	}
}

// CreateSubscription starts the first period at created_at itself. A start a
// clock reading later would make the period before the first end after
// created_at, and ForPeriod would accept it.
func TestCreateSubscriptionStartsItsFirstPeriodAtCreation(t *testing.T) {
	ctx := context.Background()
	l, _, _, p := lifecycleFixture(t)
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: p.AppID}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	got := reload(t, l, sub)
	if !got.CurrentPeriodStart.Equal(got.CreatedAt) {
		t.Fatalf("first period starts %v, created %v; want the same instant", got.CurrentPeriodStart, got.CreatedAt)
	}
	before := ledger.ForPeriod(got.CurrentPeriodStart.AddDate(0, -1, 0), got.CurrentPeriodStart)
	if _, err := l.GenerateInvoice(ctx, sub.ID, before); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("the period before the first: got %v, want ErrInvalidInput", err)
	}
}
