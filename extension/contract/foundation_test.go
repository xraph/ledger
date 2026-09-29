package contract

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/types"
)

// principal builds a principal carrying an app_id claim, or none when app is "".
func principal(app string) dash.Principal {
	if app == "" {
		return dash.Principal{Claims: map[string]any{}}
	}
	return dash.Principal{Claims: map[string]any{"app_id": app}}
}

func codeOf(err error) dash.ErrorCode {
	var ce *dash.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func TestManifestLoadsAndValidates(t *testing.T) {
	m, err := loadManifest()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Contributor.Name != contributorName {
		t.Errorf("contributor = %q, want %q", m.Contributor.Name, contributorName)
	}
	if err := validateManifest(m); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestResolveScope(t *testing.T) {
	cases := []struct {
		name     string
		p        dash.Principal
		deps     Deps
		wantApp  string
		wantCode dash.ErrorCode
	}{
		{"claim wins over config", principal("app_claim"), Deps{AppID: "app_cfg"}, "app_claim", ""},
		{"config when no claim", principal(""), Deps{AppID: "app_cfg"}, "app_cfg", ""},
		{"empty claim falls back to config", dash.Principal{Claims: map[string]any{"app_id": ""}}, Deps{AppID: "app_cfg"}, "app_cfg", ""},
		{"non-string claim falls back to config", dash.Principal{Claims: map[string]any{"app_id": 42}}, Deps{AppID: "app_cfg"}, "app_cfg", ""},
		{"single-app: nothing configured is allowed", principal(""), Deps{}, "", ""},
		{"required claim present", principal("app_claim"), Deps{RequireAppClaim: true}, "app_claim", ""},
		{"required claim missing is refused even with config", principal(""), Deps{AppID: "app_cfg", RequireAppClaim: true}, "", dash.CodePermissionDenied},
		{"nil claims map", dash.Principal{}, Deps{AppID: "app_cfg"}, "app_cfg", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc, err := resolveScope(c.p, c.deps)
			if got := codeOf(err); got != c.wantCode {
				t.Fatalf("code = %q, want %q (err %v)", got, c.wantCode, err)
			}
			if c.wantCode == "" && sc.AppID != c.wantApp {
				t.Errorf("app = %q, want %q", sc.AppID, c.wantApp)
			}
		})
	}
}

func TestScopeOwnership(t *testing.T) {
	sc := scope{AppID: "app_a"}
	if !sc.owns("app_a") || sc.owns("app_b") || sc.owns("") {
		t.Error("owns must match the app exactly")
	}
	if !sc.canRead("app_a") || !sc.canRead("") || sc.canRead("app_b") {
		t.Error("canRead must allow the scope's app and global (empty) entities only")
	}
	global := scope{AppID: ""}
	if !global.owns("") || global.owns("app_a") {
		t.Error("an empty scope owns only empty-app entities")
	}
}

func TestToContractError(t *testing.T) {
	cases := []struct {
		err  error
		want dash.ErrorCode
	}{
		{ledger.ErrPlanNotFound, dash.CodeNotFound},
		{ledger.ErrSubscriptionNotFound, dash.CodeNotFound},
		{ledger.ErrInvoiceNotFound, dash.CodeNotFound},
		{ledger.ErrCouponNotFound, dash.CodeNotFound},
		{ledger.ErrFeatureNotFound, dash.CodeNotFound},
		{ledger.ErrNotFound, dash.CodeNotFound},
		{fmt.Errorf("wrapped: %w", ledger.ErrInvalidInput), dash.CodeBadRequest},
		{ledger.ErrCouponInvalid, dash.CodeBadRequest},
		{ledger.ErrCouponExpired, dash.CodeBadRequest},
		{ledger.ErrCouponNotStarted, dash.CodeBadRequest},
		{ledger.ErrInvalidPricing, dash.CodeBadRequest},
		{ledger.ErrDuplicateFeature, dash.CodeBadRequest},
		{invoice.ErrInvalidTiers, dash.CodeBadRequest},
		{types.ErrOverflow, dash.CodeBadRequest},
		{ledger.ErrCouponAlreadyApplied, dash.CodeConflict},
		{ledger.ErrCouponExhausted, dash.CodeConflict},
		{ledger.ErrAlreadyExists, dash.CodeConflict},
		{ledger.ErrPlanInUse, dash.CodeConflict},
		{ledger.ErrInvoiceFinalized, dash.CodeConflict},
		{ledger.ErrInvoicePaid, dash.CodeConflict},
		{ledger.ErrInvoiceVoided, dash.CodeConflict},
		{ledger.ErrSubscriptionCanceled, dash.CodeConflict},
		{ledger.ErrProviderNotConfigured, dash.CodeUnavailable},
		{ledger.ErrProviderNotFound, dash.CodeUnavailable},
		{errors.New("disk on fire"), dash.CodeInternal},
		{&dash.Error{Code: dash.CodePermissionDenied}, dash.CodePermissionDenied},
	}
	for _, c := range cases {
		if got := codeOf(toContractError(c.err)); got != c.want {
			t.Errorf("%v -> %q, want %q", c.err, got, c.want)
		}
	}
	if toContractError(nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestInternalErrorDoesNotLeakDetail(t *testing.T) {
	err := toContractError(errors.New("pq: password authentication failed for user twinos"))
	var ce *dash.Error
	if !errors.As(err, &ce) {
		t.Fatal("want a *dash.Error")
	}
	if ce.Message != "internal error" {
		t.Errorf("message = %q; an internal error must not echo driver text to the client", ce.Message)
	}
}

func TestPageWindow(t *testing.T) {
	cases := []struct {
		in            PageInput
		limit, offset int
	}{
		{PageInput{}, 50, 0},
		{PageInput{Limit: 10, Offset: 20}, 10, 20},
		{PageInput{Limit: 500}, 200, 0},
		{PageInput{Limit: -5, Offset: -3}, 50, 0},
	}
	for _, c := range cases {
		l, o := c.in.window()
		if l != c.limit || o != c.offset {
			t.Errorf("%+v -> (%d, %d), want (%d, %d)", c.in, l, o, c.limit, c.offset)
		}
	}
}

func TestPageFrom(t *testing.T) {
	full := pageFrom([]int{1, 2, 3}, 2, 0)
	if !full.HasMore || len(full.Items) != 2 {
		t.Errorf("3 fetched for limit 2: got %+v, want 2 items and has_more", full)
	}
	last := pageFrom([]int{1, 2}, 2, 4)
	if last.HasMore || len(last.Items) != 2 || last.Offset != 4 {
		t.Errorf("exactly limit fetched: got %+v", last)
	}
	empty := pageFrom[int](nil, 50, 100)
	if empty.Items == nil || empty.HasMore {
		t.Errorf("past the end: got %+v, want empty non-nil items and no more", empty)
	}
}

func TestParseID(t *testing.T) {
	parse := func(s string) (id.ID, error) { return id.ParsePlanID(s) }
	if _, err := parseID("id", "", parse); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("empty id: got %v, want BAD_REQUEST", err)
	}
	if _, err := parseID("id", "not-an-id", parse); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("garbage id: got %v, want BAD_REQUEST", err)
	}
	if _, err := parseID("id", id.NewSubscriptionID().String(), parse); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("wrong prefix: got %v, want BAD_REQUEST", err)
	}
	good := id.NewPlanID()
	got, err := parseID("id", good.String(), parse)
	if err != nil || got.String() != good.String() {
		t.Errorf("valid id: got %v, %v", got, err)
	}
}

