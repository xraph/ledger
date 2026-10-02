package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	ledgerstore "github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
)

// The operator writes that can race the lifecycle clock: pause, resume, a
// plan change, a cancel and a provider sync. Each must write only its own
// columns under its own precondition, so the clock's write and the
// operator's both survive, and a subscription the clock canceled stays
// canceled.

// operatorRaceRows is how many rows each race runs on. One row gives one
// interleaving; many give the scheduler room to try both orders.
const operatorRaceRows = 25

// raceEach runs a and b at once for every row and waits for all of them.
func raceEach[T any](rows []T, a, b func(T)) {
	var wg sync.WaitGroup
	for _, row := range rows {
		wg.Add(2)
		go func() {
			defer wg.Done()
			a(row)
		}()
		go func() {
			defer wg.Done()
			b(row)
		}()
	}
	wg.Wait()
}

// errorLog collects errors from racing goroutines.
type errorLog struct {
	mu   sync.Mutex
	errs []error
}

func (l *errorLog) add(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

func (l *errorLog) check(t *testing.T) {
	t.Helper()
	if err := errors.Join(l.errs...); err != nil {
		t.Fatalf("racing writes: %v", err)
	}
}

func testOperatorWritesAreConditional(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	now := lifecycleNow()
	withStatus := func(st subscription.Status) *subscription.Subscription {
		return storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			backdate(&x.Entity)
			x.Status = st
		})
	}

	t.Run("pause", func(t *testing.T) {
		pausedAt := now.Add(-90 * time.Minute).Add(123456789 * time.Nanosecond)
		for _, st := range []subscription.Status{subscription.StatusActive, subscription.StatusTrialing} {
			sub := withStatus(st)
			before := time.Now()
			expectChanged(t, "pause a "+string(st)+" subscription", true)(s.PauseSubscription(ctx, sub.ID, pausedAt))
			after := time.Now()
			got := reread(t, s, sub)
			if got.Status != subscription.StatusPaused {
				t.Errorf("a %s subscription paused to %q", st, got.Status)
			}
			if got.PausedAt == nil || !sameInstantToMillisecond(*got.PausedAt, pausedAt) {
				t.Errorf("a %s subscription paused with paused_at %v, want %v", st, got.PausedAt, pausedAt)
			}
			if !sameInstantToMillisecond(got.CurrentPeriodEnd, sub.CurrentPeriodEnd) {
				t.Errorf("a pause moved the period end to %v", got.CurrentPeriodEnd)
			}
			requireStampedDuring(t, "updated_at", got.UpdatedAt, before, after)
		}
		for _, st := range []subscription.Status{subscription.StatusPaused, subscription.StatusPastDue, subscription.StatusCanceled, subscription.StatusExpired} {
			sub := withStatus(st)
			expectChanged(t, "pause a "+string(st)+" subscription", false)(s.PauseSubscription(ctx, sub.ID, pausedAt))
			got := reread(t, s, sub)
			if got.Status != st || got.PausedAt != nil {
				t.Errorf("a refused pause moved a %s subscription to %q, paused_at %v", st, got.Status, got.PausedAt)
			}
		}
		expectChanged(t, "pause an unknown subscription", false)(s.PauseSubscription(ctx, id.NewSubscriptionID(), pausedAt))
	})

	t.Run("resume", func(t *testing.T) {
		// Sub-second values, read back from the store, must match the
		// paused_at the store holds on every backend, sqlite's text included.
		pausedAt := now.Add(-90 * 24 * time.Hour).Add(500 * time.Millisecond)
		resumedAt := now.Add(123456789 * time.Nanosecond)
		trialEnd := now.Add(10 * 24 * time.Hour)
		resume := func(read *subscription.Subscription, status subscription.Status, trial *time.Time) subscription.Resume {
			return subscription.Resume{
				PausedAt: read.PausedAt, At: resumedAt, Status: status,
				PeriodStart: resumedAt, PeriodEnd: resumedAt.AddDate(0, 1, 0), TrialEnd: trial,
			}
		}
		paused := func(edit func(*subscription.Subscription)) *subscription.Subscription {
			return storedSubscription(t, s, appID, func(x *subscription.Subscription) {
				backdate(&x.Entity)
				x.Status = subscription.StatusPaused
				p := pausedAt
				x.PausedAt = &p
				edit(x)
			})
		}

		sub := paused(func(x *subscription.Subscription) {
			x.CurrentPeriodStart, x.CurrentPeriodEnd = pausedAt.AddDate(0, -1, 0), pausedAt.AddDate(0, 0, 3)
			oldTrial := pausedAt.Add(time.Hour)
			x.TrialEnd = &oldTrial
		})
		read := reread(t, s, sub)
		before := time.Now()
		expectChanged(t, "resume a paused subscription", true)(s.ResumeSubscription(ctx, sub.ID, resume(read, subscription.StatusTrialing, &trialEnd)))
		after := time.Now()
		got := reread(t, s, sub)
		if got.Status != subscription.StatusTrialing {
			t.Errorf("a resumed subscription is %q, want the status it was given", got.Status)
		}
		if !sameInstantToMillisecond(got.CurrentPeriodStart, resumedAt) || !sameInstantToMillisecond(got.CurrentPeriodEnd, resumedAt.AddDate(0, 1, 0)) {
			t.Errorf("a resumed period runs %v to %v, want it restarted at %v", got.CurrentPeriodStart, got.CurrentPeriodEnd, resumedAt)
		}
		if got.TrialEnd == nil || !sameInstantToMillisecond(*got.TrialEnd, trialEnd) {
			t.Errorf("trial_end %v, want the moved %v", got.TrialEnd, trialEnd)
		}
		if got.PausedAt != nil {
			t.Errorf("paused_at %v survived the resume", got.PausedAt)
		}
		if got.ResumedAt == nil || !sameInstantToMillisecond(*got.ResumedAt, resumedAt) {
			t.Errorf("resumed_at %v, want %v", got.ResumedAt, resumedAt)
		}
		requireStampedDuring(t, "updated_at", got.UpdatedAt, before, after)

		// A nil TrialEnd leaves the column alone.
		keepTrial := paused(func(x *subscription.Subscription) {
			x.TrialEnd = &trialEnd
		})
		expectChanged(t, "resume keeping the trial end", true)(s.ResumeSubscription(ctx, keepTrial.ID, resume(reread(t, s, keepTrial), subscription.StatusActive, nil)))
		if got := reread(t, s, keepTrial); got.Status != subscription.StatusActive || got.TrialEnd == nil || !sameInstantToMillisecond(*got.TrialEnd, trialEnd) {
			t.Errorf("status %q trial_end %v, want active and the trial end untouched", got.Status, got.TrialEnd)
		}

		// A row paused before paused_at existed resumes when the caller
		// read no paused_at either.
		legacy := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			backdate(&x.Entity)
			x.Status = subscription.StatusPaused
		})
		expectChanged(t, "resume a paused subscription with no paused_at", true)(s.ResumeSubscription(ctx, legacy.ID, resume(reread(t, s, legacy), subscription.StatusActive, nil)))

		// A different paused_at means the row was resumed and paused again
		// since the read: the write must miss.
		again := paused(func(*subscription.Subscription) {})
		stale := resume(reread(t, s, again), subscription.StatusActive, nil)
		otherPause := pausedAt.Add(-time.Hour)
		stale.PausedAt = &otherPause
		expectChanged(t, "resume with a stale paused_at", false)(s.ResumeSubscription(ctx, again.ID, stale))
		stale.PausedAt = nil
		expectChanged(t, "resume reading no paused_at where one is set", false)(s.ResumeSubscription(ctx, again.ID, stale))
		if got := reread(t, s, again); got.Status != subscription.StatusPaused || got.PausedAt == nil {
			t.Errorf("a missed resume left status %q paused_at %v", got.Status, got.PausedAt)
		}

		for _, st := range []subscription.Status{subscription.StatusActive, subscription.StatusTrialing, subscription.StatusCanceled, subscription.StatusExpired} {
			other := withStatus(st)
			expectChanged(t, "resume a "+string(st)+" subscription", false)(s.ResumeSubscription(ctx, other.ID, resume(reread(t, s, other), subscription.StatusActive, nil)))
			if got := reread(t, s, other); got.Status != st || got.ResumedAt != nil {
				t.Errorf("a refused resume moved a %s subscription to %q, resumed_at %v", st, got.Status, got.ResumedAt)
			}
		}
		expectChanged(t, "resume an unknown subscription", false)(s.ResumeSubscription(ctx, id.NewSubscriptionID(), subscription.Resume{At: resumedAt, Status: subscription.StatusActive}))
	})

	t.Run("plan", func(t *testing.T) {
		cancelAt := now.Add(24 * time.Hour)
		sub := storedSubscription(t, s, appID, func(x *subscription.Subscription) {
			backdate(&x.Entity)
			x.Status = subscription.StatusPaused
			x.CancelAt = &cancelAt
			x.Quantity = map[string]int64{"seats": 2}
			x.Metadata = map[string]string{"note": "kept"}
		})
		planID := id.NewPlanID()
		before := time.Now()
		expectChanged(t, "change a paused subscription's plan", true)(s.ChangeSubscriptionPlan(ctx, sub.ID, planID, map[string]int64{"seats": 5}))
		after := time.Now()
		got := reread(t, s, sub)
		if got.PlanID.String() != planID.String() || got.Quantity["seats"] != 5 {
			t.Errorf("plan %s seats %d, want %s and 5", got.PlanID, got.Quantity["seats"], planID)
		}
		if got.Status != subscription.StatusPaused || got.CancelAt == nil || !sameInstantToMillisecond(*got.CancelAt, cancelAt) ||
			!sameInstantToMillisecond(got.CurrentPeriodEnd, sub.CurrentPeriodEnd) || got.Metadata["note"] != "kept" {
			t.Errorf("a plan change wrote more than the plan and seats: %+v", got)
		}
		requireStampedDuring(t, "updated_at", got.UpdatedAt, before, after)

		expectChanged(t, "a nil quantity", true)(s.ChangeSubscriptionPlan(ctx, sub.ID, planID, nil))
		if got := reread(t, s, sub); len(got.Quantity) != 0 {
			t.Errorf("a nil quantity stored %v, want none", got.Quantity)
		}
		for _, st := range []subscription.Status{subscription.StatusCanceled, subscription.StatusExpired} {
			other := withStatus(st)
			expectChanged(t, "change a "+string(st)+" subscription's plan", false)(s.ChangeSubscriptionPlan(ctx, other.ID, planID, nil))
			if got := reread(t, s, other); got.PlanID.String() != other.PlanID.String() {
				t.Errorf("a refused plan change moved a %s subscription to plan %s", st, got.PlanID)
			}
		}
		expectChanged(t, "change an unknown subscription's plan", false)(s.ChangeSubscriptionPlan(ctx, id.NewSubscriptionID(), planID, nil))
	})

	t.Run("cancel", func(t *testing.T) {
		for st, want := range map[subscription.Status]error{
			subscription.StatusCanceled: ledger.ErrSubscriptionCanceled,
			subscription.StatusExpired:  ledger.ErrSubscriptionExpired,
		} {
			for _, immediately := range []bool{false, true} {
				sub := withStatus(st)
				if _, err := s.CancelSubscription(ctx, sub.ID, immediately); !errors.Is(err, want) {
					t.Errorf("cancel a %s subscription (immediately=%v): got %v, want %v", st, immediately, err, want)
				}
				if got := reread(t, s, sub); got.CancelAt != nil {
					t.Errorf("a refused cancel set cancel_at on a %s subscription to %v", st, got.CancelAt)
				}
			}
		}
		for _, immediately := range []bool{false, true} {
			if _, err := s.CancelSubscription(ctx, id.NewSubscriptionID(), immediately); !errors.Is(err, ledger.ErrSubscriptionNotFound) {
				t.Errorf("cancel an unknown subscription (immediately=%v): got %v, want ErrSubscriptionNotFound", immediately, err)
			}
		}
	})

	t.Run("provider", func(t *testing.T) {
		sub := withStatus(subscription.StatusActive)
		if err := s.SetSubscriptionProvider(ctx, sub.ID, "sub_123", "stripe"); err != nil {
			t.Fatalf("SetSubscriptionProvider: %v", err)
		}
		if got := reread(t, s, sub); got.ProviderID != "sub_123" || got.ProviderName != "stripe" || got.Status != subscription.StatusActive {
			t.Errorf("subscription provider %q/%q status %q", got.ProviderID, got.ProviderName, got.Status)
		}
		if err := s.SetSubscriptionProvider(ctx, id.NewSubscriptionID(), "x", "y"); !errors.Is(err, ledger.ErrSubscriptionNotFound) {
			t.Errorf("an unknown subscription: got %v, want ErrSubscriptionNotFound", err)
		}

		due := now.Add(-time.Hour)
		inv := storedInvoice(t, s, appID, invoice.StatusPending, &due)
		if err := s.SetInvoiceProvider(ctx, inv.ID, "in_123", "stripe"); err != nil {
			t.Fatalf("SetInvoiceProvider: %v", err)
		}
		got, err := s.GetInvoice(ctx, inv.ID)
		if err != nil {
			t.Fatalf("GetInvoice: %v", err)
		}
		if got.ProviderID != "in_123" || got.ProviderName != "stripe" || got.Status != invoice.StatusPending {
			t.Errorf("invoice provider %q/%q status %q", got.ProviderID, got.ProviderName, got.Status)
		}
		if err := s.SetInvoiceProvider(ctx, id.NewInvoiceID(), "x", "y"); !errors.Is(err, ledger.ErrInvoiceNotFound) {
			t.Errorf("an unknown invoice: got %v, want ErrInvoiceNotFound", err)
		}
	})
}

