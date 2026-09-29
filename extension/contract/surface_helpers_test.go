package contract

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/types"
)

type harness struct {
	t     *testing.T
	eng   *ledger.Ledger
	store *memory.Store
	deps  Deps
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	s := memory.New()
	eng := ledger.New(s)
	return &harness{t: t, eng: eng, store: s, deps: Deps{Engine: func() *ledger.Ledger { return eng }}}
}

// call runs a handler body exactly as the dispatcher would, through run, as
// a principal whose app_id claim is app.
func call[I, O any](h *harness, app string, fn handlerFn[I, O], in I) (O, error) {
	return run(h.deps, fn)(context.Background(), in, principal(app))
}

func mustCall[I, O any](h *harness, app string, fn handlerFn[I, O], in I) O {
	h.t.Helper()
	out, err := call(h, app, fn, in)
	if err != nil {
		h.t.Fatalf("call: %v", err)
	}
	return out
}

// wireKeys returns the top-level JSON keys v marshals to, which is what the
// React plugin reads.
func wireKeys(t *testing.T, v any) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := map[string]bool{}
	for k := range m {
		keys[k] = true
	}
	return keys
}

func assertIntents(t *testing.T, want map[string]string) {
	t.Helper()
	m, err := loadManifest()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := map[string]string{}
	for _, in := range m.Intents {
		got[in.Name] = string(in.Kind)
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("intent %s: declared as %q, want %q", name, got[name], kind)
		}
	}
}

func assertInvalidates(t *testing.T, want map[string][]string) {
	t.Helper()
	m, err := loadManifest()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := map[string][]string{}
	for _, in := range m.Intents {
		got[in.Name] = append([]string(nil), in.Invalidates...)
	}
	for name, w := range want {
		g := got[name]
		sort.Strings(g)
		ws := append([]string(nil), w...)
		sort.Strings(ws)
		if len(g) != len(ws) {
			t.Errorf("%s invalidates %v, want %v", name, g, ws)
			continue
		}
		for i := range g {
			if g[i] != ws[i] {
				t.Errorf("%s invalidates %v, want %v", name, g, ws)
				break
			}
		}
	}
}

// activePlan creates and activates a plan in app through the engine.
func (h *harness) activePlan(app, slug string) *plan.Plan {
	h.t.Helper()
	ctx := context.Background()
	p := &plan.Plan{
		Name: slug, Slug: slug, Currency: "usd", AppID: app,
		Features: []plan.Feature{
			{Key: "api_calls", Name: "API calls", Type: plan.FeatureMetered, Limit: 1000, Period: plan.PeriodMonthly},
			{Key: "seats", Name: "Seats", Type: plan.FeatureSeat, Limit: 0, Period: plan.PeriodNone},
		},
		Pricing: &plan.Pricing{
			BaseAmount: types.USD(4900), BillingPeriod: plan.PeriodMonthly,
			Tiers: []plan.PriceTier{{FeatureKey: "api_calls", Type: plan.TierGraduated, UpTo: -1, UnitAmount: types.USD(3)}},
		},
	}
	if err := h.eng.CreatePlan(ctx, p); err != nil {
		h.t.Fatalf("CreatePlan: %v", err)
	}
	if err := h.eng.ActivatePlan(ctx, p.ID); err != nil {
		h.t.Fatalf("ActivatePlan: %v", err)
	}
	return p
}

func ctxBackground() context.Context { return context.Background() }
