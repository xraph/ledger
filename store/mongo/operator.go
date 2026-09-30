package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/subscription"
)

// The operator writes that can race the lifecycle clock. Each is one
// conditional update of one document that sets only its own fields; see
// store.Store.

func (s *Store) conditionalSubscriptionSet(ctx context.Context, what string, filter, set bson.M) (bool, error) {
	q := s.mdb.NewUpdate((*subscriptionModel)(nil)).Filter(filter)
	for field, value := range set {
		q = q.Set(field, value)
	}
	res, err := q.Set("updated_at", now()).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("ledger/mongo: %s: %w", what, err)
	}
	return res.MatchedCount() > 0, nil
}

func (s *Store) PauseSubscription(ctx context.Context, subID id.SubscriptionID) (bool, error) {
	return s.conditionalSubscriptionSet(ctx, "pause subscription",
		bson.M{
			"_id":    subID.String(),
			"status": bson.M{"$in": []string{string(subscription.StatusActive), string(subscription.StatusTrialing)}},
		},
		bson.M{"status": string(subscription.StatusPaused)})
}

func (s *Store) ResumeSubscription(ctx context.Context, subID id.SubscriptionID) (bool, error) {
	return s.conditionalSubscriptionSet(ctx, "resume subscription",
		bson.M{"_id": subID.String(), "status": string(subscription.StatusPaused)},
		bson.M{"status": string(subscription.StatusActive)})
}

func (s *Store) ChangeSubscriptionPlan(ctx context.Context, subID id.SubscriptionID, planID id.PlanID, quantity map[string]int64) (bool, error) {
	if quantity == nil {
		quantity = map[string]int64{}
	}
	return s.conditionalSubscriptionSet(ctx, "change subscription plan",
		bson.M{
			"_id":    subID.String(),
			"status": bson.M{"$nin": []string{string(subscription.StatusCanceled), string(subscription.StatusExpired)}},
		},
		bson.M{"plan_id": planID.String(), "quantity": quantity})
}

func (s *Store) SetSubscriptionProvider(ctx context.Context, subID id.SubscriptionID, providerID, providerName string) error {
	ok, err := s.conditionalSubscriptionSet(ctx, "set subscription provider",
		bson.M{"_id": subID.String()},
		bson.M{"provider_id": providerID, "provider_name": providerName})
	if err != nil {
		return err
	}
	if !ok {
		return ledger.ErrSubscriptionNotFound
	}
	return nil
}

func (s *Store) SetInvoiceProvider(ctx context.Context, invID id.InvoiceID, providerID, providerName string) error {
	res, err := s.mdb.NewUpdate((*invoiceModel)(nil)).
		Filter(bson.M{"_id": invID.String()}).
		Set("provider_id", providerID).
		Set("provider_name", providerName).
		Set("updated_at", now()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("ledger/mongo: set invoice provider: %w", err)
	}
	if res.MatchedCount() == 0 {
		return ledger.ErrInvoiceNotFound
	}
	return nil
}

// cancelMissed explains a CancelSubscription that matched no document: the
// subscription is missing, or it is already canceled or expired.
func (s *Store) cancelMissed(ctx context.Context, subID id.SubscriptionID) error {
	sub, err := s.GetSubscription(ctx, subID)
	if err != nil {
		return err
	}
	switch sub.Status {
	case subscription.StatusCanceled:
		return ledger.ErrSubscriptionCanceled
	case subscription.StatusExpired:
		return ledger.ErrSubscriptionExpired
	}
	return fmt.Errorf("ledger/mongo: cancel of subscription %s matched no document though it is %s", subID, sub.Status)
}
