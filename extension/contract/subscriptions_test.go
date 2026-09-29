package contract

import (
	"context"
	"errors"
	"testing"
	"time"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
)

func TestSubscriptionsManifest(t *testing.T) {
	assertIntents(t, map[string]string{
		"subscriptions.list": "query", "subscriptions.detail": "query", "subscriptions.usage": "query",
		"subscriptions.create": "command", "subscriptions.changePlan": "command", "subscriptions.pause": "command",
		"subscriptions.resume": "command", "subscriptions.cancel": "command", "subscriptions.syncToProvider": "command",
	})
	assertInvalidates(t, map[string][]string{
		"subscriptions.create":         {"subscriptions.list", "overview.stats", "entitlements.check", "paymentMethods.list"},
		"subscriptions.changePlan":     {"subscriptions.list", "subscriptions.detail", "subscriptions.usage", "entitlements.check"},
		"subscriptions.pause":          {"subscriptions.list", "subscriptions.detail", "overview.stats", "entitlements.check"},
		"subscriptions.resume":         {"subscriptions.list", "subscriptions.detail", "overview.stats", "entitlements.check"},
		"subscriptions.cancel":         {"subscriptions.list", "subscriptions.detail", "overview.stats", "entitlements.check"},
		"subscriptions.syncToProvider": {"subscriptions.detail"},
	})
}

