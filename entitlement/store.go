package entitlement

import (
	"context"
	"time"
)

type Store interface {
	GetCached(ctx context.Context, tenantID, appID, featureKey string) (*Result, error)
	SetCached(ctx context.Context, tenantID, appID, featureKey string, result *Result, ttl time.Duration) error
	Invalidate(ctx context.Context, tenantID, appID string) error
	InvalidateFeature(ctx context.Context, tenantID, appID, featureKey string) error
}
