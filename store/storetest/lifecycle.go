package storetest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
)

// The lifecycle clock subtests. Each works in its own app and names it in
// every List call: postgres runs the suite against a database it never tears
// down, so an every-app query also sees earlier runs' rows. Where a subtest
// checks the every-app form, it checks only that its own row is there.

var (
	liveStatuses = []subscription.Status{
		subscription.StatusActive, subscription.StatusTrialing, subscription.StatusPastDue, subscription.StatusPaused,
	}
	runningStatuses = []subscription.Status{
		subscription.StatusActive, subscription.StatusTrialing, subscription.StatusPastDue,
	}
)

// lifecycleNow is "now" on a whole second, so a date set exactly on it
// round-trips on every backend and "at or before now" can be pinned.
func lifecycleNow() time.Time { return time.Now().UTC().Truncate(time.Second) }

// expectChanged checks a transition's answer:
// expectChanged(t, "why", true)(s.EndSubscriptionTrial(ctx, subID, now)).
func expectChanged(t *testing.T, what string, want bool) func(bool, error) {
	t.Helper()
	return func(got bool, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got != want {
			t.Errorf("%s: changed = %v, want %v", what, got, want)
		}
	}
}

// storedSubscription writes an active subscription in appID after edit has
// shaped it.
func storedSubscription(t *testing.T, s ledgerstore.Store, appID string, edit func(*subscription.Subscription)) *subscription.Subscription {
	t.Helper()
	sub := newTestSubscription("tenant-"+uniqueSuffix(), appID)
	edit(sub)
	if err := s.CreateSubscription(context.Background(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	return sub
}

func storedInvoice(t *testing.T, s ledgerstore.Store, appID string, status invoice.Status, due *time.Time) *invoice.Invoice {
	t.Helper()
	inv := newTestInvoice("tenant-"+uniqueSuffix(), appID)
	inv.Status = status
	inv.DueDate = due
	if err := s.CreateInvoice(context.Background(), inv); err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	return inv
}

func reread(t *testing.T, s ledgerstore.Store, sub *subscription.Subscription) *subscription.Subscription {
	t.Helper()
	got, err := s.GetSubscription(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	return got
}

func idListed(ids []string, want string) bool {
	for _, got := range ids {
		if got == want {
			return true
		}
	}
	return false
}

func testListDueSubscriptions(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	now := lifecycleNow()
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	earliest := storedSubscription(t, s, appID, func(x *subscription.Subscription) { x.CancelAt = at(-2 * time.Hour) })
	paused := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
		x.Status = subscription.StatusPaused
		x.CancelAt = at(-time.Hour)
	})
	onTheDot := storedSubscription(t, s, appID, func(x *subscription.Subscription) { x.CancelAt = at(0) })
	storedSubscription(t, s, appID, func(x *subscription.Subscription) { x.CancelAt = at(time.Hour) })
	storedSubscription(t, s, appID, func(x *subscription.Subscription) {
		x.Status = subscription.StatusCanceled
		x.CancelAt = at(-time.Hour)
	})
	trial := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
		x.Status = subscription.StatusTrialing
		x.TrialEnd = at(-time.Hour)
	})
	storedSubscription(t, s, appID, func(x *subscription.Subscription) {
		x.Status = subscription.StatusTrialing
		x.TrialEnd = at(time.Hour)
	})
	ended := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = now.AddDate(0, -1, 0), now.Add(-time.Hour)
	})
	storedSubscription(t, s, appID, func(x *subscription.Subscription) {
		x.Status = subscription.StatusPaused
		x.CurrentPeriodStart, x.CurrentPeriodEnd = now.AddDate(0, -1, 0), now.Add(-time.Hour)
	})
	elsewhere := storedSubscription(t, s, "app-"+uniqueSuffix(), func(x *subscription.Subscription) { x.CancelAt = at(-time.Hour) })

	list := func(opts subscription.DueOpts) []string {
		t.Helper()
		got, err := s.ListDueSubscriptions(ctx, opts)
		if err != nil {
			t.Fatalf("ListDueSubscriptions(%+v): %v", opts, err)
		}
		return subscriptionIDs(got)
	}
	cases := []struct {
		name string
		opts subscription.DueOpts
		want []*subscription.Subscription
	}{
		{
			"cancels, earliest first, the one due exactly now included, the canceled one left out",
			subscription.DueOpts{Field: subscription.DueCancel, Before: now, Statuses: liveStatuses, AppID: appID},
			[]*subscription.Subscription{earliest, paused, onTheDot},
		},
		{
			"trials",
			subscription.DueOpts{Field: subscription.DueTrialEnd, Before: now, Statuses: []subscription.Status{subscription.StatusTrialing}, AppID: appID},
			[]*subscription.Subscription{trial},
		},
		{
			"periods of running subscriptions only",
			subscription.DueOpts{Field: subscription.DuePeriodEnd, Before: now, Statuses: runningStatuses, AppID: appID},
			[]*subscription.Subscription{ended},
		},
		{
			"a page",
			subscription.DueOpts{Field: subscription.DueCancel, Before: now, Statuses: liveStatuses, AppID: appID, Limit: 1, Offset: 1},
			[]*subscription.Subscription{paused},
		},
	}
	for _, c := range cases {
		if got, want := list(c.opts), subscriptionIDs(c.want); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", c.name, got, want)
		}
	}

	every := list(subscription.DueOpts{Field: subscription.DueCancel, Before: now, Statuses: liveStatuses})
	if !idListed(every, elsewhere.ID.String()) || !idListed(every, earliest.ID.String()) {
		t.Errorf("with no app, due cancels in both apps must be listed; got %d rows without them", len(every))
	}
	if _, err := s.ListDueSubscriptions(ctx, subscription.DueOpts{Field: "created_at", Before: now}); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("an unknown due field: got %v, want ErrInvalidInput", err)
	}

	// A cancel half a second after a whole second, kept at that precision.
	// SQLite compares these timestamps as text, so this pins that a fraction
	// sorts after the whole second it follows and before the next one.
	fractionApp := "app-" + uniqueSuffix()
	half := now.Add(500 * time.Millisecond)
	fraction := storedSubscription(t, s, fractionApp, func(x *subscription.Subscription) { x.CancelAt = &half })
	if got := list(subscription.DueOpts{Field: subscription.DueCancel, Before: now, Statuses: liveStatuses, AppID: fractionApp}); len(got) != 0 {
		t.Errorf("a cancel at %v listed as due at %v: %v", half, now, got)
	}
	if got, want := list(subscription.DueOpts{Field: subscription.DueCancel, Before: now.Add(time.Second), Statuses: liveStatuses, AppID: fractionApp}), subscriptionIDs([]*subscription.Subscription{fraction}); !reflect.DeepEqual(got, want) {
		t.Errorf("a cancel at %v at %v: got %v, want %v", half, now.Add(time.Second), got, want)
	}
}