func TestSubscriptionsListFiltersByTenantWithinTheApp(t *testing.T) {
	h := newHarness(t)
	pa := h.activePlan("app_a", "a")
	pb := h.activePlan("app_b", "b")
	h.subscribe("app_a", "acme", pa)
	h.subscribe("app_a", "globex", pa)
	h.subscribe("app_b", "acme", pb)

	all := mustCall(h, "app_a", subscriptionsList, SubscriptionsListInput{})
	if len(all.Items) != 2 {
		t.Errorf("app_a lists %d subscriptions, want 2 (none from app_b)", len(all.Items))
	}
	acme := mustCall(h, "app_a", subscriptionsList, SubscriptionsListInput{TenantID: "acme"})
	if len(acme.Items) != 1 || acme.Items[0].AppID != "app_a" {
		t.Errorf("app_a filtered to acme: %+v", acme.Items)
	}
	if _, err := call(h, "app_a", subscriptionsList, SubscriptionsListInput{Status: "zombie"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("unknown status: got %v, want BAD_REQUEST", err)
	}
}

func TestSubscriptionsRefuseTheEmptyScope(t *testing.T) {
	h := newHarness(t)
	h.subscribe("app_a", "acme", h.activePlan("app_a", "visible"))
	if _, err := call(h, "", subscriptionsList, SubscriptionsListInput{}); codeOf(err) != dash.CodePermissionDenied {
		t.Errorf("subscriptions.list from an empty scope: got %v, want PERMISSION_DENIED (never every app's subscriptions)", err)
	}
}

func TestSubscriptionsCreateUsesTheScopeApp(t *testing.T) {
	h := newHarness(t)
	own := h.activePlan("app_a", "own")
	foreign := h.activePlan("app_b", "foreign")

	got := mustCall(h, "app_a", subscriptionsCreate, SubscriptionCreateInput{TenantID: "acme", PlanID: own.ID.String()})
	if got.AppID != "app_a" || got.Status != subscription.StatusActive {
		t.Errorf("got app %q status %q", got.AppID, got.Status)
	}
	if _, err := call(h, "app_a", subscriptionsCreate, SubscriptionCreateInput{TenantID: "acme", PlanID: foreign.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("another app's plan: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_a", subscriptionsCreate, SubscriptionCreateInput{PlanID: own.ID.String()}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("no tenant: got %v, want BAD_REQUEST", err)
	}
}

func TestSubscriptionsDetailAndScoping(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	foreign := h.subscribe("app_b", "acme", h.activePlan("app_b", "q"))

	got := mustCall(h, "app_a", subscriptionsDetail, IDInput{ID: sub.ID.String()})
	if got.Plan == nil || got.AppliedCoupons == nil {
		t.Errorf("detail must carry the plan and a non-nil applied_coupons list: %+v", got)
	}
	for _, key := range []string{"subscription", "plan", "applied_coupons"} {
		if !wireKeys(t, got)[key] {
			t.Errorf("detail on the wire lacks %q", key)
		}
	}

	for name, fn := range map[string]handlerFn[IDInput, *subscription.Subscription]{
		"pause": subscriptionsPause, "resume": subscriptionsResume,
	} {
		if _, err := call(h, "app_a", fn, IDInput{ID: foreign.ID.String()}); codeOf(err) != dash.CodeNotFound {
			t.Errorf("%s another app's subscription: got %v, want NOT_FOUND", name, err)
		}
	}
	if _, err := call(h, "app_a", subscriptionsDetail, IDInput{ID: foreign.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("detail of another app's subscription: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_a", subscriptionsDetail, IDInput{ID: id.NewSubscriptionID().String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("unknown subscription: got %v, want NOT_FOUND", err)
	}
}

func TestSubscriptionsUsage(t *testing.T) {
	h := newHarness(t)
	p := h.activePlan("app_a", "u")
	sub := &subscription.Subscription{TenantID: "acme", PlanID: p.ID, AppID: "app_a", Quantity: map[string]int64{"seats": 3}}
	if err := h.eng.CreateSubscription(context.Background(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err := h.store.IngestBatch(context.Background(), []*meter.UsageEvent{
		{ID: id.NewUsageEventID(), TenantID: "acme", AppID: "app_a", FeatureKey: "api_calls", Quantity: 1200, Timestamp: time.Now().UTC()},
		{ID: id.NewUsageEventID(), TenantID: "other", AppID: "app_a", FeatureKey: "api_calls", Quantity: 9999, Timestamp: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got := mustCall(h, "app_a", subscriptionsUsage, IDInput{ID: sub.ID.String()})
	byKey := map[string]FeatureUsage{}
	for _, f := range got.Features {
		byKey[f.Key] = f
	}
	api := byKey["api_calls"]
	if api.Used != 1200 || api.Limit != 1000 || api.Remaining != 0 || !api.OverLimit {
		t.Errorf("api_calls: %+v; want used 1200 of 1000, none remaining, over limit (and only acme's usage)", api)
	}
	seats := byKey["seats"]
	if seats.Used != 3 {
		t.Errorf("seats used = %d, want 3 from the subscription's quantity", seats.Used)
	}
}

func TestSubscriptionsChangePlanPauseResumeCancel(t *testing.T) {
	h := newHarness(t)
	from := h.activePlan("app_a", "from")
	to := h.activePlan("app_a", "to")
	sub := h.subscribe("app_a", "acme", from)

	moved := mustCall(h, "app_a", subscriptionsChangePlan, SubscriptionChangePlanInput{ID: sub.ID.String(), PlanID: to.ID.String()})
	if moved.PlanID.String() != to.ID.String() {
		t.Errorf("plan = %s, want %s", moved.PlanID, to.ID)
	}
	if got := mustCall(h, "app_a", subscriptionsPause, IDInput{ID: sub.ID.String()}); got.Status != subscription.StatusPaused {
		t.Errorf("pause: %q", got.Status)
	}
	if got := mustCall(h, "app_a", subscriptionsResume, IDInput{ID: sub.ID.String()}); got.Status != subscription.StatusActive {
		t.Errorf("resume: %q", got.Status)
	}
	// cancel_at is set by every cancel, immediate or not, so only the status
	// shows that an immediate cancel took effect now rather than at the
	// period end.
	got := mustCall(h, "app_a", subscriptionsCancel, SubscriptionCancelInput{ID: sub.ID.String(), Immediately: true})
	if got.Status != subscription.StatusCanceled {
		t.Errorf("immediate cancel: status %q, want %q", got.Status, subscription.StatusCanceled)
	}
	if got.CanceledAt == nil {
		t.Errorf("immediate cancel: canceled_at not set: %+v", got)
	}
}

func TestSubscriptionsCancelOnACanceledSubscriptionIsConflict(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "twice"))
	mustCall(h, "app_a", subscriptionsCancel, SubscriptionCancelInput{ID: sub.ID.String(), Immediately: true})

	for _, immediately := range []bool{false, true} {
		_, err := call(h, "app_a", subscriptionsCancel, SubscriptionCancelInput{ID: sub.ID.String(), Immediately: immediately})
		if codeOf(err) != dash.CodeConflict {
			t.Errorf("cancel again (immediately=%v): got %v, want CONFLICT", immediately, err)
		}
	}
}

func TestSubscriptionsSyncToProviderWithoutAProviderIsUnavailable(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "nosync"))
	if _, err := call(h, "app_a", subscriptionsSync, IDInput{ID: sub.ID.String()}); codeOf(err) != dash.CodeUnavailable {
		t.Errorf("got %v, want UNAVAILABLE", err)
	}
}

// subscriptionProvider adds SyncSubscription to fakeProvider, which answers
// only for plans.
type subscriptionProvider struct {
	fakeProvider
}

func (p *subscriptionProvider) Provider() provider.Provider { return p }

func (p *subscriptionProvider) SyncSubscription(context.Context, *subscription.Subscription) (string, error) {
	return p.syncID, p.syncErr
}

func TestSubscriptionsSyncToProviderReportsARefusalAsAnAnswer(t *testing.T) {
	h := newHarness(t)
	eng := ledger.New(h.store, ledger.WithPlugin(&subscriptionProvider{fakeProvider{syncErr: errors.New("card network says no")}}))
	h.eng = eng
	h.deps = Deps{Engine: func() *ledger.Ledger { return eng }}
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "refused"))

	got, err := call(h, "app_a", subscriptionsSync, IDInput{ID: sub.ID.String()})
	if err != nil {
		t.Fatalf("a provider refusal must not be an error, got %v", err)
	}
	if got == nil || got.Success || got.Error != "card network says no" || got.EntityID != sub.ID.String() {
		t.Errorf("got %+v, want an unsuccessful result carrying the provider's message", got)
	}
}
