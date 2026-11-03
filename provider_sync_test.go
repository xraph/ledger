package ledger_test

import (
	"context"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/types"
)

// syncSink is a payment provider that counts SyncInvoice calls and answers
// each with the id "in_remote". Nothing else in provider.Provider is reached.
type syncSink struct {
	providerAPI
	calls int
}

func (s *syncSink) Name() string                { return "fake" }
func (s *syncSink) Provider() provider.Provider { return s }

func (s *syncSink) SyncInvoice(context.Context, *invoice.Invoice) (string, error) {
	s.calls++
	return "in_remote", nil
}

func pendingInvoice(t *testing.T, st *memory.Store, total int64) *invoice.Invoice {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	due := start.AddDate(0, 1, 7)
	inv := &invoice.Invoice{
		ID: id.NewInvoiceID(), TenantID: "acme", Status: invoice.StatusPending, Currency: "usd",
		Subtotal: types.USD(total), Total: types.USD(total), TaxAmount: types.Zero("usd"),
		DiscountAmount: types.Zero("usd"), CreditAmount: types.Zero("usd"),
		PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), DueDate: &due,
	}
	if err := st.CreateInvoice(context.Background(), inv); err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	return inv
}

func TestSyncInvoiceToProviderMarksAZeroTotalInvoicePaid(t *testing.T) {
	ctx := context.Background()
	sink := &syncSink{}
	st := memory.New()
	l := ledger.New(st, ledger.WithPlugin(sink))
	inv := pendingInvoice(t, st, 0)

	res, err := l.SyncInvoiceToProvider(ctx, inv.ID)
	if err != nil {
		t.Fatalf("SyncInvoiceToProvider: %v", err)
	}
	if !res.Success || res.ProviderID != "" {
		t.Errorf("result = %+v, want a success with no provider id", res)
	}
	if sink.calls != 0 {
		t.Errorf("provider SyncInvoice called %d times, want 0", sink.calls)
	}
	got, err := st.GetInvoice(ctx, inv.ID)
	if err != nil {
		t.Fatalf("GetInvoice: %v", err)
	}
	if got.Status != invoice.StatusPaid || got.PaidAt == nil || got.PaymentRef != "zero-total" {
		t.Errorf("invoice is %s, paid at %v, ref %q; want paid with ref zero-total", got.Status, got.PaidAt, got.PaymentRef)
	}
	if got.ProviderID != "" {
		t.Errorf("provider id = %q, want none", got.ProviderID)
	}
}

func TestSyncInvoiceToProviderSendsAnInvoiceWithSomethingToCollect(t *testing.T) {
	ctx := context.Background()
	sink := &syncSink{}
	st := memory.New()
	l := ledger.New(st, ledger.WithPlugin(sink))
	inv := pendingInvoice(t, st, 4900)

	res, err := l.SyncInvoiceToProvider(ctx, inv.ID)
	if err != nil {
		t.Fatalf("SyncInvoiceToProvider: %v", err)
	}
	if sink.calls != 1 || res.ProviderID != "in_remote" {
		t.Errorf("provider called %d times, result %+v; want one call returning in_remote", sink.calls, res)
	}
	got, err := st.GetInvoice(ctx, inv.ID)
	if err != nil {
		t.Fatalf("GetInvoice: %v", err)
	}
	if got.Status != invoice.StatusPending || got.ProviderID != "in_remote" {
		t.Errorf("invoice is %s with provider id %q, want pending with in_remote", got.Status, got.ProviderID)
	}
}
