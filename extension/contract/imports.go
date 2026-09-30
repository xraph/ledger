package contract

import (
	"context"
	"strings"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/plan"
)

// ImportInput names one record at a payment provider. An empty provider_name
// means the first registered provider, as the engine reads it.
type ImportInput struct {
	ProviderName string `json:"provider_name"`
	ProviderID   string `json:"provider_id"`
}

// resolve trims the input and refuses a blank provider id before any provider
// is asked.
func (in ImportInput) resolve() (name, providerID string, err error) {
	providerID = strings.TrimSpace(in.ProviderID)
	if providerID == "" {
		return "", "", badRequest("provider_id is required")
	}
	return strings.TrimSpace(in.ProviderName), providerID, nil
}

// The four imports below file the record under the caller's app through
// ledger.ImportInto, so the provider never chooses the app, and answer in the
// shape of the entity's detail intent, so the page can open what it imported.
// A provider's refusal arrives as ledger.ErrProviderSync, which
// toContractError reports as UNAVAILABLE with the provider's words.

func plansImport(ctx context.Context, eng *ledger.Ledger, sc scope, in ImportInput) (*plan.Plan, error) {
	name, providerID, err := in.resolve()
	if err != nil {
		return nil, err
	}
	p, err := eng.ImportPlanFromProvider(ctx, name, providerID, ledger.ImportInto(sc.AppID))
	if err != nil {
		return nil, err
	}
	return withFeatures(p), nil
}

// featuresImport also runs from the empty scope, as features.create does:
// sc.AppID is then "" and the feature joins the shared catalog.
func featuresImport(ctx context.Context, eng *ledger.Ledger, sc scope, in ImportInput) (*feature.Feature, error) {
	name, providerID, err := in.resolve()
	if err != nil {
		return nil, err
	}
	return eng.ImportFeatureFromProvider(ctx, name, providerID, ledger.ImportInto(sc.AppID))
}

func subscriptionsImport(ctx context.Context, eng *ledger.Ledger, sc scope, in ImportInput) (SubscriptionDetail, error) {
	name, providerID, err := in.resolve()
	if err != nil {
		return SubscriptionDetail{}, err
	}
	s, err := eng.ImportSubscriptionFromProvider(ctx, name, providerID, ledger.ImportInto(sc.AppID))
	if err != nil {
		return SubscriptionDetail{}, err
	}
	return subscriptionsDetail(ctx, eng, sc, IDInput{ID: s.ID.String()})
}

func invoicesImport(ctx context.Context, eng *ledger.Ledger, sc scope, in ImportInput) (InvoiceDetail, error) {
	name, providerID, err := in.resolve()
	if err != nil {
		return InvoiceDetail{}, err
	}
	inv, err := eng.ImportInvoiceFromProvider(ctx, name, providerID, ledger.ImportInto(sc.AppID))
	if err != nil {
		return InvoiceDetail{}, err
	}
	return invoicesDetail(ctx, eng, sc, IDInput{ID: inv.ID.String()})
}
