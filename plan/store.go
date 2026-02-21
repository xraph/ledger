package plan

import (
	"context"

	"github.com/xraph/ledger/id"
)

type Store interface {
	Create(ctx context.Context, p *Plan) error
	Get(ctx context.Context, planID id.PlanID) (*Plan, error)
	GetBySlug(ctx context.Context, slug string, appID string) (*Plan, error)
	List(ctx context.Context, appID string, opts ListOpts) ([]*Plan, error)
	Update(ctx context.Context, p *Plan) error
	Delete(ctx context.Context, planID id.PlanID) error
	Archive(ctx context.Context, planID id.PlanID) error
}

type ListOpts struct {
	Status Status
	Limit  int
	Offset int
}