type echoIn struct {
	Value string `json:"value"`
}

func TestRunRefusesWithoutAnEngine(t *testing.T) {
	h := run(Deps{Engine: func() *ledger.Ledger { return nil }}, func(context.Context, *ledger.Ledger, scope, echoIn) (Ack, error) {
		t.Fatal("handler body must not run without an engine")
		return Ack{}, nil
	})
	if _, err := h(context.Background(), echoIn{}, principal("app_a")); codeOf(err) != dash.CodeUnavailable {
		t.Errorf("got %v, want UNAVAILABLE", err)
	}
}

func TestRunRefusesAMissingRequiredClaim(t *testing.T) {
	eng := ledger.New(memory.New())
	deps := Deps{Engine: func() *ledger.Ledger { return eng }, AppID: "app_cfg", RequireAppClaim: true}
	h := run(deps, func(context.Context, *ledger.Ledger, scope, echoIn) (Ack, error) {
		t.Fatal("handler body must not run without a resolved scope")
		return Ack{}, nil
	})
	if _, err := h(context.Background(), echoIn{}, principal("")); codeOf(err) != dash.CodePermissionDenied {
		t.Errorf("got %v, want PERMISSION_DENIED", err)
	}
}

func TestRunRefusesTheEmptyScopeUnlessThePlatformOptsIn(t *testing.T) {
	eng := ledger.New(memory.New())
	deps := Deps{Engine: func() *ledger.Ledger { return eng }}

	entered := false
	body := func(_ context.Context, _ *ledger.Ledger, sc scope, _ echoIn) (Ack, error) {
		entered = true
		if sc.AppID != "" {
			t.Errorf("scope app = %q, want the empty platform scope", sc.AppID)
		}
		return Ack{OK: true}, nil
	}

	_, err := run(deps, body)(context.Background(), echoIn{}, principal(""))
	if codeOf(err) != dash.CodePermissionDenied {
		t.Errorf("default policy from the empty scope: got %v, want PERMISSION_DENIED", err)
	}
	if entered {
		t.Fatal("the handler body must not run for the empty scope under the default policy")
	}

	got, err := runPlatform(deps, body)(context.Background(), echoIn{}, principal(""))
	if err != nil || !got.OK || !entered {
		t.Errorf("platform policy from the empty scope: got %+v, %v, entered %v; want the handler to run", got, err, entered)
	}
}

