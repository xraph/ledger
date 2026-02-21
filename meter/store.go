package meter

import (
	"context"
	"time"

	"github.com/xraph/ledger/plan"
)

type Store interface {
	IngestBatch(ctx context.Context, events []*UsageEvent) error
	Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error)
	AggregateMulti(ctx context.Context, tenantID, appID string, featureKeys []string, period plan.Period) (map[string]int64, error)
	Query(ctx context.Context, tenantID, appID string, opts QueryOpts) ([]*UsageEvent, error)
	Purge(ctx context.Context, before time.Time) (int64, error)
}

type QueryOpts struct {
	FeatureKey string
	Start      time.Time
	End        time.Time
	Limit      int
	Offset     int
}
