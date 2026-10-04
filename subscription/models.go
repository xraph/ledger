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
	// status is paused and cleared by a resume, which moves the period end
	// and a running trial's end on by the length of the pause. A
	// subscription paused before this field existed has none.
	PausedAt *time.Time `json:"paused_at,omitempty"`
	// Stretch remembers the most recent period a resume stretched, so
	// ledger.ForPeriod can still prove the periods around it. Ledger keeps
	// it for itself; it is not part of the JSON.
	Stretch      *Stretch          `json:"-"`
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

// Stretch is the most recent billing period a resume stretched: it began at
// Start, was due to end at OriginalEnd, and ends at End once every pause in
// it is added on. The periods after it follow the clock's anchor from End
// (whatever nextPeriod gives); the ones before it ran on the cadence that led
// to OriginalEnd. Floor is where that earlier cadence began when an older
// stretch came before it (the older stretch's End): periods before Floor
// cannot be proved, since only one stretch is remembered. A nil Floor means
// the earlier cadence runs back to the subscription's creation.
type Stretch struct {
	Start       time.Time
	End         time.Time
	OriginalEnd time.Time
	Floor       *time.Time
}

// StretchColumns splits a stretch into the four nullable columns the stores
// keep it in, in UTC. A nil stretch is four nils.
func StretchColumns(st *Stretch) (start, end, originalEnd, floor *time.Time) {
	if st == nil {
		return nil, nil, nil, nil
	}
	utc := func(t time.Time) *time.Time { t = t.UTC(); return &t }
	start, end, originalEnd = utc(st.Start), utc(st.End), utc(st.OriginalEnd)
	if st.Floor != nil {
		floor = utc(*st.Floor)
	}
	return start, end, originalEnd, floor
}

// StretchFromColumns is StretchColumns the other way: nil unless start, end
// and originalEnd are all set.
func StretchFromColumns(start, end, originalEnd, floor *time.Time) *Stretch {
	if start == nil || end == nil || originalEnd == nil {
		return nil
	}
	return &Stretch{Start: *start, End: *end, OriginalEnd: *originalEnd, Floor: floor}
}

// Resume is what a resume writes, in one conditional store write that lands
// only while the subscription is still paused and still holds the PausedAt
// the engine read (nil: none). Everything here is worked out by the engine
// from that read, so a second pause and resume in between must not let it
// land. The period start never changes.
type Resume struct {
	// PausedAt is the paused_at the engine read, nil when there was none.
	PausedAt *time.Time
	// Status is what the subscription resumes to: trialing when its trial
	// was still running when it was paused, active otherwise.
	Status Status
	// PeriodEnd is the current period's end moved on by the length of the
	// pause, or nil to leave the period alone. A cancel scheduled for the
	// old period end (cancel_at equal to current_period_end) moves with it.
	PeriodEnd *time.Time
	// TrialEnd is the trial end moved on by the length of the pause, or nil
	// to leave trial_end as it is.
	TrialEnd *time.Time
	// Stretch is the stretch record to store, or nil to leave it as it is.
	Stretch *Stretch
}

// Renewal is what OnSubscriptionRenewed receives when the lifecycle clock
// moves a subscription on: the subscription in its new period, and every
// period that ended in the move, oldest first, catch-up periods included.
type Renewal struct {
	Subscription *Subscription `json:"subscription"`
	Ended        []Period      `json:"ended"`
}
