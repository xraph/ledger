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
	// PausedAt is when the subscription was paused. It is set while the
	// status is paused and cleared by a resume, which moves the trial end on
	// by the length of the pause. A subscription paused before this field
	// existed has none.
	PausedAt *time.Time `json:"paused_at,omitempty"`
	// ResumedAt is when the subscription was last resumed. A resume restarts
	// the billing period there, so no period before it can be invoiced by
	// name: the time in between was spent paused.
	ResumedAt    *time.Time        `json:"resumed_at,omitempty"`
	AppID        string            `json:"app_id"`
	ProviderID   string            `json:"provider_id,omitempty"`
	ProviderName string            `json:"provider_name,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`

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

// Resume is what a resume writes, in one conditional store write that lands
// only while the subscription is still paused and still holds the PausedAt
// the engine read (nil: none). Everything here is worked out by the engine
// from that read, so a second pause and resume in between must not let it
// land.
type Resume struct {
	// PausedAt is the paused_at the engine read, nil when there was none.
	PausedAt *time.Time
	// At is the moment of the resume, stored as resumed_at. paused_at is
	// cleared.
	At time.Time
	// Status is what the subscription resumes to: trialing when its trial
	// was still running when it was paused, active otherwise.
	Status Status
	// PeriodStart and PeriodEnd are the period the resume starts.
	PeriodStart time.Time
	PeriodEnd   time.Time
	// TrialEnd is the trial end moved on by the length of the pause, or nil
	// to leave trial_end as it is.
	TrialEnd *time.Time
}

// Renewal is what OnSubscriptionRenewed receives when the lifecycle clock
// moves a subscription on: the subscription in its new period, and every
// period that ended in the move, oldest first, catch-up periods included.
type Renewal struct {
	Subscription *Subscription `json:"subscription"`
	Ended        []Period      `json:"ended"`
}
