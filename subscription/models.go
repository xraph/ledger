package subscription

import (
	"time"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/types"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusTrialing Status = "trialing"
	StatusPastDue  Status = "past_due"
	StatusCanceled Status = "canceled"
	StatusExpired  Status = "expired"
	StatusPaused   Status = "paused"
)

type Subscription struct {
	types.Entity
	ID                 id.SubscriptionID `json:"id"`
	TenantID           string            `json:"tenant_id"`
	PlanID             id.PlanID         `json:"plan_id"`
	Status             Status            `json:"status"`
	CurrentPeriodStart time.Time         `json:"current_period_start"`
	CurrentPeriodEnd   time.Time         `json:"current_period_end"`
	TrialStart         *time.Time        `json:"trial_start,omitempty"`
	TrialEnd           *time.Time        `json:"trial_end,omitempty"`
	CanceledAt         *time.Time        `json:"canceled_at,omitempty"`
	CancelAt           *time.Time        `json:"cancel_at,omitempty"`
	EndedAt            *time.Time        `json:"ended_at,omitempty"`
	AppID              string            `json:"app_id"`
	ProviderID         string            `json:"provider_id,omitempty"`
	ProviderName       string            `json:"provider_name,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`

	// Quantity holds the current count for each quantity-priced plan
	// feature, keyed by feature key. Seats are the usual case.
	//
	// A seat count is a level and not a flow, so it is stored here and set
	// when the subscription is created or changed, rather than derived
	// from usage events. Aggregating a stream would answer "how many seats
	// were added this month", which is a different question.
	Quantity map[string]int64 `json:"quantity,omitempty"`
}

// Period is one billing period, [Start, End).
type Period struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Renewal is what OnSubscriptionRenewed receives when the lifecycle clock
// moves a subscription on: the subscription in its new period, and every
// period that ended in the move, oldest first, catch-up periods included.
type Renewal struct {
	Subscription *Subscription `json:"subscription"`
	Ended        []Period      `json:"ended"`
}
