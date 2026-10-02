package ledger_test

import (
	"context"
	"sync"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

// cancelAfterWrite is a memory store that cancels the run's context the moment
// a lifecycle write lands, as a run deadline or a Stop can.
type cancelAfterWrite struct {
	*memory.Store
	cancel context.CancelFunc
}

func (s *cancelAfterWrite) landed(ok bool, err error) (bool, error) {
	if ok {
		s.cancel()
	}
	return ok, err
}

func (s *cancelAfterWrite) AdvanceSubscriptionPeriod(ctx context.Context, subID id.SubscriptionID, start, end, now time.Time) (bool, error) {
	return s.landed(s.Store.AdvanceSubscriptionPeriod(ctx, subID, start, end, now))
}

func (s *cancelAfterWrite) EnactSubscriptionCancel(ctx context.Context, subID id.SubscriptionID, now time.Time) (bool, error) {
	return s.landed(s.Store.EnactSubscriptionCancel(ctx, subID, now))
}

func (s *cancelAfterWrite) EndSubscriptionTrial(ctx context.Context, subID id.SubscriptionID, now time.Time) (bool, error) {
	return s.landed(s.Store.EndSubscriptionTrial(ctx, subID, now))
}

func (s *cancelAfterWrite) MarkInvoicePastDue(ctx context.Context, invID id.InvoiceID, now time.Time) (bool, error) {
	return s.landed(s.Store.MarkInvoicePastDue(ctx, invID, now))
}

type runKey struct{}

// hookContexts records, per lifecycle hook, whether the context it got was
// still live and still carried the run's values.
type hookContexts struct {
	mu   sync.Mutex
	seen map[string]string
}

func (h *hookContexts) Name() string { return "hook-contexts" }

func (h *hookContexts) note(hook string, ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case ctx.Err() != nil:
		h.seen[hook] = "dead: " + ctx.Err().Error()
	case ctx.Value(runKey{}) != "run-1":
		h.seen[hook] = "lost the run's values"
	default:
		h.seen[hook] = "live"
	}
	return nil
}

func (h *hookContexts) OnSubscriptionRenewed(ctx context.Context, _ interface{}) error {
	return h.note("renewed", ctx)
}

func (h *hookContexts) OnSubscriptionCanceled(ctx context.Context, _ interface{}) error {
	return h.note("canceled", ctx)
}

func (h *hookContexts) OnSubscriptionTrialEnded(ctx context.Context, _ interface{}) error {
	return h.note("trial ended", ctx)
}

func (h *hookContexts) OnInvoicePastDue(ctx context.Context, _ interface{}) error {
	return h.note("past due", ctx)
}

// A hook fires after its write has landed and no later run announces that
// transition again, so a run context cancelled in between (a deadline, a Stop)
// must not reach it: each hook gets a live context that still carries the
// run's values.
func TestLifecycleHooksGetALiveContextWhenTheRunIsCancelled(t *testing.T) {
	s := &cancelAfterWrite{Store: memory.New()}
	hooks := &hookContexts{seen: map[string]string{}}
	l := ledger.New(s, ledger.WithPlugin(hooks))
	p := activePlan(t, l, "pro", "app_1", 0)
	seedSub(t, s.Store, p, func(*subscription.Subscription) {}) // period ended 1 February
	seedSub(t, s.Store, p, func(x *subscription.Subscription) {
		x.CurrentPeriodEnd = at(2026, 6, 1)
		x.CancelAt = ptr(at(2026, 2, 15))
	})
	seedSub(t, s.Store, p, func(x *subscription.Subscription) {
		x.Status = subscription.StatusTrialing
		x.CurrentPeriodEnd = at(2026, 6, 1)
		x.TrialEnd = ptr(at(2026, 2, 15))
	})
	due := at(2026, 2, 1)
	inv := &invoice.Invoice{
		ID: id.NewInvoiceID(), TenantID: "t1", AppID: "app_1", Status: invoice.StatusPending, DueDate: &due,
	}
	if err := s.CreateInvoice(context.Background(), inv); err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}

	now := at(2026, 3, 1)
	for _, step := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := l.AdvancePeriods(ctx, now); return err },
		func(ctx context.Context) error { _, err := l.EnactCancels(ctx, now); return err },
		func(ctx context.Context) error { _, err := l.EndTrials(ctx, now); return err },
		func(ctx context.Context) error { _, err := l.MarkInvoicesPastDue(ctx, now); return err },
	} {
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), runKey{}, "run-1"))
		s.cancel = cancel
		if err := step(ctx); err != nil {
			t.Fatalf("step: %v", err)
		}
		cancel()
	}

	for _, hook := range []string{"renewed", "canceled", "trial ended", "past due"} {
		if got := hooks.seen[hook]; got != "live" {
			t.Errorf("%s hook: context %q, want live", hook, got)
		}
	}
}
