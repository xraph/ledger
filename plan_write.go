package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/subscription"
)

// normalisePlan tidies a plan before validation: trimmed text, lowercase
// currency, draft status by default, ids for features and pricing that lack
// them, and pricing pointed at its plan.
func normalisePlan(p *plan.Plan) {
	p.Name = strings.TrimSpace(p.Name)
	p.Slug = strings.TrimSpace(p.Slug)
	p.Currency = strings.ToLower(strings.TrimSpace(p.Currency))
	if p.Status == "" {
		p.Status = plan.StatusDraft
	}
	for i := range p.Features {
		if p.Features[i].ID.IsNil() {
			p.Features[i].ID = id.NewFeatureID()
		}
	}
	if p.Pricing != nil {
		if p.Pricing.ID.IsNil() {
			p.Pricing.ID = id.NewPriceID()
		}
		p.Pricing.PlanID = p.ID
		p.Pricing.BaseAmount.Currency = strings.ToLower(p.Pricing.BaseAmount.Currency)
		if p.Pricing.BaseAmount.Currency == "" {
			p.Pricing.BaseAmount.Currency = p.Currency
		}
	}
}

// validatePlan refuses a plan that cannot be billed correctly. Tier ladders are
// checked here, at write time, so a broken ladder is refused when someone saves
// it rather than at the next billing run.
func validatePlan(p *plan.Plan) error {
	if p.Name == "" {
		return fmt.Errorf("%w: a plan needs a name", ErrInvalidInput)
	}
	if p.Slug == "" {
		return fmt.Errorf("%w: a plan needs a slug", ErrInvalidInput)
	}
	if p.Currency == "" {
		return fmt.Errorf("%w: a plan needs a currency", ErrInvalidInput)
	}
	switch p.Status {
	case plan.StatusDraft, plan.StatusActive, plan.StatusArchived:
	default:
		return fmt.Errorf("%w: unknown plan status %q", ErrInvalidInput, p.Status)
	}
	if p.TrialDays < 0 {
		return fmt.Errorf("%w: trial days cannot be negative", ErrInvalidInput)
	}

	keys := make(map[string]bool, len(p.Features))
	for _, f := range p.Features {
		if strings.TrimSpace(f.Key) == "" {
			return fmt.Errorf("%w: every plan feature needs a key", ErrInvalidInput)
		}
		if keys[f.Key] {
			return fmt.Errorf("%w: %q", ErrDuplicateFeature, f.Key)
		}
		keys[f.Key] = true
		switch f.Type {
		case plan.FeatureMetered, plan.FeatureBoolean, plan.FeatureSeat:
		default:
			return fmt.Errorf("%w: feature %q has unknown type %q", ErrInvalidInput, f.Key, f.Type)
		}
		switch f.Period {
		case "", plan.PeriodMonthly, plan.PeriodYearly, plan.PeriodNone:
		default:
			return fmt.Errorf("%w: feature %q has unknown period %q", ErrInvalidInput, f.Key, f.Period)
		}
		if f.Limit < -1 {
			return fmt.Errorf("%w: feature %q has limit %d; use -1 for unlimited", ErrInvalidInput, f.Key, f.Limit)
		}
	}

	if p.Pricing == nil {
		return nil
	}
	if p.Pricing.BaseAmount.Amount < 0 {
		return fmt.Errorf("%w: the base price cannot be negative", ErrInvalidPricing)
	}
	if !strings.EqualFold(p.Pricing.BaseAmount.Currency, p.Currency) {
		return fmt.Errorf("%w: the base price is in %s but the plan bills in %s",
			ErrInvalidPricing, p.Pricing.BaseAmount.Currency, p.Currency)
	}

	byFeature := map[string][]plan.PriceTier{}
	order := []string{}
	for _, t := range p.Pricing.Tiers {
		if !keys[t.FeatureKey] {
			return fmt.Errorf("%w: a tier prices feature %q, which the plan does not have", ErrInvalidPricing, t.FeatureKey)
		}
		if _, seen := byFeature[t.FeatureKey]; !seen {
			order = append(order, t.FeatureKey)
		}
		byFeature[t.FeatureKey] = append(byFeature[t.FeatureKey], t)
	}
	for _, key := range order {
		if err := invoice.ValidateTiers(byFeature[key], p.Currency); err != nil {
			return fmt.Errorf("feature %q: %w", key, err)
		}
	}
	return nil
}