func TestPlatformIntentsAreExactlyTheFeatureCatalogAndSettings(t *testing.T) {
	b := newBinder(dispatcher.New(nil), Deps{})
	registerAll(b)
	if b.err != nil {
		t.Fatalf("register: %v", b.err)
	}
	for name := range b.kinds {
		wantPlatform := strings.HasPrefix(name, "features.") || name == "settings.detail"
		if b.platform[name] != wantPlatform {
			t.Errorf("%s: accepts the empty scope = %v, want %v", name, b.platform[name], wantPlatform)
		}
	}
}

func TestRunPassesScopeAndTranslatesErrors(t *testing.T) {
	eng := ledger.New(memory.New())
	deps := Deps{Engine: func() *ledger.Ledger { return eng }}
	var seen scope
	h := run(deps, func(_ context.Context, _ *ledger.Ledger, sc scope, _ echoIn) (Ack, error) {
		seen = sc
		return Ack{}, ledger.ErrPlanNotFound
	})
	_, err := h(context.Background(), echoIn{}, principal("app_a"))
	if seen.AppID != "app_a" {
		t.Errorf("scope app = %q, want app_a", seen.AppID)
	}
	if codeOf(err) != dash.CodeNotFound {
		t.Errorf("got %v, want NOT_FOUND", err)
	}
}

func TestBinderRecordsKindsAndRejectsDuplicates(t *testing.T) {
	b := newBinder(dispatcher.New(nil), Deps{})
	query(b, "things.list", func(context.Context, *ledger.Ledger, scope, echoIn) (Ack, error) { return Ack{}, nil })
	command(b, "things.poke", func(context.Context, *ledger.Ledger, scope, echoIn) (Ack, error) { return Ack{}, nil })
	if b.err != nil {
		t.Fatalf("register: %v", b.err)
	}
	if b.kinds["things.list"] != "query" || b.kinds["things.poke"] != "command" {
		t.Errorf("kinds = %v", b.kinds)
	}
	query(b, "things.list", func(context.Context, *ledger.Ledger, scope, echoIn) (Ack, error) { return Ack{}, nil })
	if b.err == nil {
		t.Error("a duplicate registration must set b.err")
	}
}
