package contract

import (
	"fmt"
	"testing"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
)

func TestOverviewManifest(t *testing.T) {
	assertIntents(t, map[string]string{
		"overview.stats": "query", "overview.recentInvoices": "query", "settings.detail": "query",
	})
}

func TestOverviewStatsCountsOnlyTheApp(t *testing.T) {
	h := newHarness(t)
	pa := h.activePlan("app_a", "a1")
	h.activePlan("app_a", "a2")
	h.activePlan("app_b", "b1")
	h.subscribe("app_a", "acme", pa)
	h.subscribe("app_a", "globex", pa)

	got := mustCall(h, "app_a", overviewStats, struct{}{})
	if got.Plans != 2 || got.ActivePlans != 2 {
		t.Errorf("plans %d active %d, want 2 and 2", got.Plans, got.ActivePlans)
	}
	if got.SubscriptionsByStatus["active"] != 2 {
		t.Errorf("active subscriptions = %d, want 2", got.SubscriptionsByStatus["active"])
	}
	if got.Capped {
		t.Error("a handful of rows must not report capped")
	}
}

func TestOverviewStatsFromAnEmptyScopeIsRefused(t *testing.T) {
	h := newHarness(t)
	h.activePlan("app_a", "a1")
	if _, err := call(h, "", overviewStats, struct{}{}); codeOf(err) != dash.CodePermissionDenied {
		t.Errorf("overview.stats from the empty scope: got %v, want PERMISSION_DENIED", err)
	}
}

func TestOverviewRecentInvoicesIsNewestFirstAndClamped(t *testing.T) {
	h := newHarness(t)
	p := h.activePlan("app_a", "a1")
	var newest string
	for i, tenant := range []string{"t1", "t2", "t3"} {
		sub := h.subscribe("app_a", tenant, p)
		inv, err := h.eng.GenerateInvoice(ctxBackground(), sub.ID)
		if err != nil {
			t.Fatalf("GenerateInvoice %d: %v", i, err)
		}
		newest = inv.ID.String()
	}
	other := h.activePlan("app_b", "b1")
	osub := h.subscribe("app_b", "elsewhere", other)
	if _, err := h.eng.GenerateInvoice(ctxBackground(), osub.ID); err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	all := mustCall(h, "app_a", overviewRecentInvoices, RecentInvoicesInput{})
	if len(all) != 3 {
		t.Fatalf("got %d invoices, want the 3 of app_a", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].CreatedAt.Before(all[i].CreatedAt) {
			t.Errorf("invoice %d is newer than invoice %d, want newest first", i, i-1)
		}
	}
	if all[0].ID.String() != newest {
		t.Errorf("first invoice is %s, want the most recently generated %s", all[0].ID, newest)
	}
	two := mustCall(h, "app_a", overviewRecentInvoices, RecentInvoicesInput{Limit: 2})
	if len(two) != 2 || two[0].ID != all[0].ID {
		t.Errorf("limit 2 returned %d invoices, first %v", len(two), two)
	}
	empty := mustCall(h, "app_c", overviewRecentInvoices, RecentInvoicesInput{})
	if empty == nil || len(empty) != 0 {
		t.Errorf("an app with no invoices must answer an empty list, got %#v", empty)
	}
}

func TestSettingsDetailReadsConfigurationNotConstants(t *testing.T) {
	h := newHarness(t)
	h.deps.AppID = "app_cfg"
	h.deps.RequireAppClaim = true
	h.deps.Settings = func() SettingsView {
		return SettingsView{MeterBatchSize: 250, MeterFlushInterval: "7s", EntitlementCacheTTL: "45s", LifecycleInterval: "2m0s"}
	}
	got := mustCall(h, "app_a", settingsDetailFor(h.deps), struct{}{})
	if got.MeterBatchSize != 250 || got.MeterFlushInterval != "7s" || got.EntitlementCacheTTL != "45s" || got.LifecycleInterval != "2m0s" {
		t.Errorf("got %+v, want the configured values", got)
	}
	if got.AppID != "app_cfg" || !got.RequireAppClaim {
		t.Errorf("got app %q require %v", got.AppID, got.RequireAppClaim)
	}
	if got.Providers == nil || got.InvoiceFormats == nil {
		t.Error("providers and invoice_formats must be empty lists, not null")
	}
}

func TestSettingsDetailWithoutASettingsFunction(t *testing.T) {
	h := newHarness(t)
	got, err := call(h, "app_a", settingsDetailFor(h.deps), struct{}{})
	if err != nil {
		t.Fatalf("a nil Settings func must yield zero values, not an error: %v", err)
	}
	if got.MeterBatchSize != 0 || got.MeterFlushInterval != "" {
		t.Errorf("got %+v, want zero settings", got)
	}
}

func TestSettingsDetailSucceedsFromAnEmptyScope(t *testing.T) {
	h := newHarness(t)
	got := mustCallPlatform(h, "", settingsDetailFor(h.deps), struct{}{})
	if got.Providers == nil || got.InvoiceFormats == nil {
		t.Error("providers and invoice_formats must be empty lists, not null")
	}
	// The same body under the default policy is what registration would refuse.
	if _, err := call(h, "", settingsDetailFor(h.deps), struct{}{}); codeOf(err) != dash.CodePermissionDenied {
		t.Errorf("default policy from the empty scope: got %v, want PERMISSION_DENIED", err)
	}
}

func TestSettingsDetailWireShape(t *testing.T) {
	keys := wireKeys(t, SettingsDetail{})
	for _, k := range []string{"meter_batch_size", "meter_flush_interval", "entitlement_cache_ttl", "app_id", "require_app_claim", "providers", "invoice_formats"} {
		if !keys[k] {
			t.Errorf("settings.detail is missing wire key %q, have %v", k, keys)
		}
	}
	stats := wireKeys(t, OverviewStats{})
	for _, k := range []string{"plans", "active_plans", "subscriptions_by_status", "pending_invoices", "coupons", "capped"} {
		if !stats[k] {
			t.Errorf("overview.stats is missing wire key %q, have %v", k, stats)
		}
	}
}

func TestOverviewStatsReportsWhenACountHitsTheBound(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < statsScanLimit+1; i++ {
		c := &coupon.Coupon{ID: id.NewCouponID(), AppID: "app_a", Code: fmt.Sprintf("C%d", i)}
		if err := h.store.CreateCoupon(ctxBackground(), c); err != nil {
			t.Fatalf("CreateCoupon: %v", err)
		}
	}
	got := mustCall(h, "app_a", overviewStats, struct{}{})
	if !got.Capped {
		t.Error("capped must be true when a scan reached the bound")
	}
	if got.Coupons != statsScanLimit {
		t.Errorf("coupons = %d, want the bound %d as a lower bound", got.Coupons, statsScanLimit)
	}
}
