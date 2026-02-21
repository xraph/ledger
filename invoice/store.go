package invoice

import (
	"context"
	"time"

	"github.com/xraph/ledger/id"
)

type Store interface {
	Create(ctx context.Context, inv *Invoice) error
	Get(ctx context.Context, invID id.InvoiceID) (*Invoice, error)
	List(ctx context.Context, tenantID, appID string, opts ListOpts) ([]*Invoice, error)
	Update(ctx context.Context, inv *Invoice) error
	GetByPeriod(ctx context.Context, tenantID, appID string, periodStart, periodEnd time.Time) (*Invoice, error)
	ListPending(ctx context.Context, appID string) ([]*Invoice, error)
	MarkPaid(ctx context.Context, invID id.InvoiceID, paidAt time.Time, paymentRef string) error
	MarkVoided(ctx context.Context, invID id.InvoiceID, reason string) error
}

type ListOpts struct {
	Status Status
	Start  time.Time
	End    time.Time
	Limit  int
	Offset int
}
