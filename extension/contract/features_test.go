package contract

import (
	"context"
	"errors"
	"testing"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/provider"
)

func newFeatureInput(key string) FeatureCreateInput {
	return FeatureCreateInput{Key: key, Name: key, Type: string(feature.FeatureMetered), DefaultLimit: 100, Period: string(feature.PeriodMonthly)}
}

func TestFeaturesManifest(t *testing.T) {
	assertIntents(t, map[string]string{
		"features.list": "query", "features.detail": "query", "features.create": "command",
		"features.update": "command", "features.archive": "command", "features.delete": "command",
		"features.syncToProvider": "command",
	})
	assertInvalidates(t, map[string][]string{
		"features.create":         {"features.list"},
		"features.update":         {"features.list", "features.detail"},
		"features.archive":        {"features.list", "features.detail"},
		"features.delete":         {"features.list"},
		"features.syncToProvider": {"features.detail"},
	})
}

func TestFeaturesCreateAndScope(t *testing.T) {
	h := newHarness(t)
	own := mustCall(h, "app_a", featuresCreate, newFeatureInput("api_calls"))
	if own.AppID != "app_a" {
		t.Errorf("app = %q, want app_a", own.AppID)
	}
	if _, err := call(h, "app_a", featuresCreate, newFeatureInput("api_calls")); codeOf(err) != dash.CodeConflict {
		t.Errorf("duplicate key: got %v, want CONFLICT", err)
	}
	if _, err := call(h, "app_b", featuresCreate, newFeatureInput("api_calls")); err != nil {
		t.Errorf("another app may reuse the key: %v", err)
	}

	for name, in := range map[string]FeatureCreateInput{
		"no key":         {Name: "x", Type: "metered"},
		"unknown type":   {Key: "k", Name: "x", Type: "tiered"},
		"unknown period": {Key: "k2", Name: "x", Type: "metered", Period: "weekly"},
		"limit below -1": {Key: "k3", Name: "x", Type: "metered", DefaultLimit: -2},
	} {
		if _, err := call(h, "app_a", featuresCreate, in); codeOf(err) != dash.CodeBadRequest {
			t.Errorf("%s: got %v, want BAD_REQUEST", name, err)
		}
	}

	if _, err := call(h, "app_b", featuresDetail, IDInput{ID: own.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("another app's feature: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_a", featuresDetail, IDInput{ID: id.NewFeatureID().String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("unknown feature: got %v, want NOT_FOUND", err)
	}
	for _, key := range []string{"id", "key", "name", "type", "default_limit", "period", "soft_limit", "status", "app_id"} {
		if !wireKeys(t, own)[key] {
			t.Errorf("feature on the wire lacks %q", key)
		}
	}
}

func TestGlobalFeaturesAreReadableButNotWritableFromAnApp(t *testing.T) {
	h := newHarness(t)
	global := mustCall(h, "", featuresCreate, newFeatureInput("sso"))
	if global.AppID != "" {
		t.Fatalf("a feature created from an empty scope must be global, got app %q", global.AppID)
	}

	if _, err := call(h, "app_a", featuresDetail, IDInput{ID: global.ID.String()}); err != nil {
		t.Errorf("reading a global feature from an app: %v", err)
	}
	listed := mustCall(h, "app_a", featuresList, FeaturesListInput{Global: true})
	if len(listed.Items) != 1 || listed.Items[0].ID.String() != global.ID.String() {
		t.Errorf("global list from app_a: %+v", listed.Items)
	}

	name := "Renamed"
	if _, err := call(h, "app_a", featuresUpdate, FeatureUpdateInput{ID: global.ID.String(), Name: &name}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("an app writing a global feature: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_a", featuresDelete, IDInput{ID: global.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("an app deleting a global feature: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_a", featuresArchive, IDInput{ID: global.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("an app archiving a global feature: got %v, want NOT_FOUND", err)
	}
	if stored, _ := h.eng.GetFeature(ctxBackground(), global.ID); stored == nil || stored.Name != "sso" || stored.Status != feature.StatusActive {
		t.Errorf("a refused write changed the global feature: %+v", stored)
	}
}

func TestFeaturesUpdateArchiveDelete(t *testing.T) {
	h := newHarness(t)
	f := mustCall(h, "app_a", featuresCreate, newFeatureInput("exports"))

	limit := int64(250)
	got := mustCall(h, "app_a", featuresUpdate, FeatureUpdateInput{ID: f.ID.String(), DefaultLimit: &limit})
	if got.DefaultLimit != 250 || got.Name != "exports" {
		t.Errorf("got limit %d name %q; want 250 and the name unchanged", got.DefaultLimit, got.Name)
	}

	mustCall(h, "app_a", featuresArchive, IDInput{ID: f.ID.String()})
	if stored, _ := h.eng.GetFeature(ctxBackground(), f.ID); stored.Status != feature.StatusArchived {
		t.Errorf("after archive: %q", stored.Status)
	}
	mustCall(h, "app_a", featuresDelete, IDInput{ID: f.ID.String()})
	if _, err := call(h, "app_a", featuresDetail, IDInput{ID: f.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("after delete: got %v, want NOT_FOUND", err)
	}
}

func TestFeaturesUpdateValidatesAndKeepsKeyAndType(t *testing.T) {
	h := newHarness(t)
	f := mustCall(h, "app_a", featuresCreate, newFeatureInput("seats"))

	badPeriod, badLimit := "weekly", int64(-5)
	if _, err := call(h, "app_a", featuresUpdate, FeatureUpdateInput{ID: f.ID.String(), Period: &badPeriod}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("unknown period on update: got %v, want BAD_REQUEST", err)
	}
	if _, err := call(h, "app_a", featuresUpdate, FeatureUpdateInput{ID: f.ID.String(), DefaultLimit: &badLimit}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("limit below -1 on update: got %v, want BAD_REQUEST", err)
	}
	if stored, _ := h.eng.GetFeature(ctxBackground(), f.ID); stored.DefaultLimit != 100 || stored.Period != feature.PeriodMonthly {
		t.Errorf("a refused update was stored: limit %d period %q", stored.DefaultLimit, stored.Period)
	}

	unlimited, soft, desc := int64(-1), true, "Seats in use"
	meta := map[string]string{"tier": "gold"}
	got := mustCall(h, "app_a", featuresUpdate, FeatureUpdateInput{
		ID: f.ID.String(), DefaultLimit: &unlimited, SoftLimit: &soft, Description: &desc, Metadata: &meta,
	})
	if got.DefaultLimit != -1 || !got.SoftLimit || got.Description != desc || got.Metadata["tier"] != "gold" {
		t.Errorf("update did not apply: %+v", got)
	}
	if got.Key != "seats" || got.Type != feature.FeatureMetered {
		t.Errorf("key and type must not change: %q %q", got.Key, got.Type)
	}
}

func TestFeaturesList(t *testing.T) {
	h := newHarness(t)
	for _, key := range []string{"a", "b", "c"} {
		mustCall(h, "app_a", featuresCreate, newFeatureInput(key))
	}
	mustCall(h, "app_b", featuresCreate, newFeatureInput("other"))
	mustCall(h, "", featuresCreate, newFeatureInput("shared"))

	all := mustCall(h, "app_a", featuresList, FeaturesListInput{})
	if len(all.Items) != 3 {
		t.Errorf("app_a lists its own catalog only: got %d rows, want 3", len(all.Items))
	}

	first := mustCall(h, "app_a", featuresList, FeaturesListInput{PageInput: PageInput{Limit: 2}})
	if len(first.Items) != 2 || !first.HasMore {
		t.Errorf("first page: got %d rows, has_more %v; want 2 and true", len(first.Items), first.HasMore)
	}
	past := mustCall(h, "app_a", featuresList, FeaturesListInput{PageInput: PageInput{Limit: 2, Offset: 10}})
	if past.Items == nil || len(past.Items) != 0 || past.HasMore {
		t.Errorf("past the end: %+v, want empty items and has_more false", past)
	}

	clamped := mustCall(h, "app_a", featuresList, FeaturesListInput{PageInput: PageInput{Limit: 500}})
	if clamped.Limit != 200 {
		t.Errorf("a limit of 500 is reported as %d, want 200", clamped.Limit)
	}

	if _, err := call(h, "app_a", featuresList, FeaturesListInput{Status: "bogus"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("unknown status: got %v, want BAD_REQUEST", err)
	}
	mustCall(h, "app_a", featuresArchive, IDInput{ID: first.Items[0].ID.String()})
	archived := mustCall(h, "app_a", featuresList, FeaturesListInput{Status: string(feature.StatusArchived)})
	if len(archived.Items) != 1 {
		t.Errorf("archived filter: got %d rows, want 1", len(archived.Items))
	}
}

// featureProvider is a payment provider plugin whose SyncFeature answers with a
// fixed id and error. The rest of provider.Provider is left nil.
type featureProvider struct {
	baseProvider
	syncID  string
	syncErr error
}

func (f *featureProvider) Name() string                { return "fake" }
func (f *featureProvider) Provider() provider.Provider { return f }
func (f *featureProvider) SyncFeature(context.Context, *feature.Feature) (string, error) {
	return f.syncID, f.syncErr
}

func (h *harness) withFeatureProvider(fp *featureProvider) {
	eng := ledger.New(h.store, ledger.WithPlugin(fp))
	h.eng = eng
	h.deps = Deps{Engine: func() *ledger.Ledger { return eng }}
}

func TestFeaturesSyncToProvider(t *testing.T) {
	t.Run("no provider configured is unavailable", func(t *testing.T) {
		h := newHarness(t)
		f := mustCall(h, "app_a", featuresCreate, newFeatureInput("nosync"))
		if _, err := call(h, "app_a", featuresSync, IDInput{ID: f.ID.String()}); codeOf(err) != dash.CodeUnavailable {
			t.Errorf("got %v, want UNAVAILABLE", err)
		}
	})

	t.Run("a refusal is an answer, not an internal error", func(t *testing.T) {
		h := newHarness(t)
		h.withFeatureProvider(&featureProvider{syncErr: errors.New("meter rejected")})
		f := mustCall(h, "app_a", featuresCreate, newFeatureInput("refused"))

		got, err := call(h, "app_a", featuresSync, IDInput{ID: f.ID.String()})
		if err != nil {
			t.Fatalf("a provider refusal must not be an error, got %v", err)
		}
		if got == nil || got.Success || got.Error != "meter rejected" || got.EntityID != f.ID.String() {
			t.Errorf("got %+v, want an unsuccessful result carrying the provider's message", got)
		}
	})

	t.Run("success records the provider's id", func(t *testing.T) {
		h := newHarness(t)
		h.withFeatureProvider(&featureProvider{syncID: "feat_123"})
		f := mustCall(h, "app_a", featuresCreate, newFeatureInput("synced"))

		got := mustCall(h, "app_a", featuresSync, IDInput{ID: f.ID.String()})
		if !got.Success || got.ProviderID != "feat_123" {
			t.Errorf("got %+v, want a successful result for feat_123", got)
		}
		if stored, _ := h.eng.GetFeature(ctxBackground(), f.ID); stored.ProviderID != "feat_123" {
			t.Errorf("stored provider id %q, want feat_123", stored.ProviderID)
		}
	})

	t.Run("another app's feature is not found and nothing is pushed", func(t *testing.T) {
		h := newHarness(t)
		h.withFeatureProvider(&featureProvider{syncID: "feat_x"})
		foreign := mustCall(h, "app_b", featuresCreate, newFeatureInput("foreign"))
		if _, err := call(h, "app_a", featuresSync, IDInput{ID: foreign.ID.String()}); codeOf(err) != dash.CodeNotFound {
			t.Errorf("got %v, want NOT_FOUND", err)
		}
		if stored, _ := h.eng.GetFeature(ctxBackground(), foreign.ID); stored.ProviderID != "" {
			t.Errorf("a refused cross-app sync stamped provider id %q", stored.ProviderID)
		}
	})
}