// testOperatorWritesRaceTheClock races each operator write against the clock
// transition it can meet, on many rows, and checks that neither write is lost
// and that nothing the clock canceled comes back.
func testOperatorWritesRaceTheClock(t *testing.T, s ledgerstore.Store) {
	ctx := context.Background()
	appID := "app-" + uniqueSuffix()
	now := lifecycleNow()
	past := now.Add(-time.Hour)
	next := past.AddDate(0, 1, 0)
	many := func(edit func(*subscription.Subscription)) []*subscription.Subscription {
		out := make([]*subscription.Subscription, operatorRaceRows)
		for i := range out {
			out[i] = storedSubscription(t, s, appID, edit)
		}
		return out
	}
	endedPeriod := func(x *subscription.Subscription) {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = past.AddDate(0, -1, 0), past
	}
	requireNewPeriod := func(t *testing.T, got *subscription.Subscription) {
		t.Helper()
		if !sameInstantToMillisecond(got.CurrentPeriodStart, past) || !sameInstantToMillisecond(got.CurrentPeriodEnd, next) {
			t.Errorf("%s: period %v to %v, want the advanced %v to %v", got.ID, got.CurrentPeriodStart, got.CurrentPeriodEnd, past, next)
		}
	}

	t.Run("resume against cancel enactment", func(t *testing.T) {
		subs := many(func(x *subscription.Subscription) {
			x.Status = subscription.StatusPaused
			c := past
			x.CancelAt = &c
		})
		var log errorLog
		enacted := sync.Map{}
		raceEach(subs,
			func(sub *subscription.Subscription) {
				_, err := s.ResumeSubscription(ctx, sub.ID, subscription.Resume{
					At: now, Status: subscription.StatusActive, PeriodStart: now, PeriodEnd: now.AddDate(0, 1, 0),
				})
				log.add(err)
			},
			func(sub *subscription.Subscription) {
				ok, err := s.EnactSubscriptionCancel(ctx, sub.ID, now)
				log.add(err)
				enacted.Store(sub.ID.String(), ok)
			})
		log.check(t)
		for _, sub := range subs {
			got := reread(t, s, sub)
			if got.Status != subscription.StatusCanceled {
				t.Errorf("%s: a resume racing the cancel left it %q, want canceled", sub.ID, got.Status)
			}
			if got.CanceledAt == nil || !sameInstantToMillisecond(*got.CanceledAt, past) {
				t.Errorf("%s: canceled_at %v, want the cancel_at %v", sub.ID, got.CanceledAt, past)
			}
			// Paused or resumed, the cancel was due, so the clock enacted it.
			if ok, _ := enacted.Load(sub.ID.String()); ok != true {
				t.Errorf("%s: the enactment did not match", sub.ID)
			}
		}
	})

	t.Run("resume against period advance and trial end", func(t *testing.T) {
		// The clock never writes a paused row's period or trial, and a resume
		// moves both past now, so neither clock write can match either side
		// of the resume, and the resume always lands.
		trialEnd := past
		subs := many(func(x *subscription.Subscription) {
			endedPeriod(x)
			x.Status = subscription.StatusPaused
			p := past.AddDate(0, 0, -3)
			x.PausedAt = &p
			x.TrialEnd = &trialEnd
		})
		resumedTrial := now.AddDate(0, 0, 3)
		var log errorLog
		clockMatched := sync.Map{}
		raceEach(subs,
			func(sub *subscription.Subscription) {
				ok, err := s.ResumeSubscription(ctx, sub.ID, subscription.Resume{
					PausedAt: reread(t, s, sub).PausedAt, At: now, Status: subscription.StatusTrialing,
					PeriodStart: now, PeriodEnd: now.AddDate(0, 1, 0), TrialEnd: &resumedTrial,
				})
				if err == nil && !ok {
					err = errors.New("the resume did not match " + sub.ID.String())
				}
				log.add(err)
			},
			func(sub *subscription.Subscription) {
				advanced, err := s.AdvanceSubscriptionPeriod(ctx, sub.ID, past, past, next, now)
				log.add(err)
				ended, err := s.EndSubscriptionTrial(ctx, sub.ID, now)
				log.add(err)
				clockMatched.Store(sub.ID.String(), advanced || ended)
			})
		log.check(t)
		for _, sub := range subs {
			got := reread(t, s, sub)
			if ok, _ := clockMatched.Load(sub.ID.String()); ok == true {
				t.Errorf("%s: a clock write matched a paused or just-resumed row", sub.ID)
			}
			if got.Status != subscription.StatusTrialing || !sameInstantToMillisecond(got.CurrentPeriodStart, now) ||
				got.TrialEnd == nil || !sameInstantToMillisecond(*got.TrialEnd, resumedTrial) {
				t.Errorf("%s: status %q period from %v trial_end %v, want the resume's", sub.ID, got.Status, got.CurrentPeriodStart, got.TrialEnd)
			}
		}
	})

	t.Run("plan change against period advance", func(t *testing.T) {
		subs := many(endedPeriod)
		planID := id.NewPlanID()
		var log errorLog
		raceEach(subs,
			func(sub *subscription.Subscription) {
				ok, err := s.ChangeSubscriptionPlan(ctx, sub.ID, planID, map[string]int64{"seats": 7})
				if err == nil && !ok {
					err = errors.New("the plan change did not match " + sub.ID.String())
				}
				log.add(err)
			},
			func(sub *subscription.Subscription) {
				ok, err := s.AdvanceSubscriptionPeriod(ctx, sub.ID, past, past, next, now)
				if err == nil && !ok {
					err = errors.New("the advance did not match " + sub.ID.String())
				}
				log.add(err)
			})
		log.check(t)
		for _, sub := range subs {
			got := reread(t, s, sub)
			requireNewPeriod(t, got)
			if got.PlanID.String() != planID.String() || got.Quantity["seats"] != 7 {
				t.Errorf("%s: plan %s seats %d, want the new plan %s and 7 seats", sub.ID, got.PlanID, got.Quantity["seats"], planID)
			}
			if got.Status != subscription.StatusActive {
				t.Errorf("%s: status %q, want active", sub.ID, got.Status)
			}
		}
	})

	t.Run("pause against period advance", func(t *testing.T) {
		subs := many(endedPeriod)
		var log errorLog
		advanced := sync.Map{}
		raceEach(subs,
			func(sub *subscription.Subscription) {
				ok, err := s.PauseSubscription(ctx, sub.ID, now)
				if err == nil && !ok {
					err = errors.New("the pause did not match " + sub.ID.String())
				}
				log.add(err)
			},
			func(sub *subscription.Subscription) {
				ok, err := s.AdvanceSubscriptionPeriod(ctx, sub.ID, past, past, next, now)
				log.add(err)
				advanced.Store(sub.ID.String(), ok)
			})
		log.check(t)
		for _, sub := range subs {
			got := reread(t, s, sub)
			if got.Status != subscription.StatusPaused {
				t.Errorf("%s: status %q, want paused", sub.ID, got.Status)
			}
			// A paused period does not roll, so the advance matched only
			// if it came first, and then its period must have stayed.
			if ok, _ := advanced.Load(sub.ID.String()); ok == true {
				requireNewPeriod(t, got)
			} else if !sameInstantToMillisecond(got.CurrentPeriodEnd, past) {
				t.Errorf("%s: the advance did not match, yet the period ends %v, not %v", sub.ID, got.CurrentPeriodEnd, past)
			}
		}
	})

	t.Run("immediate cancel against cancel enactment", func(t *testing.T) {
		subs := many(func(x *subscription.Subscription) { c := past; x.CancelAt = &c })
		var (
			log     errorLog
			mu      sync.Mutex
			winners = map[string]int{}
		)
		win := func(sub *subscription.Subscription) {
			mu.Lock()
			defer mu.Unlock()
			winners[sub.ID.String()]++
		}
		raceEach(subs,
			func(sub *subscription.Subscription) {
				_, err := s.CancelSubscription(ctx, sub.ID, true)
				switch {
				case err == nil:
					win(sub)
				case !errors.Is(err, ledger.ErrSubscriptionCanceled):
					log.add(err)
				}
			},
			func(sub *subscription.Subscription) {
				ok, err := s.EnactSubscriptionCancel(ctx, sub.ID, now)
				log.add(err)
				if ok {
					win(sub)
				}
			})
		log.check(t)
		for _, sub := range subs {
			if n := winners[sub.ID.String()]; n != 1 {
				t.Errorf("%s: %d writes canceled it, want exactly 1", sub.ID, n)
			}
			if got := reread(t, s, sub); got.Status != subscription.StatusCanceled || got.CanceledAt == nil {
				t.Errorf("%s: status %q canceled_at %v, want canceled with a date", sub.ID, got.Status, got.CanceledAt)
			}
		}
	})

	t.Run("provider sync against the clock", func(t *testing.T) {
		subs := many(endedPeriod)
		var log errorLog
		raceEach(subs,
			func(sub *subscription.Subscription) {
				log.add(s.SetSubscriptionProvider(ctx, sub.ID, "sub_"+sub.ID.String(), "stripe"))
			},
			func(sub *subscription.Subscription) {
				_, err := s.AdvanceSubscriptionPeriod(ctx, sub.ID, past, past, next, now)
				log.add(err)
			})
		invs := make([]*invoice.Invoice, operatorRaceRows)
		for i := range invs {
			invs[i] = storedInvoice(t, s, appID, invoice.StatusPending, &past)
		}
		raceEach(invs,
			func(inv *invoice.Invoice) {
				log.add(s.SetInvoiceProvider(ctx, inv.ID, "in_"+inv.ID.String(), "stripe"))
			},
			func(inv *invoice.Invoice) {
				_, err := s.MarkInvoicePastDue(ctx, inv.ID, now)
				log.add(err)
			})
		log.check(t)
		for _, sub := range subs {
			got := reread(t, s, sub)
			requireNewPeriod(t, got)
			if got.ProviderID != "sub_"+sub.ID.String() {
				t.Errorf("%s: provider id %q lost", sub.ID, got.ProviderID)
			}
		}
		for _, inv := range invs {
			got, err := s.GetInvoice(ctx, inv.ID)
			if err != nil {
				t.Fatalf("GetInvoice: %v", err)
			}
			if got.Status != invoice.StatusPastDue || got.ProviderID != "in_"+inv.ID.String() {
				t.Errorf("%s: status %q provider %q, want past_due and the provider id", inv.ID, got.Status, got.ProviderID)
			}
		}
	})
}
