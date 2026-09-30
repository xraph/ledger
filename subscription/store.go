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

// DueField names the date a lifecycle step reads from a subscription.
type DueField string

const (
	// DueCancel reads cancel_at, a scheduled cancellation.
	DueCancel DueField = "cancel_at"
	// DueTrialEnd reads trial_end.
	DueTrialEnd DueField = "trial_end"
	// DuePeriodEnd reads current_period_end.
	DuePeriodEnd DueField = "current_period_end"
)

// DueOpts selects the subscriptions whose Field is at or before Before and
// whose status is one of Statuses (any status when it is empty), earliest date
// first, ties broken by id. An empty AppID means every app: the lifecycle
// clock runs across apps.
type DueOpts struct {
	Field    DueField
	Before   time.Time
	Statuses []Status
	AppID    string
	Limit    int
	Offset   int
}