func (l *Ledger) slugTaken(ctx context.Context, slug, appID string, except id.PlanID) (bool, error) {
	existing, err := l.store.GetPlanBySlug(ctx, slug, appID)
	switch {
	case err == nil && existing != nil:
		return existing.ID.String() != except.String(), nil
	case err != nil && !errors.Is(err, ErrPlanNotFound):
		return false, err
	}
	return false, nil
}

// UpdatePlan saves a plan's new state. The app and currency are fixed once a
// plan exists, because subscriptions, coupons and invoices are all priced in
// that currency.
func (l *Ledger) UpdatePlan(ctx context.Context, p *plan.Plan) error {
	existing, err := l.store.GetPlan(ctx, p.ID)
	if err != nil {
		return err
	}

	normalisePlan(p)
	if p.AppID != existing.AppID {
		return fmt.Errorf("%w: a plan cannot move between apps", ErrInvalidInput)
	}
	// EqualFold, not !=: a plan stored before currencies were normalised
	// carries "USD", and the normalised "usd" is the same currency.
	if !strings.EqualFold(p.Currency, existing.Currency) {
		return fmt.Errorf("%w: a plan's currency cannot change once it is created", ErrInvalidInput)
	}
	if err := validatePlan(p); err != nil {
		return err
	}
	if p.Slug != existing.Slug {
		taken, err := l.slugTaken(ctx, p.Slug, p.AppID, p.ID)
		if err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("%w: slug %q is already used in this app", ErrAlreadyExists, p.Slug)
		}
	}

	p.CreatedAt = existing.CreatedAt
	p.Touch()
	if err := l.store.UpdatePlan(ctx, p); err != nil {
		return err
	}

	l.plugins.EmitPlanUpdated(ctx, existing, p)
	return nil
}

// ArchivePlan stops new subscriptions to a plan. Existing subscriptions keep
// billing. Archiving an archived plan does nothing.
func (l *Ledger) ArchivePlan(ctx context.Context, planID id.PlanID) error {
	p, err := l.store.GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	if p.Status == plan.StatusArchived {
		return nil
	}
	if err := l.store.ArchivePlan(ctx, planID); err != nil {
		return err
	}

	l.plugins.EmitPlanArchived(ctx, planID.String())
	return nil
}

// ActivatePlan makes a plan available for new subscriptions. The plan must
// validate first.
func (l *Ledger) ActivatePlan(ctx context.Context, planID id.PlanID) error {
	p, err := l.store.GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	if p.Status == plan.StatusActive {
		return nil
	}

	previous := *p
	p.Status = plan.StatusActive
	if err := validatePlan(p); err != nil {
		return err
	}
	p.Touch()
	if err := l.store.UpdatePlan(ctx, p); err != nil {
		return err
	}

	l.plugins.EmitPlanUpdated(ctx, &previous, p)
	return nil
}

// DeletePlan removes a plan no subscription references, in any status. A plan
// with subscriptions is archived instead, because invoicing reads the plan.
func (l *Ledger) DeletePlan(ctx context.Context, planID id.PlanID) error {
	p, err := l.store.GetPlan(ctx, planID)
	if err != nil {
		return err
	}

	// An empty tenant lists every tenant within the plan's app, which is
	// exactly the set that could reference it.
	subs, err := l.store.ListSubscriptions(ctx, "", p.AppID, subscription.ListOpts{})
	if err != nil {
		return err
	}
	for _, s := range subs {
		if s.PlanID.String() == planID.String() {
			return fmt.Errorf("%w: subscription %s uses this plan; archive it instead", ErrPlanInUse, s.ID)
		}
	}

	return l.store.DeletePlan(ctx, planID)
}