func testListOverdueInvoices(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	now := lifecycleNow()
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	older := storedInvoice(t, s, appID, invoice.StatusPending, at(-2*time.Hour))
	newer := storedInvoice(t, s, appID, invoice.StatusPending, at(-time.Hour))
	storedInvoice(t, s, appID, invoice.StatusPending, at(0)) // due now, not late yet
	storedInvoice(t, s, appID, invoice.StatusPending, at(time.Hour))
	storedInvoice(t, s, appID, invoice.StatusPending, nil)
	storedInvoice(t, s, appID, invoice.StatusPaid, at(-time.Hour))
	storedInvoice(t, s, appID, invoice.StatusPastDue, at(-time.Hour))
	elsewhere := storedInvoice(t, s, "app-"+uniqueSuffix(), invoice.StatusPending, at(-time.Hour))

	list := func(opts invoice.OverdueOpts) []string {
		t.Helper()
		got, err := s.ListOverdueInvoices(ctx, opts)
		if err != nil {
			t.Fatalf("ListOverdueInvoices(%+v): %v", opts, err)
		}
		return invoiceIDs(got)
	}
	if got, want := list(invoice.OverdueOpts{Before: now, AppID: appID}), invoiceIDs([]*invoice.Invoice{older, newer}); !reflect.DeepEqual(got, want) {
		t.Errorf("overdue: got %v, want %v", got, want)
	}
	if got, want := list(invoice.OverdueOpts{Before: now, AppID: appID, Limit: 1, Offset: 1}), invoiceIDs([]*invoice.Invoice{newer}); !reflect.DeepEqual(got, want) {
		t.Errorf("a page: got %v, want %v", got, want)
	}
	if every := list(invoice.OverdueOpts{Before: now}); !idListed(every, elsewhere.ID.String()) || !idListed(every, older.ID.String()) {
		t.Errorf("with no app, overdue invoices in both apps must be listed")
	}
}

