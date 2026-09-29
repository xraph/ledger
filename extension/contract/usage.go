package contract

import (
	"context"
	"strings"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
)

func registerUsage(b *binder) {
	query(b, "usage.events", usageEvents)
	query(b, "usage.aggregate", usageAggregate)
	query(b, "entitlements.check", entitlementsCheck)
	command(b, "entitlements.invalidate", entitlementsInvalidate)
	query(b, "paymentMethods.list", paymentMethodsList)
}

func requireTenant(raw string) (string, error) {
	tenant := strings.TrimSpace(raw)
	if tenant == "" {
		return "", badRequest("tenant_id is required")
	}
	return tenant, nil
}

type UsageEventsInput struct {
	PageInput
	TenantID   string     `json:"tenant_id"`
	FeatureKey string     `json:"feature_key"`
	Start      *time.Time `json:"start"`
	End        *time.Time `json:"end"`
}

// usageEvents lists raw usage events in the app, optionally for one tenant and
// feature, over the half-open window [start, end).
func usageEvents(ctx context.Context, eng *ledger.Ledger, sc scope, in UsageEventsInput) (Page[*meter.UsageEvent], error) {
	limit, offset := in.window()
	opts := meter.QueryOpts{FeatureKey: strings.TrimSpace(in.FeatureKey), Limit: limit + 1, Offset: offset}
	if in.Start != nil {
		opts.Start = *in.Start
	}
	if in.End != nil {
		opts.End = *in.End
	}
	rows, err := eng.Store().QueryUsage(ctx, strings.TrimSpace(in.TenantID), sc.AppID, opts)
	if err != nil {
		return Page[*meter.UsageEvent]{}, err
	}
	return pageFrom(rows, limit, offset), nil
}

type UsageAggregateInput struct {
	TenantID    string   `json:"tenant_id"`
	FeatureKeys []string `json:"feature_keys"`
	Period      string   `json:"period"`
}

type UsageTotals struct {
	Period string           `json:"period"`
	Totals map[string]int64 `json:"totals"`
}

func usageAggregate(ctx context.Context, eng *ledger.Ledger, sc scope, in UsageAggregateInput) (UsageTotals, error) {
	tenant, err := requireTenant(in.TenantID)
	if err != nil {
		return UsageTotals{}, err
	}
	if len(in.FeatureKeys) == 0 {
		return UsageTotals{}, badRequest("feature_keys needs at least one key")
	}
	switch plan.Period(in.Period) {
	case plan.PeriodMonthly, plan.PeriodYearly, plan.PeriodNone:
	default:
		return UsageTotals{}, badRequest("unknown period %q", in.Period)
	}
	totals, err := eng.Store().AggregateMulti(ctx, tenant, sc.AppID, in.FeatureKeys, plan.Period(in.Period))
	if err != nil {
		return UsageTotals{}, err
	}
	if totals == nil {
		totals = map[string]int64{}
	}
	return UsageTotals{Period: in.Period, Totals: totals}, nil
}

type EntitlementCheckInput struct {
	TenantID   string `json:"tenant_id"`
	FeatureKey string `json:"feature_key"`
}

// entitlementsCheck inspects a tenant's entitlement fresh from the store,
// without touching the cache or firing quota plugin events.
func entitlementsCheck(ctx context.Context, eng *ledger.Ledger, sc scope, in EntitlementCheckInput) (*entitlement.Result, error) {
	tenant, err := requireTenant(in.TenantID)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(in.FeatureKey)
	if key == "" {
		return nil, badRequest("feature_key is required")
	}
	return eng.InspectEntitlement(ctx, tenant, sc.AppID, key)
}

type EntitlementInvalidateInput struct {
	TenantID   string `json:"tenant_id"`
	FeatureKey string `json:"feature_key"`
}

// entitlementsInvalidate drops cached entitlement answers for a tenant in this
// app: every feature, or one when feature_key is given.
func entitlementsInvalidate(ctx context.Context, eng *ledger.Ledger, sc scope, in EntitlementInvalidateInput) (Ack, error) {
	tenant, err := requireTenant(in.TenantID)
	if err != nil {
		return Ack{}, err
	}
	key := strings.TrimSpace(in.FeatureKey)
	if key == "" {
		err = eng.Store().Invalidate(ctx, tenant, sc.AppID)
	} else {
		err = eng.Store().InvalidateFeature(ctx, tenant, sc.AppID, key)
	}
	if err != nil {
		return Ack{}, err
	}
	return Ack{OK: true}, nil
}

type PaymentMethodsInput struct {
	TenantID string `json:"tenant_id"`
}

type PaymentMethods struct {
	Configured bool                     `json:"configured"`
	Methods    []provider.PaymentMethod `json:"methods"`
}

// paymentMethodsList answers only for a tenant with a subscription in the
// caller's app. A payment provider is keyed by tenant alone, so its namespace
// is shared across apps and the provider cannot enforce the app boundary. The
// subscription lookup does, and it runs before the provider is consulted.
func paymentMethodsList(ctx context.Context, eng *ledger.Ledger, sc scope, in PaymentMethodsInput) (PaymentMethods, error) {
	tenant, err := requireTenant(in.TenantID)
	if err != nil {
		return PaymentMethods{}, err
	}
	subs, err := eng.Store().ListSubscriptions(ctx, tenant, sc.AppID, subscription.ListOpts{Limit: 1})
	if err != nil {
		return PaymentMethods{}, err
	}
	if len(subs) == 0 {
		return PaymentMethods{}, notFound("tenant")
	}
	if !eng.HasProviders() {
		return PaymentMethods{Configured: false, Methods: []provider.PaymentMethod{}}, nil
	}
	methods, err := eng.ListPaymentMethods(ctx, tenant)
	if err != nil {
		return PaymentMethods{}, err
	}
	if methods == nil {
		methods = []provider.PaymentMethod{}
	}
	return PaymentMethods{Configured: true, Methods: methods}, nil
}
