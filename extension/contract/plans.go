package contract

import (
	"context"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
)

func registerPlans(b *binder) {
	query(b, "plans.list", plansList)
	query(b, "plans.detail", plansDetail)
	command(b, "plans.create", plansCreate)
	command(b, "plans.update", plansUpdate)
	command(b, "plans.archive", plansArchive)
	command(b, "plans.activate", plansActivate)
	command(b, "plans.delete", plansDelete)
	command(b, "plans.syncToProvider", plansSync)
}

// IDInput is the request for every intent that names one entity.
type IDInput struct {
	ID string `json:"id"`
}

type PlansListInput struct {
	PageInput
	Status string `json:"status"`
}

func plansList(ctx context.Context, eng *ledger.Ledger, sc scope, in PlansListInput) (Page[*plan.Plan], error) {
	switch plan.Status(in.Status) {
	case "", plan.StatusDraft, plan.StatusActive, plan.StatusArchived:
	default:
		return Page[*plan.Plan]{}, badRequest("unknown plan status %q", in.Status)
	}

	limit, offset := in.window()
	rows, err := eng.Store().ListPlans(ctx, sc.AppID, plan.ListOpts{Status: plan.Status(in.Status), Limit: limit + 1, Offset: offset})
	if err != nil {
		return Page[*plan.Plan]{}, err
	}
	return pageFrom(rows, limit, offset), nil
}

// loadPlan parses an id and loads the plan it names, refusing with NOT_FOUND
// when it belongs to another app.
func loadPlan(ctx context.Context, eng *ledger.Ledger, sc scope, field, raw string) (*plan.Plan, error) {
	planID, err := parseID(field, raw, id.ParsePlanID)
	if err != nil {
		return nil, err
	}
	p, err := eng.GetPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	if !sc.owns(p.AppID) {
		return nil, notFound("plan")
	}
	return p, nil
}

func plansDetail(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*plan.Plan, error) {
	return loadPlan(ctx, eng, sc, "id", in.ID)
}

type PlanCreateInput struct {
	Name        string            `json:"name"`
	Slug        string            `json:"slug"`
	Description string            `json:"description"`
	Currency    string            `json:"currency"`
	TrialDays   int               `json:"trial_days"`
	Features    []plan.Feature    `json:"features"`
	Pricing     *plan.Pricing     `json:"pricing"`
	Metadata    map[string]string `json:"metadata"`
}

// plansCreate creates a plan as a draft. Activating it is a separate command,
// so a half-built plan is never offered to subscribers.
func plansCreate(ctx context.Context, eng *ledger.Ledger, sc scope, in PlanCreateInput) (*plan.Plan, error) {
	p := &plan.Plan{
		Name: in.Name, Slug: in.Slug, Description: in.Description, Currency: in.Currency,
		TrialDays: in.TrialDays, Features: in.Features, Pricing: in.Pricing, Metadata: in.Metadata,
		AppID: sc.AppID, Status: plan.StatusDraft,
	}
	if err := eng.CreatePlan(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// PlanUpdateInput names only what changes. An omitted field is left alone.
// `features` and `pricing`, when present, replace the plan's whole list or
// pricing block.
type PlanUpdateInput struct {
	ID          string             `json:"id"`
	Name        *string            `json:"name"`
	Slug        *string            `json:"slug"`
	Description *string            `json:"description"`
	Currency    *string            `json:"currency"`
	TrialDays   *int               `json:"trial_days"`
	Features    *[]plan.Feature    `json:"features"`
	Pricing     *plan.Pricing      `json:"pricing"`
	Metadata    *map[string]string `json:"metadata"`
}

func plansUpdate(ctx context.Context, eng *ledger.Ledger, sc scope, in PlanUpdateInput) (*plan.Plan, error) {
	p, err := loadPlan(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.Slug != nil {
		p.Slug = *in.Slug
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.Currency != nil {
		p.Currency = *in.Currency
	}
	if in.TrialDays != nil {
		p.TrialDays = *in.TrialDays
	}
	if in.Features != nil {
		p.Features = *in.Features
	}
	if in.Pricing != nil {
		p.Pricing = in.Pricing
	}
	if in.Metadata != nil {
		p.Metadata = *in.Metadata
	}
	if err := eng.UpdatePlan(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func plansArchive(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (Ack, error) {
	p, err := loadPlan(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return Ack{}, err
	}
	if err := eng.ArchivePlan(ctx, p.ID); err != nil {
		return Ack{}, err
	}
	return Ack{OK: true}, nil
}

func plansActivate(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (Ack, error) {
	p, err := loadPlan(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return Ack{}, err
	}
	if err := eng.ActivatePlan(ctx, p.ID); err != nil {
		return Ack{}, err
	}
	return Ack{OK: true}, nil
}

func plansDelete(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (Ack, error) {
	p, err := loadPlan(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return Ack{}, err
	}
	if err := eng.DeletePlan(ctx, p.ID); err != nil {
		return Ack{}, err
	}
	return Ack{OK: true}, nil
}

func plansSync(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*provider.SyncResult, error) {
	p, err := loadPlan(ctx, eng, sc, "id", in.ID)
	if err != nil {
		return nil, err
	}
	res, err := eng.SyncPlanToProvider(ctx, p.ID)
	// The engine returns the result and the provider's error together when the
	// provider refuses. Passing that error on would turn a refusal into a bare
	// "internal error" and drop the result, so report the refusal as an answer:
	// Success is false and Error carries the provider's message. Only a call
	// that produced no result (the plan is gone, or no provider is configured)
	// is an error.
	if res != nil {
		return res, nil
	}
	return nil, err
}
