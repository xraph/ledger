package contract

import (
	"context"
	"testing"
	"time"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/meter"
)

func ingestEvent(t *testing.T, h *harness, app, tenant, key string, qty int64) {
	t.Helper()
	if err := h.store.IngestBatch(context.Background(), []*meter.UsageEvent{{
		ID: id.NewUsageEventID(), TenantID: tenant, AppID: app, FeatureKey: key, Quantity: qty, Timestamp: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}
}

func TestUsageManifest(t *testing.T) {
	assertIntents(t, map[string]string{
		"usage.events": "query", "usage.aggregate": "query", "entitlements.check": "query",
		"entitlements.invalidate": "command", "paymentMethods.list": "query",
	})
	assertInvalidates(t, map[string][]string{
		"entitlements.invalidate": {"entitlements.check", "subscriptions.usage"},
	})
}

func TestUsageEventsStayInTheApp(t *testing.T) {
	h := newHarness(t)
	ingestEvent(t, h, "app_a", "acme", "api_calls", 5)
	ingestEvent(t, h, "app_a", "globex", "api_calls", 7)
	ingestEvent(t, h, "app_b", "acme", "api_calls", 999)

	all := mustCall(h, "app_a", usageEvents, UsageEventsInput{})
	if len(all.Items) != 2 {
		t.Errorf("app_a events = %d, want 2", len(all.Items))
	}
	for _, e := range all.Items {
		if e.AppID != "app_a" {
			t.Errorf("app_a saw an event from %s", e.AppID)
		}
	}
	acme := mustCall(h, "app_a", usageEvents, UsageEventsInput{TenantID: "acme"})
	if len(acme.Items) != 1 || acme.Items[0].Quantity != 5 {
		t.Errorf("acme in app_a: %+v", acme.Items)
	}
}

func TestUsageAggregateNeedsATenant(t *testing.T) {
	h := newHarness(t)
	ingestEvent(t, h, "app_a", "acme", "api_calls", 5)
	ingestEvent(t, h, "app_a", "acme", "api_calls", 6)

	got := mustCall(h, "app_a", usageAggregate, UsageAggregateInput{TenantID: "acme", FeatureKeys: []string{"api_calls"}, Period: "monthly"})
	if got.Totals["api_calls"] != 11 {
		t.Errorf("total = %d, want 11", got.Totals["api_calls"])
	}
	if _, err := call(h, "app_a", usageAggregate, UsageAggregateInput{FeatureKeys: []string{"api_calls"}, Period: "monthly"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("no tenant: got %v, want BAD_REQUEST", err)
	}
	if _, err := call(h, "app_a", usageAggregate, UsageAggregateInput{TenantID: "acme", Period: "monthly"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("no feature keys: got %v, want BAD_REQUEST", err)
	}
	if _, err := call(h, "app_a", usageAggregate, UsageAggregateInput{TenantID: "acme", FeatureKeys: []string{"api_calls"}, Period: "weekly"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("unknown period: got %v, want BAD_REQUEST", err)
	}
}

func TestEntitlementsCheckAndInvalidate(t *testing.T) {
	h := newHarness(t)
	h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	ingestEvent(t, h, "app_a", "acme", "api_calls", 400)

	got := mustCall(h, "app_a", entitlementsCheck, EntitlementCheckInput{TenantID: "acme", FeatureKey: "api_calls"})
	if !got.Allowed || got.Used != 400 || got.Limit != 1000 {
		t.Errorf("got %+v, want allowed with 400 of 1000 used", got)
	}
	other := mustCall(h, "app_b", entitlementsCheck, EntitlementCheckInput{TenantID: "acme", FeatureKey: "api_calls"})
	if other.Allowed {
		t.Error("acme has no subscription in app_b, so app_b must not see it as entitled")
	}
	if _, err := call(h, "app_a", entitlementsCheck, EntitlementCheckInput{FeatureKey: "api_calls"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("no tenant: got %v, want BAD_REQUEST", err)
	}
	if ack := mustCall(h, "app_a", entitlementsInvalidate, EntitlementInvalidateInput{TenantID: "acme"}); !ack.OK {
		t.Error("invalidate must acknowledge")
	}
	if ack := mustCall(h, "app_a", entitlementsInvalidate, EntitlementInvalidateInput{TenantID: "acme", FeatureKey: "api_calls"}); !ack.OK {
		t.Error("invalidating one feature must acknowledge")
	}
	if _, err := call(h, "app_a", entitlementsInvalidate, EntitlementInvalidateInput{}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("invalidate with no tenant: got %v, want BAD_REQUEST", err)
	}
}

func TestPaymentMethodsWithoutAProvider(t *testing.T) {
	h := newHarness(t)
	got := mustCall(h, "app_a", paymentMethodsList, PaymentMethodsInput{TenantID: "acme"})
	if got.Configured || got.Methods == nil || len(got.Methods) != 0 {
		t.Errorf("got %+v, want configured false and an empty non-nil list", got)
	}
}

func TestPaymentMethodsNeedATenant(t *testing.T) {
	h := newHarness(t)
	if _, err := call(h, "app_a", paymentMethodsList, PaymentMethodsInput{}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("no tenant: got %v, want BAD_REQUEST", err)
	}
}

// Usage timestamps are client supplied and a batch ingest shares one instant,
// so the id has to break the tie or a row repeats or vanishes between pages.
func TestUsageEventsPageStablyWhenTimestampsTie(t *testing.T) {
	h := newHarness(t)
	instant := time.Now().UTC().Truncate(time.Second)
	batch := make([]*meter.UsageEvent, 5)
	for i := range batch {
		batch[i] = &meter.UsageEvent{
			ID: id.NewUsageEventID(), TenantID: "acme", AppID: "app_a", FeatureKey: "api_calls",
			Quantity: int64(i + 1), Timestamp: instant,
		}
	}
	if err := h.store.IngestBatch(context.Background(), batch); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	seen := map[string]int{}
	var last Page[*meter.UsageEvent]
	for offset := 0; offset < 6; offset += 2 {
		last = mustCall(h, "app_a", usageEvents, UsageEventsInput{PageInput: PageInput{Limit: 2, Offset: offset}})
		for _, e := range last.Items {
			seen[e.ID.String()]++
		}
	}
	if len(seen) != 5 {
		t.Errorf("pages held %d distinct events, want 5", len(seen))
	}
	for eid, n := range seen {
		if n != 1 {
			t.Errorf("event %s appeared on %d pages, want 1", eid, n)
		}
	}
	if len(last.Items) != 1 || last.HasMore {
		t.Errorf("last page = %d items, has_more %v; want 1 item and no more", len(last.Items), last.HasMore)
	}
	past := mustCall(h, "app_a", usageEvents, UsageEventsInput{PageInput: PageInput{Limit: 2, Offset: 50}})
	if len(past.Items) != 0 || past.HasMore {
		t.Errorf("past the end: %d items, has_more %v; want empty", len(past.Items), past.HasMore)
	}
}

// The empty scope is the platform, not an app, and every store reads an empty
// app id as "all apps". usage.events must refuse it rather than list them all.
func TestUsageEventsRefuseAnEmptyScope(t *testing.T) {
	h := newHarness(t)
	ingestEvent(t, h, "app_a", "acme", "api_calls", 5)
	if _, err := call(h, "", usageEvents, UsageEventsInput{}); codeOf(err) != dash.CodePermissionDenied {
		t.Errorf("empty scope: got %v, want PERMISSION_DENIED", err)
	}
}
