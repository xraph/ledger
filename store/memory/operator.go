package memory

import (
	"context"
	"time"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/subscription"
)

// The operator writes that can race the lifecycle clock. Each checks its
// precondition and writes its own fields under the store's lock, as the
// database stores do in one conditional statement.

func (s *Store) PauseSubscription(_ context.Context, subID id.SubscriptionID, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	if !ok || (sub.Status != subscription.StatusActive && sub.Status != subscription.StatusTrialing) {
		return false, nil
	}
	pausedAt := at.UTC()
	sub.Status = subscription.StatusPaused
	sub.PausedAt = &pausedAt
	sub.UpdatedAt = time.Now().UTC()
	return true, nil
}

func (s *Store) ResumeSubscription(_ context.Context, subID id.SubscriptionID, r subscription.Resume) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	if !ok || sub.Status != subscription.StatusPaused || !sameTime(sub.PausedAt, r.PausedAt) {
		return false, nil
	}
	resumedAt := r.At.UTC()
	sub.Status = r.Status
	sub.CurrentPeriodStart = r.PeriodStart.UTC()
	sub.CurrentPeriodEnd = r.PeriodEnd.UTC()
	if r.TrialEnd != nil {
		trialEnd := r.TrialEnd.UTC()
		sub.TrialEnd = &trialEnd
	}
	sub.PausedAt = nil
	sub.ResumedAt = &resumedAt
	sub.UpdatedAt = time.Now().UTC()
	return true, nil
}

// sameTime reports whether two optional times are both unset or the same
// instant.
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func (s *Store) ChangeSubscriptionPlan(_ context.Context, subID id.SubscriptionID, planID id.PlanID, quantity map[string]int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	if !ok || sub.Status == subscription.StatusCanceled || sub.Status == subscription.StatusExpired {
		return false, nil
	}
	sub.PlanID = planID
	sub.Quantity = copySubscriptionQuantity(quantity)
	sub.UpdatedAt = time.Now().UTC()
	return true, nil
}

func (s *Store) SetSubscriptionProvider(_ context.Context, subID id.SubscriptionID, providerID, providerName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	if !ok {
		return ledger.ErrSubscriptionNotFound
	}
	sub.ProviderID = providerID
	sub.ProviderName = providerName
	sub.UpdatedAt = time.Now().UTC()
	return nil
}

// SetInvoiceProvider stores a changed copy, for the reason MarkInvoicePastDue
// gives.
func (s *Store) SetInvoiceProvider(_ context.Context, invID id.InvoiceID, providerID, providerName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	inv, ok := s.invoices[invID.String()]
	if !ok {
		return ledger.ErrInvoiceNotFound
	}
	cp := *inv
	cp.ProviderID = providerID
	cp.ProviderName = providerName
	cp.UpdatedAt = time.Now().UTC()
	s.invoices[invID.String()] = &cp
	return nil
}
