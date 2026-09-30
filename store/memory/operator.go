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

func (s *Store) PauseSubscription(_ context.Context, subID id.SubscriptionID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	if !ok || (sub.Status != subscription.StatusActive && sub.Status != subscription.StatusTrialing) {
		return false, nil
	}
	sub.Status = subscription.StatusPaused
	sub.UpdatedAt = time.Now().UTC()
	return true, nil
}

func (s *Store) ResumeSubscription(_ context.Context, subID id.SubscriptionID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	if !ok || sub.Status != subscription.StatusPaused {
		return false, nil
	}
	sub.Status = subscription.StatusActive
	sub.UpdatedAt = time.Now().UTC()
	return true, nil
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