func testLifecycleTransitionsAreConditional(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	now := lifecycleNow()
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	t.Run("trial", func(t *testing.T) {
		due := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			backdate(&x.Entity)
			x.Status = subscription.StatusTrialing
			x.TrialEnd = at(-time.Hour)
		})
		before := time.Now()
		expectChanged(t, "a trial that has ended", true)(s.EndSubscriptionTrial(ctx, due.ID, now))
		after := time.Now()
		got := reread(t, s, due)
		if got.Status != subscription.StatusActive {
			t.Errorf("status %q, want active", got.Status)
		}
		requireStampedDuring(t, "updated_at", got.UpdatedAt, before, after)
		expectChanged(t, "the same trial again", false)(s.EndSubscriptionTrial(ctx, due.ID, now))

		early := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			x.Status = subscription.StatusTrialing
			x.TrialEnd = at(time.Hour)
		})
		expectChanged(t, "a trial that has not ended", false)(s.EndSubscriptionTrial(ctx, early.ID, now))
		paused := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			x.Status = subscription.StatusPaused
			x.TrialEnd = at(-time.Hour)
		})
		expectChanged(t, "a paused subscription", false)(s.EndSubscriptionTrial(ctx, paused.ID, now))
		if got := reread(t, s, paused); got.Status != subscription.StatusPaused {
			t.Errorf("a paused subscription became %q", got.Status)
		}
		expectChanged(t, "an unknown subscription", false)(s.EndSubscriptionTrial(ctx, id.NewSubscriptionID(), now))
	})

	t.Run("cancel", func(t *testing.T) {
		due := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			backdate(&x.Entity)
			x.CancelAt = at(-time.Hour)
		})
		before := time.Now()
		expectChanged(t, "a cancel that is due", true)(s.EnactSubscriptionCancel(ctx, due.ID, now))
		after := time.Now()
		got := reread(t, s, due)
		if got.Status != subscription.StatusCanceled {
			t.Errorf("status %q, want canceled", got.Status)
		}
		requireStampedDuring(t, "updated_at", got.UpdatedAt, before, after)
		if got.CanceledAt == nil || !sameInstantToMillisecond(*got.CanceledAt, *due.CancelAt) {
			t.Errorf("canceled_at %v, want cancel_at %v", got.CanceledAt, *due.CancelAt)
		}
		expectChanged(t, "the same cancel again", false)(s.EnactSubscriptionCancel(ctx, due.ID, now))

		paused := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			x.Status = subscription.StatusPaused
			x.CancelAt = at(-time.Hour)
		})
		expectChanged(t, "a paused subscription whose cancel is due", true)(s.EnactSubscriptionCancel(ctx, paused.ID, now))
		expired := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			x.Status = subscription.StatusExpired
			x.CancelAt = at(-time.Hour)
		})
		expectChanged(t, "an expired subscription", false)(s.EnactSubscriptionCancel(ctx, expired.ID, now))
		later := storedSubscription(t, s, appID, func(x *subscription.Subscription) { x.CancelAt = at(time.Hour) })
		expectChanged(t, "a cancel dated later", false)(s.EnactSubscriptionCancel(ctx, later.ID, now))
		none := storedSubscription(t, s, appID, func(*subscription.Subscription) {})
		expectChanged(t, "no cancel at all", false)(s.EnactSubscriptionCancel(ctx, none.ID, now))
		fraction := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			c := now.Add(500 * time.Millisecond)
			x.CancelAt = &c
		})
		expectChanged(t, "a cancel half a second after now", false)(s.EnactSubscriptionCancel(ctx, fraction.ID, now))
		expectChanged(t, "the same cancel a second later", true)(s.EnactSubscriptionCancel(ctx, fraction.ID, now.Add(time.Second)))
		if got := reread(t, s, none); got.Status != subscription.StatusActive || got.CanceledAt != nil {
			t.Errorf("a subscription with no cancel became %q with canceled_at %v", got.Status, got.CanceledAt)
		}
	})

	t.Run("period", func(t *testing.T) {
		past := now.Add(-time.Hour)
		next := past.AddDate(0, 1, 0)
		ended := func(edit func(*subscription.Subscription)) *subscription.Subscription {
			return storedSubscription(t, s, appID, func(x *subscription.Subscription) {
				x.CurrentPeriodStart, x.CurrentPeriodEnd = past.AddDate(0, -1, 0), past
				edit(x)
			})
		}

		due := ended(func(x *subscription.Subscription) {
			backdate(&x.Entity)
			x.Quantity = map[string]int64{"seats": 3}
			x.Metadata = map[string]string{"note": "kept"}
		})
		before := time.Now()
		expectChanged(t, "an ended period", true)(s.AdvanceSubscriptionPeriod(ctx, due.ID, past, next, now))
		after := time.Now()
		got := reread(t, s, due)
		requireStampedDuring(t, "updated_at", got.UpdatedAt, before, after)
		if !sameInstantToMillisecond(got.CurrentPeriodStart, past) || !sameInstantToMillisecond(got.CurrentPeriodEnd, next) {
			t.Errorf("period %v to %v, want %v to %v", got.CurrentPeriodStart, got.CurrentPeriodEnd, past, next)
		}
		// An operator's plan, seat or metadata change must survive the clock.
		if got.PlanID.String() != due.PlanID.String() || got.Quantity["seats"] != 3 || got.Metadata["note"] != "kept" || got.Status != subscription.StatusActive {
			t.Errorf("the advance wrote more than the period: %+v", got)
		}
		expectChanged(t, "the same advance again", false)(s.AdvanceSubscriptionPeriod(ctx, due.ID, past, next, now))

		stale := ended(func(*subscription.Subscription) {})
		expectChanged(t, "an end no later than the current one", false)(s.AdvanceSubscriptionPeriod(ctx, stale.ID, past.AddDate(0, -1, 0), past, now))
		paused := ended(func(x *subscription.Subscription) { x.Status = subscription.StatusPaused })
		expectChanged(t, "a paused subscription", false)(s.AdvanceSubscriptionPeriod(ctx, paused.ID, past, next, now))
		cancelAtEnd := ended(func(x *subscription.Subscription) { c := past; x.CancelAt = &c })
		expectChanged(t, "a cancel due at the period end", false)(s.AdvanceSubscriptionPeriod(ctx, cancelAtEnd.ID, past, next, now))
		cancelLater := ended(func(x *subscription.Subscription) { c := next; x.CancelAt = &c })
		expectChanged(t, "a cancel in a later period", true)(s.AdvanceSubscriptionPeriod(ctx, cancelLater.ID, past, next, now))
		running := storedSubscription(t, s, appID, func(*subscription.Subscription) {})
		expectChanged(t, "a period that has not ended", false)(s.AdvanceSubscriptionPeriod(ctx, running.ID, running.CurrentPeriodEnd, running.CurrentPeriodEnd.AddDate(0, 1, 0), now))
	})

	t.Run("invoice", func(t *testing.T) {
		inv := newTestInvoice("tenant-"+uniqueSuffix(), appID)
		backdate(&inv.Entity)
		inv.Status = invoice.StatusPending
		inv.DueDate = at(-time.Hour)
		if err := s.CreateInvoice(ctx, inv); err != nil {
			t.Fatalf("CreateInvoice: %v", err)
		}
		before := time.Now()
		expectChanged(t, "an overdue invoice", true)(s.MarkInvoicePastDue(ctx, inv.ID, now))
		after := time.Now()
		got, err := s.GetInvoice(ctx, inv.ID)
		if err != nil {
			t.Fatalf("GetInvoice: %v", err)
		}
		if got.Status != invoice.StatusPastDue {
			t.Errorf("status %q, want past_due", got.Status)
		}
		requireStampedDuring(t, "updated_at", got.UpdatedAt, before, after)
		expectChanged(t, "the same invoice again", false)(s.MarkInvoicePastDue(ctx, inv.ID, now))

		for _, c := range []struct {
			what   string
			status invoice.Status
			due    *time.Time
		}{
			{"a pending invoice due exactly now", invoice.StatusPending, at(0)},
			{"a pending invoice due later", invoice.StatusPending, at(time.Hour)},
			{"a paid invoice", invoice.StatusPaid, at(-time.Hour)},
			{"a draft with no due date", invoice.StatusDraft, nil},
		} {
			other := storedInvoice(t, s, appID, c.status, c.due)
			expectChanged(t, c.what, false)(s.MarkInvoicePastDue(ctx, other.ID, now))
		}
		expectChanged(t, "an unknown invoice", false)(s.MarkInvoicePastDue(ctx, id.NewInvoiceID(), now))
	})
}

