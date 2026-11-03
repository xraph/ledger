package ledger_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// checkPlan has a quota of 10 api_calls and a boolean sso feature.
func checkPlan(t *testing.T, l *ledger.Ledger) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Name: "check", Slug: "check", Currency: "usd", AppID: "a1",
		Features: []plan.Feature{
			{Key: "api_calls", Name: "API calls", Type: plan.FeatureMetered, Limit: 10, Period: plan.PeriodMonthly},
			{Key: "sso", Name: "SSO", Type: plan.FeatureBoolean, Limit: 1},
		},
		Pricing: &plan.Pricing{BaseAmount: types.USD(4900), BillingPeriod: plan.PeriodMonthly},
	}
	ctx := context.Background()
	if err := l.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := l.ActivatePlan(ctx, p.ID); err != nil {
		t.Fatalf("ActivatePlan: %v", err)
	}
	return p
}

// subscribe puts t1 in a1 on the plan.
func subscribe(t *testing.T, l *ledger.Ledger, p *plan.Plan) {
	t.Helper()
	sub := &subscription.Subscription{TenantID: "t1", AppID: "a1", PlanID: p.ID}
	if err := l.CreateSubscription(context.Background(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
}

func TestCheckMatchesEntitled(t *testing.T) {
	ctx := ledger.WithTenant(context.Background(), "t1", "a1")

	cases := []struct {
		name    string
		feature string
		setup   func(t *testing.T, s *memory.Store, l *ledger.Ledger)
	}{
		{"no subscription", "api_calls", func(*testing.T, *memory.Store, *ledger.Ledger) {}},
		{"boolean feature", "sso", func(t *testing.T, _ *memory.Store, l *ledger.Ledger) { subscribe(t, l, checkPlan(t, l)) }},
		{"feature not in the plan", "nope", func(t *testing.T, _ *memory.Store, l *ledger.Ledger) { subscribe(t, l, checkPlan(t, l)) }},
		{"quota left", "api_calls", func(t *testing.T, _ *memory.Store, l *ledger.Ledger) { subscribe(t, l, checkPlan(t, l)) }},
		{"quota exceeded", "api_calls", func(t *testing.T, s *memory.Store, l *ledger.Ledger) {
			subscribe(t, l, checkPlan(t, l))
			ev := &meter.UsageEvent{ID: id.NewUsageEventID(), TenantID: "t1", AppID: "a1", FeatureKey: "api_calls", Quantity: 11, Timestamp: time.Now().UTC()}
			if err := s.IngestBatch(context.Background(), []*meter.UsageEvent{ev}); err != nil {
				t.Fatalf("IngestBatch: %v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := memory.New()
			l := ledger.New(s)
			c.setup(t, s, l)

			want, wantErr := l.Entitled(ctx, c.feature)
			got, gotErr := l.Check(ctx, c.feature)
			if wantErr != nil || gotErr != nil {
				t.Fatalf("Entitled err %v, Check err %v, want neither", wantErr, gotErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Check = %+v, Entitled = %+v", got, want)
			}
		})
	}
}

// outageStore fails the subscription lookup with an error that is neither
// sentinel, the way a dropped connection does.
type outageStore struct {
	*memory.Store
	aggregateErr error
	lookupErr    error
}

func (o *outageStore) GetActiveSubscription(ctx context.Context, tenantID, appID string) (*subscription.Subscription, error) {
	if o.lookupErr != nil {
		return nil, o.lookupErr
	}
	return o.Store.GetActiveSubscription(ctx, tenantID, appID)
}

func (o *outageStore) Aggregate(ctx context.Context, tenantID, appID, featureKey string, period plan.Period) (int64, error) {
	if o.aggregateErr != nil {
		return 0, o.aggregateErr
	}
	return o.Store.Aggregate(ctx, tenantID, appID, featureKey, period)
}

func TestCheckReportsStoreOutage(t *testing.T) {
	ctx := ledger.WithTenant(context.Background(), "t1", "a1")
	down := errors.New("connection refused")

	t.Run("subscription lookup", func(t *testing.T) {
		s := &outageStore{Store: memory.New(), lookupErr: down}
		l := ledger.New(s)

		res, err := l.Check(ctx, "api_calls")
		if !errors.Is(err, ledger.ErrUnavailable) || !errors.Is(err, down) {
			t.Fatalf("Check err = %v, want ErrUnavailable wrapping the cause", err)
		}
		if res != nil {
			t.Errorf("Check result = %+v, want nil on an outage", res)
		}

		// Entitled is unchanged: an outage still reads as no access.
		got, err := l.Entitled(ctx, "api_calls")
		if err != nil {
			t.Fatalf("Entitled err = %v, want nil", err)
		}
		if got.Allowed {
			t.Errorf("Entitled allowed a feature during an outage: %+v", got)
		}
	})

	t.Run("usage aggregate", func(t *testing.T) {
		mem := memory.New()
		s := &outageStore{Store: mem}
		l := ledger.New(s)
		subscribe(t, l, checkPlan(t, l))
		s.aggregateErr = down

		if _, err := l.Check(ctx, "api_calls"); !errors.Is(err, ledger.ErrUnavailable) || !errors.Is(err, down) {
			t.Errorf("Check err = %v, want ErrUnavailable wrapping the cause", err)
		}
	})

	t.Run("no subscription is an answer", func(t *testing.T) {
		l := ledger.New(&outageStore{Store: memory.New()})
		res, err := l.Check(ctx, "api_calls")
		if err != nil {
			t.Fatalf("Check err = %v, want nil for a tenant with no subscription", err)
		}
		if res.Allowed || res.Reason != "no active subscription" {
			t.Errorf("Check = %+v, want not allowed, no active subscription", res)
		}
	})
}

func TestUnavailableIsRetryable(t *testing.T) {
	if !ledger.IsRetryable(ledger.ErrUnavailable) {
		t.Error("IsRetryable(ErrUnavailable) = false, want true: an outage is temporary")
	}
}
