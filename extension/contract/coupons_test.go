package contract

import (
	"encoding/json"
	"testing"
	"time"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/ledger/coupon"
)

func pctCoupon(code string, pct int) CouponCreateInput {
	return CouponCreateInput{Code: code, Name: code, Type: string(coupon.CouponTypePercentage), Percentage: pct, Currency: "usd"}
}

func TestCouponsManifest(t *testing.T) {
	assertIntents(t, map[string]string{
		"coupons.list": "query", "coupons.detail": "query", "coupons.create": "command",
		"coupons.update": "command", "coupons.delete": "command", "coupons.apply": "command",
	})
	assertInvalidates(t, map[string][]string{
		"coupons.create": {"coupons.list", "overview.stats"},
		"coupons.update": {"coupons.list", "coupons.detail", "subscriptions.detail"},
		"coupons.delete": {"coupons.list", "subscriptions.detail", "overview.stats"},
		"coupons.apply":  {"coupons.list", "coupons.detail", "subscriptions.detail"},
	})
}

func TestNullableDistinguishesAbsentNullAndValue(t *testing.T) {
	var in CouponUpdateInput
	if err := json.Unmarshal([]byte(`{"id":"x"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.ValidUntil.Set {
		t.Error("absent key must leave Set false")
	}
	if err := json.Unmarshal([]byte(`{"id":"x","valid_until":null}`), &in); err != nil {
		t.Fatal(err)
	}
	if !in.ValidUntil.Set || !in.ValidUntil.Null {
		t.Errorf("null must set Set and Null: %+v", in.ValidUntil)
	}
	in = CouponUpdateInput{}
	if err := json.Unmarshal([]byte(`{"id":"x","valid_until":"2030-01-01T00:00:00Z"}`), &in); err != nil {
		t.Fatal(err)
	}
	if !in.ValidUntil.Set || in.ValidUntil.Null || in.ValidUntil.Value.Year() != 2030 {
		t.Errorf("a value must set Set and Value: %+v", in.ValidUntil)
	}
}

func TestCouponsCreateScopedAndValidated(t *testing.T) {
	h := newHarness(t)
	got := mustCall(h, "app_a", couponsCreate, pctCoupon("LAUNCH10", 10))
	if got.AppID != "app_a" || got.TimesRedeemed != 0 {
		t.Errorf("got app %q times %d", got.AppID, got.TimesRedeemed)
	}
	if _, err := call(h, "app_a", couponsCreate, pctCoupon("LAUNCH10", 20)); codeOf(err) != dash.CodeConflict {
		t.Errorf("duplicate code: got %v, want CONFLICT", err)
	}
	if _, err := call(h, "app_a", couponsCreate, pctCoupon("P101", 101)); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("101 percent: got %v, want BAD_REQUEST", err)
	}
	if _, err := call(h, "app_b", couponsDetail, IDInput{ID: got.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("another app's coupon: got %v, want NOT_FOUND", err)
	}
}

func TestCouponsUpdateOmitVersusNull(t *testing.T) {
	h := newHarness(t)
	until := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	in := pctCoupon("WINDOW", 10)
	in.ValidUntil = &until
	c := mustCall(h, "app_a", couponsCreate, in)

	name := "Renamed"
	kept := mustCall(h, "app_a", couponsUpdate, CouponUpdateInput{ID: c.ID.String(), Name: &name})
	if kept.ValidUntil == nil || !kept.ValidUntil.Equal(until) {
		t.Errorf("omitting valid_until must leave it alone, got %v", kept.ValidUntil)
	}

	cleared := mustCall(h, "app_a", couponsUpdate, CouponUpdateInput{ID: c.ID.String(), ValidUntil: Nullable[time.Time]{Set: true, Null: true}})
	if cleared.ValidUntil != nil {
		t.Errorf("null valid_until must clear it, got %v", cleared.ValidUntil)
	}
	if cleared.Name != "Renamed" {
		t.Errorf("the earlier rename must survive, got %q", cleared.Name)
	}
}

func TestCouponsApplyAndDelete(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	foreignSub := h.subscribe("app_b", "acme", h.activePlan("app_b", "q"))
	c := mustCall(h, "app_a", couponsCreate, pctCoupon("SAVE", 10))

	applied := mustCall(h, "app_a", couponsApply, CouponApplyInput{SubscriptionID: sub.ID.String(), Code: "SAVE"})
	if applied.TimesRedeemed != 1 {
		t.Errorf("times redeemed = %d, want 1", applied.TimesRedeemed)
	}
	if _, err := call(h, "app_a", couponsApply, CouponApplyInput{SubscriptionID: sub.ID.String(), Code: "SAVE"}); codeOf(err) != dash.CodeConflict {
		t.Errorf("apply twice: got %v, want CONFLICT", err)
	}
	if _, err := call(h, "app_a", couponsApply, CouponApplyInput{SubscriptionID: foreignSub.ID.String(), Code: "SAVE"}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("apply to another app's subscription: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_b", couponsApply, CouponApplyInput{SubscriptionID: foreignSub.ID.String(), Code: "SAVE"}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("app_b applying app_a's code: got %v, want NOT_FOUND (codes are per app)", err)
	}

	mustCall(h, "app_a", couponsDelete, IDInput{ID: c.ID.String()})
	if _, err := call(h, "app_a", couponsDetail, IDInput{ID: c.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("after delete: got %v, want NOT_FOUND", err)
	}
}

// TestCouponsUpdateDecodesLikeTheDispatcher mirrors wrapTyped: route params are
// decoded into the input first, then the payload into the same struct. A
// Nullable absent from both must stay unset.
func TestCouponsUpdateDecodesLikeTheDispatcher(t *testing.T) {
	var in CouponUpdateInput
	if err := json.Unmarshal([]byte(`{"id":"coupon_x"}`), &in); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"name":"Renamed"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.ID != "coupon_x" || in.Name == nil || *in.Name != "Renamed" {
		t.Errorf("both decodes must land: %+v", in)
	}
	if in.ValidUntil.Set || in.ValidFrom.Set {
		t.Errorf("nullable fields absent from both decodes must stay unset: %+v %+v", in.ValidFrom, in.ValidUntil)
	}
}

func TestCouponsRefuseTheEmptyScope(t *testing.T) {
	h := newHarness(t)
	if _, err := call(h, "", couponsList, CouponsListInput{}); codeOf(err) != dash.CodePermissionDenied {
		t.Errorf("empty scope: got %v, want PERMISSION_DENIED", err)
	}
}