// testLifecycleTransitionsApplyOnce races eight callers on one row per
// transition, as replicas running the clock at once would: a due cancel, an
// ended trial, an ended period and an overdue invoice. Exactly one caller may
// see each change.
func testLifecycleTransitionsApplyOnce(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	now := lifecycleNow()
	past := now.Add(-time.Hour)
	next := past.AddDate(0, 1, 0)
	sub := storedSubscription(t, s, appID, func(x *subscription.Subscription) { c := past; x.CancelAt = &c })
	trial := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
		x.Status = subscription.StatusTrialing
		x.TrialEnd = &past
	})
	ended := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = past.AddDate(0, -1, 0), past
	})
	inv := storedInvoice(t, s, appID, invoice.StatusPending, &past)

	const racers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		cancels  int
		trials   int
		advances int
		pastDues int
		errs     []error
	)
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			canceled, cancelErr := s.EnactSubscriptionCancel(ctx, sub.ID, now)
			trialEnded, trialErr := s.EndSubscriptionTrial(ctx, trial.ID, now)
			advanced, advanceErr := s.AdvanceSubscriptionPeriod(ctx, ended.ID, past, next, now)
			marked, markErr := s.MarkInvoicePastDue(ctx, inv.ID, now)
			mu.Lock()
			defer mu.Unlock()
			for _, c := range []struct {
				changed bool
				count   *int
			}{{canceled, &cancels}, {trialEnded, &trials}, {advanced, &advances}, {marked, &pastDues}} {
				if c.changed {
					*c.count++
				}
			}
			errs = append(errs, cancelErr, trialErr, advanceErr, markErr)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("racing transitions: %v", err)
	}
	if cancels != 1 || trials != 1 || advances != 1 || pastDues != 1 {
		t.Errorf("%d racers saw %d cancels, %d trial ends, %d period advances and %d invoices go past due, want 1 each",
			racers, cancels, trials, advances, pastDues)
	}
}

