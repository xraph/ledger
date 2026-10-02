package mongo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

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

func (s *Store) PauseSubscription(ctx context.Context, subID id.SubscriptionID, at time.Time) (bool, error) {
	return s.conditionalSubscriptionSet(ctx, "pause subscription",
		bson.M{
			"_id":    subID.String(),
			"status": bson.M{"$in": []string{string(subscription.StatusActive), string(subscription.StatusTrialing)}},
		},
		bson.M{"status": string(subscription.StatusPaused), "paused_at": at.UTC()})
}

// ResumeSubscription moves cancel_at with the period end when it equals the
// old end, which needs the stored values inside the update, so like
// EnactSubscriptionCancel it is a pipeline update on the driver's collection
// and bypasses grove's operation hooks.
func (s *Store) ResumeSubscription(ctx context.Context, subID id.SubscriptionID, r subscription.Resume) (bool, error) {
	// A nil paused_at in the filter matches a document whose field is null
	// or missing: one paused before the field existed.
	var pausedAt any
	if r.PausedAt != nil {
		pausedAt = r.PausedAt.UTC()
	}
	set := bson.D{
		{Key: "status", Value: string(r.Status)},
		{Key: "paused_at", Value: nil},
		{Key: "updated_at", Value: now()},
	}
	if r.PeriodEnd != nil {
		end := r.PeriodEnd.UTC()
		// Every field in one $set stage reads the document as it was, so
		// cancel_at is compared with the old period end. A missing cancel_at
		// stays missing.
		set = append(set,
			bson.E{Key: "cancel_at", Value: bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$cancel_at", "$current_period_end"}}, end, "$cancel_at",
			}}},
			bson.E{Key: "current_period_end", Value: end},
		)
	}
	if r.TrialEnd != nil {
		set = append(set, bson.E{Key: "trial_end", Value: r.TrialEnd.UTC()})
	}
	if r.Stretch != nil {
		start, end, originalEnd, floor := subscription.StretchColumns(r.Stretch)
		set = append(set,
			bson.E{Key: "stretch_start", Value: *start},
			bson.E{Key: "stretch_end", Value: *end},
			bson.E{Key: "stretch_original_end", Value: *originalEnd},
		)
		if floor != nil {
			set = append(set, bson.E{Key: "stretch_floor", Value: *floor})
		} else {
			set = append(set, bson.E{Key: "stretch_floor", Value: nil})
		}
	}
	res, err := s.mdb.Collection(colSubscriptions).UpdateOne(ctx,
		bson.M{"_id": subID.String(), "status": string(subscription.StatusPaused), "paused_at": pausedAt},
		mongo.Pipeline{{{Key: "$set", Value: set}}},
	)
	if err != nil {
		return false, fmt.Errorf("ledger/mongo: resume subscription: %w", err)
	}
	return res.MatchedCount > 0, nil
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
