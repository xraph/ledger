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
}