// testLifecycleAdvanceKeepsARacingCancel races a scheduled cancel against the
// period advance on fifty ended subscriptions. The two write different
// columns, so every row must end with both: the new period and the cancel_at.
// A write that put back a whole row it had read would lose one of them.
func testLifecycleAdvanceKeepsARacingCancel(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	now := lifecycleNow()
	past := now.Add(-time.Hour)
	next := past.AddDate(0, 1, 0)
	cancelAt := next.Add(time.Hour)

	const rows = 50
	subs := make([]*subscription.Subscription, rows)
	for i := range subs {
		subs[i] = storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			x.CurrentPeriodStart, x.CurrentPeriodEnd = past.AddDate(0, -1, 0), past
		})
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		miss []string
	)
	record := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}
	for _, sub := range subs {
		wg.Add(2)
		go func() {
			defer wg.Done()
			record(s.CancelSubscription(ctx, sub.ID, cancelAt))
		}()
		go func() {
			defer wg.Done()
			advanced, err := s.AdvanceSubscriptionPeriod(ctx, sub.ID, past, next, now)
			record(err)
			if !advanced {
				mu.Lock()
				defer mu.Unlock()
				miss = append(miss, sub.ID.String())
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("racing writes: %v", err)
	}
	if len(miss) > 0 {
		t.Errorf("%d advances did not match, though a cancel after the new end never blocks one: %v", len(miss), miss)
	}
	for _, sub := range subs {
		got := reread(t, s, sub)
		if !sameInstantToMillisecond(got.CurrentPeriodStart, past) || !sameInstantToMillisecond(got.CurrentPeriodEnd, next) {
			t.Errorf("%s: period %v to %v, want %v to %v", sub.ID, got.CurrentPeriodStart, got.CurrentPeriodEnd, past, next)
		}
		if got.CancelAt == nil || !sameInstantToMillisecond(*got.CancelAt, cancelAt) {
			t.Errorf("%s: cancel_at %v, want %v", sub.ID, got.CancelAt, cancelAt)
		}
		if got.Status != subscription.StatusActive {
			t.Errorf("%s: status %q, want active", sub.ID, got.Status)
		}
	}
}
