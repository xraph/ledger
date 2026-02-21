package subscription

import (
	"context"
	"time"

	"github.com/xraph/ledger/id"
)

type Store interface {
	Create(ctx context.Context, s *Subscription) error
	Get(ctx context.Context, subID id.SubscriptionID) (*Subscription, error)
	GetActive(ctx context.Context, tenantID string, appID string) (*Subscription, error)
	List(ctx context.Context, tenantID string, appID string, opts ListOpts) ([]*Subscription, error)
	Update(ctx context.Context, s *Subscription) error
	Cancel(ctx context.Context, subID id.SubscriptionID, cancelAt time.Time) error
}

type ListOpts struct {
	Status Status
	Limit  int
	Offset int
}
