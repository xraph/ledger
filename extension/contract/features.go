package contract

import (
	"context"
	"errors"
	"strings"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/provider"
)

// registerFeatures binds the catalog through the platform helpers: a global
// feature belongs to no app, so the empty scope is a legitimate caller here.
func registerFeatures(b *binder) {
	platformQuery(b, "features.list", featuresList)
	platformQuery(b, "features.detail", featuresDetail)
	platformCommand(b, "features.create", featuresCreate)
	platformCommand(b, "features.update", featuresUpdate)
	platformCommand(b, "features.archive", featuresArchive)
	platformCommand(b, "features.delete", featuresDelete)
	platformCommand(b, "features.syncToProvider", featuresSync)
	platformCommand(b, "features.importFromProvider", featuresImport)
}

type FeaturesListInput struct {
	PageInput
	Status string `json:"status"`
	// Global lists the catalog shared by every app instead of this app's own.
	Global bool `json:"global"`
}

func featuresList(ctx context.Context, eng *ledger.Ledger, sc scope, in FeaturesListInput) (Page[*feature.Feature], error) {
	switch feature.Status(in.Status) {
	case "", feature.StatusDraft, feature.StatusActive, feature.StatusArchived:
	default:
		return Page[*feature.Feature]{}, badRequest("unknown feature status %q", in.Status)
	}

	limit, offset := in.window()
	opts := feature.ListOpts{Status: feature.Status(in.Status), Limit: limit + 1, Offset: offset}

	// The empty scope has no app of its own, and every store reads an empty app
	// id as "all apps", so it lists the global catalog and nothing else.
	global := in.Global || sc.AppID == ""

	var rows []*feature.Feature
	var err error
	if global {
		rows, err = eng.ListGlobalFeatures(ctx, opts)
	} else {
		rows, err = eng.ListFeatures(ctx, sc.AppID, opts)
	}
	if err != nil {
		return Page[*feature.Feature]{}, err
	}
	return pageFrom(rows, limit, offset), nil
}

// loadFeature loads a feature this scope may read, or write when write is true.
// A global feature (empty app id) is readable from every app but writable only
// from an empty scope, so an app that tries to change one is told NOT_FOUND.
func loadFeature(ctx context.Context, eng *ledger.Ledger, sc scope, raw string, write bool) (*feature.Feature, error) {
	featureID, err := parseID("id", raw, id.ParseFeatureID)
	if err != nil {
		return nil, err
	}
	f, err := eng.GetFeature(ctx, featureID)
	if err != nil {
		return nil, err
	}
	allowed := sc.canRead(f.AppID)
	if write {
		allowed = sc.owns(f.AppID)
	}
	if !allowed {
		return nil, notFound("feature")
	}
	return f, nil
}

func featuresDetail(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*feature.Feature, error) {
	return loadFeature(ctx, eng, sc, in.ID, false)
}

// FeatureCreateInput carries a new catalog feature. Its key and type are fixed
// once created: plans refer to a catalog feature by key and price it by type.
type FeatureCreateInput struct {
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Type         string            `json:"type"`
	DefaultLimit int64             `json:"default_limit"`
	Period       string            `json:"period"`
	SoftLimit    bool              `json:"soft_limit"`
	Metadata     map[string]string `json:"metadata"`
}

// validFeatureShape checks what the engine's feature writes do not: the engine
// stores whatever it is given. The rules live in ledger.ValidateFeature, which
// the provider imports share; this only words a refusal as a BAD_REQUEST.
func validFeatureShape(key, typ, period string, limit int64) error {
	err := ledger.ValidateFeature(&feature.Feature{
		Key: key, Type: feature.FeatureType(typ), Period: feature.Period(period), DefaultLimit: limit,
	})
	if err == nil {
		return nil
	}
	return badRequest("%s", strings.TrimPrefix(err.Error(), ledger.ErrInvalidInput.Error()+": "))
}

func featuresCreate(ctx context.Context, eng *ledger.Ledger, sc scope, in FeatureCreateInput) (*feature.Feature, error) {
	key := strings.TrimSpace(in.Key)
	if err := validFeatureShape(key, in.Type, in.Period, in.DefaultLimit); err != nil {
		return nil, err
	}
	if existing, err := eng.GetFeatureByKey(ctx, key, sc.AppID); err == nil && existing != nil {
		return nil, conflict("feature key %q already exists in this app", key)
	} else if err != nil && !errors.Is(err, ledger.ErrFeatureNotFound) {
		return nil, err
	}

	f := &feature.Feature{
		Key: key, Name: in.Name, Description: in.Description, Type: feature.FeatureType(in.Type),
		DefaultLimit: in.DefaultLimit, Period: feature.Period(in.Period), SoftLimit: in.SoftLimit,
		Metadata: in.Metadata, Status: feature.StatusActive, AppID: sc.AppID,
	}
	if err := eng.CreateFeature(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// FeatureUpdateInput names only what changes. An omitted field is left alone.
// There is no key or type: those are fixed once the feature is created.
type FeatureUpdateInput struct {
	ID           string             `json:"id"`
	Name         *string            `json:"name"`
	Description  *string            `json:"description"`
	DefaultLimit *int64             `json:"default_limit"`
	Period       *string            `json:"period"`
	SoftLimit    *bool              `json:"soft_limit"`
	Metadata     *map[string]string `json:"metadata"`
}

func featuresUpdate(ctx context.Context, eng *ledger.Ledger, sc scope, in FeatureUpdateInput) (*feature.Feature, error) {
	f, err := loadFeature(ctx, eng, sc, in.ID, true)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		f.Name = *in.Name
	}
	if in.Description != nil {
		f.Description = *in.Description
	}
	if in.DefaultLimit != nil {
		f.DefaultLimit = *in.DefaultLimit
	}
	if in.Period != nil {
		f.Period = feature.Period(*in.Period)
	}
	if in.SoftLimit != nil {
		f.SoftLimit = *in.SoftLimit
	}
	if in.Metadata != nil {
		f.Metadata = *in.Metadata
	}
	if err := validFeatureShape(f.Key, string(f.Type), string(f.Period), f.DefaultLimit); err != nil {
		return nil, err
	}
	if err := eng.UpdateFeature(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

func featuresArchive(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (Ack, error) {
	f, err := loadFeature(ctx, eng, sc, in.ID, true)
	if err != nil {
		return Ack{}, err
	}
	if err := eng.ArchiveFeature(ctx, f.ID); err != nil {
		return Ack{}, err
	}
	return Ack{OK: true}, nil
}

func featuresDelete(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (Ack, error) {
	f, err := loadFeature(ctx, eng, sc, in.ID, true)
	if err != nil {
		return Ack{}, err
	}
	if err := eng.DeleteFeature(ctx, f.ID); err != nil {
		return Ack{}, err
	}
	return Ack{OK: true}, nil
}

func featuresSync(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*provider.SyncResult, error) {
	f, err := loadFeature(ctx, eng, sc, in.ID, true)
	if err != nil {
		return nil, err
	}
	res, err := eng.SyncFeatureToProvider(ctx, f.ID)
	// The engine returns the result and the provider's error together when the
	// provider refuses. Passing that error on would turn a refusal into a bare
	// "internal error" and drop the result, so report the refusal as an answer:
	// Success is false and Error carries the provider's message. Only a call
	// that produced no result (the feature is gone, or no provider is
	// configured) is an error.
	if res != nil {
		return res, nil
	}
	return nil, err
}
