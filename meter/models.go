package meter

import (
	"time"

	"github.com/xraph/ledger/id"
)

type UsageEvent struct {
	ID             id.UsageEventID   `json:"id"`
	TenantID       string            `json:"tenant_id"`
	AppID          string            `json:"app_id"`
	FeatureKey     string            `json:"feature_key"`
	Quantity       int64             `json:"quantity"`
	Timestamp      time.Time         `json:"timestamp"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}
