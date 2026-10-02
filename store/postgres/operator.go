package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/subscription"
)

// The operator writes that can race the lifecycle clock. Each is one
// conditional UPDATE that names only its own columns; see store.Store.

func (s *Store) PauseSubscription(ctx context.Context, subID id.SubscriptionID, at time.Time) (bool, error) {
	return changed(s.pg.NewUpdate((*subscriptionModel)(nil)).
		Set("status = ?", string(subscription.StatusPaused)).
		Set("paused_at = ?", at.UTC()).
		Set("updated_at = ?", now()).
		Where("id = ?", subID.String()).
		Where("status IN (?, ?)", string(subscription.StatusActive), string(subscription.StatusTrialing)).
		Exec(ctx))
}

func (s *Store) ResumeSubscription(ctx context.Context, subID id.SubscriptionID, r subscription.Resume) (bool, error) {
	q := s.pg.NewUpdate((*subscriptionModel)(nil)).
		Set("status = ?", string(r.Status)).
		Set("current_period_start = ?", r.PeriodStart.UTC()).
		Set("current_period_end = ?", r.PeriodEnd.UTC()).
		Set("paused_at = NULL").
		Set("resumed_at = ?", r.At.UTC()).
		Set("updated_at = ?", now())
	if r.TrialEnd != nil {
		q = q.Set("trial_end = ?", r.TrialEnd.UTC())
	}
	q = q.Where("id = ?", subID.String()).
		Where("status = ?", string(subscription.StatusPaused))
	if r.PausedAt == nil {
		q = q.Where("paused_at IS NULL")
	} else {
		// The value round-trips from this row, read a moment ago, so it
		// binds in the form the column holds.
		q = q.Where("paused_at = ?", r.PausedAt.UTC())
	}
	return changed(q.Exec(ctx))
}

func (s *Store) ChangeSubscriptionPlan(ctx context.Context, subID id.SubscriptionID, planID id.PlanID, quantity map[string]int64) (bool, error) {
	if quantity == nil {
		quantity = map[string]int64{}
	}
	quantityJSON, err := json.Marshal(quantity)
	if err != nil {
		return false, fmt.Errorf("ledger/postgres: encode quantity: %w", err)
	}
	return changed(s.pg.NewUpdate((*subscriptionModel)(nil)).
		Set("plan_id = ?", planID.String()).
		Set("quantity = ?::jsonb", string(quantityJSON)).
		Set("updated_at = ?", now()).
		Where("id = ?", subID.String()).
		Where("status NOT IN (?, ?)", string(subscription.StatusCanceled), string(subscription.StatusExpired)).
		Exec(ctx))
}

func (s *Store) SetSubscriptionProvider(ctx context.Context, subID id.SubscriptionID, providerID, providerName string) error {
	ok, err := changed(s.pg.NewUpdate((*subscriptionModel)(nil)).
		Set("provider_id = ?", providerID).
		Set("provider_name = ?", providerName).
		Set("updated_at = ?", now()).
		Where("id = ?", subID.String()).
		Exec(ctx))
	if err != nil {
		return err
	}
	if !ok {
		return ledger.ErrSubscriptionNotFound
	}
	return nil
}

func (s *Store) SetInvoiceProvider(ctx context.Context, invID id.InvoiceID, providerID, providerName string) error {
	ok, err := changed(s.pg.NewUpdate((*invoiceModel)(nil)).
		Set("provider_id = ?", providerID).
		Set("provider_name = ?", providerName).
		Set("updated_at = ?", now()).
		Where("id = ?", invID.String()).
		Exec(ctx))
	if err != nil {
		return err
	}
	if !ok {
		return ledger.ErrInvoiceNotFound
	}
	return nil
}

// cancelMissed explains a CancelSubscription that matched no row: the
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
	return fmt.Errorf("ledger/postgres: cancel of subscription %s matched no row though it is %s", subID, sub.Status)
}
