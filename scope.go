package ledger

import "context"

// scopeKey is the type of the context keys WithTenant writes. It is
// unexported, so no other package can collide with or forge them.
type scopeKey int

const (
	tenantScopeKey scopeKey = iota
	appScopeKey
)

// Legacy context keys. Callers that predate WithTenant put the tenant and app
// under these plain strings, and extractTenantID and extractAppID still read
// them when no typed scope is present.
const (
	legacyTenantKey = "tenant_id"
	legacyAppKey    = "app_id"
)

// WithTenant returns a context that scopes Meter, MeterWithKey, Entitled,
// Check and Remaining to a tenant and an app. It takes precedence over the
// plain "tenant_id" and "app_id" string keys older callers set, which still
// work when WithTenant is not used.
func WithTenant(ctx context.Context, tenantID, appID string) context.Context {
	ctx = context.WithValue(ctx, tenantScopeKey, tenantID)

	return context.WithValue(ctx, appScopeKey, appID)
}

func extractTenantID(ctx context.Context) string {
	return scopeValue(ctx, tenantScopeKey, legacyTenantKey)
}

func extractAppID(ctx context.Context) string {
	return scopeValue(ctx, appScopeKey, legacyAppKey)
}

// scopeValue reads the typed key first and the legacy string key second. A
// typed key set to the empty string counts as set, so WithTenant(ctx, "", "")
// does not fall through to a legacy value further up the chain.
func scopeValue(ctx context.Context, typed scopeKey, legacy string) string {
	if v, ok := ctx.Value(typed).(string); ok {
		return v
	}
	if v, ok := ctx.Value(legacy).(string); ok {
		return v
	}

	return ""
}
