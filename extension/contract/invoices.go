package contract

import (
	"context"
	"strings"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
)

func registerInvoices(b *binder) {
	query(b, "invoices.list", invoicesList)
	query(b, "invoices.detail", invoicesDetail)
	query(b, "invoices.pending", invoicesPending)
	query(b, "invoices.export", invoicesExport)
	command(b, "invoices.generate", invoicesGenerate)
	command(b, "invoices.finalize", invoicesFinalize)
	command(b, "invoices.markPaid", invoicesMarkPaid)
	command(b, "invoices.void", invoicesVoid)
	command(b, "invoices.syncToProvider", invoicesSync)
	command(b, "invoices.importFromProvider", invoicesImport)
}

func loadInvoice(ctx context.Context, eng *ledger.Ledger, sc scope, raw string) (*invoice.Invoice, error) {
	invID, err := parseID("id", raw, id.ParseInvoiceID)
	if err != nil {
		return nil, err
	}
	inv, err := eng.Store().GetInvoice(ctx, invID)
	if err != nil {
		return nil, err
	}
	if !sc.owns(inv.AppID) {
		return nil, notFound("invoice")
	}
	return withLineItems(inv), nil
}

// withLineItems replaces a nil line item list with an empty one, so an
// invoice always reaches the wire with "line_items": [] and never null.
func withLineItems(inv *invoice.Invoice) *invoice.Invoice {
	if inv != nil && inv.LineItems == nil {
		inv.LineItems = []invoice.LineItem{}
	}
	return inv
}

// reloadInvoice reads an invoice back after a command changed it.
func reloadInvoice(ctx context.Context, eng *ledger.Ledger, invID id.InvoiceID) (*invoice.Invoice, error) {
	inv, err := eng.Store().GetInvoice(ctx, invID)
	if err != nil {
		return nil, err
	}
	return withLineItems(inv), nil
}

type InvoicesListInput struct {
	PageInput
	TenantID string     `json:"tenant_id"`
	Status   string     `json:"status"`
	Start    *time.Time `json:"start"`
	End      *time.Time `json:"end"`
}

func invoicesList(ctx context.Context, eng *ledger.Ledger, sc scope, in InvoicesListInput) (Page[*invoice.Invoice], error) {
	switch invoice.Status(in.Status) {
	case "", invoice.StatusDraft, invoice.StatusPending, invoice.StatusPaid, invoice.StatusPastDue, invoice.StatusVoided:
	default:
		return Page[*invoice.Invoice]{}, badRequest("unknown invoice status %q", in.Status)
	}
	limit, offset := in.window()
	opts := invoice.ListOpts{Status: invoice.Status(in.Status), Limit: limit + 1, Offset: offset}
	if in.Start != nil {
		opts.Start = *in.Start
	}
	if in.End != nil {
		opts.End = *in.End
	}
	rows, err := eng.Store().ListInvoices(ctx, strings.TrimSpace(in.TenantID), sc.AppID, opts)
	if err != nil {
		return Page[*invoice.Invoice]{}, err
	}
	for _, inv := range rows {
		withLineItems(inv)
	}
	return pageFrom(rows, limit, offset), nil
}

type InvoiceDetail struct {
	Invoice       *invoice.Invoice           `json:"invoice"`
	Subscription  *subscription.Subscription `json:"subscription"`
	ExportFormats []string                   `json:"export_formats"`
}

func invoicesDetail(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (InvoiceDetail, error) {
	inv, err := loadInvoice(ctx, eng, sc, in.ID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	sub, err := eng.GetSubscription(ctx, inv.SubscriptionID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	return InvoiceDetail{Invoice: inv, Subscription: sub, ExportFormats: eng.InvoiceFormats()}, nil
}

func invoicesPending(ctx context.Context, eng *ledger.Ledger, sc scope, _ struct{}) ([]*invoice.Invoice, error) {
	rows, err := eng.Store().ListPendingInvoices(ctx, sc.AppID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*invoice.Invoice{}
	}
	for _, inv := range rows {
		withLineItems(inv)
	}
	return rows, nil
}

type InvoiceExportInput struct {
	ID     string `json:"id"`
	Format string `json:"format"`
}

type InvoiceExport struct {
	Format   string `json:"format"`
	Filename string `json:"filename"`
	Content  []byte `json:"content"`
}

func invoicesExport(ctx context.Context, eng *ledger.Ledger, sc scope, in InvoiceExportInput) (InvoiceExport, error) {
	inv, err := loadInvoice(ctx, eng, sc, in.ID)
	if err != nil {
		return InvoiceExport{}, err
	}
	body, err := eng.ExportInvoice(ctx, inv.ID, in.Format)
	if err != nil {
		return InvoiceExport{}, err
	}
	return InvoiceExport{Format: in.Format, Filename: "invoice-" + inv.ID.String() + "." + in.Format, Content: body}, nil
}

// InvoiceGenerateInput names the subscription and, optionally, a period it has
// already had. With no period the invoice is for the current one.
type InvoiceGenerateInput struct {
	SubscriptionID string     `json:"subscription_id"`
	PeriodStart    *time.Time `json:"period_start,omitempty"`
	PeriodEnd      *time.Time `json:"period_end,omitempty"`
}

func invoicesGenerate(ctx context.Context, eng *ledger.Ledger, sc scope, in InvoiceGenerateInput) (*invoice.Invoice, error) {
	sub, err := loadSubscription(ctx, eng, sc, "subscription_id", in.SubscriptionID)
	if err != nil {
		return nil, err
	}
	var opts []ledger.InvoiceOption
	switch {
	case in.PeriodStart == nil && in.PeriodEnd == nil:
	case in.PeriodStart == nil || in.PeriodEnd == nil:
		return nil, badRequest("period_start and period_end go together")
	default:
		opts = append(opts, ledger.ForPeriod(*in.PeriodStart, *in.PeriodEnd))
	}
	inv, err := eng.GenerateInvoice(ctx, sub.ID, opts...)
	if err != nil {
		return nil, err
	}
	return withLineItems(inv), nil
}

func invoicesFinalize(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*invoice.Invoice, error) {
	inv, err := loadInvoice(ctx, eng, sc, in.ID)
	if err != nil {
		return nil, err
	}
	if err := eng.FinalizeInvoice(ctx, inv.ID); err != nil {
		return nil, err
	}
	return reloadInvoice(ctx, eng, inv.ID)
}

type InvoiceMarkPaidInput struct {
	ID         string     `json:"id"`
	PaymentRef string     `json:"payment_ref"`
	PaidAt     *time.Time `json:"paid_at"`
}

func invoicesMarkPaid(ctx context.Context, eng *ledger.Ledger, sc scope, in InvoiceMarkPaidInput) (*invoice.Invoice, error) {
	inv, err := loadInvoice(ctx, eng, sc, in.ID)
	if err != nil {
		return nil, err
	}
	paidAt := time.Now().UTC()
	if in.PaidAt != nil {
		paidAt = in.PaidAt.UTC()
	}
	if err := eng.MarkInvoicePaid(ctx, inv.ID, paidAt, strings.TrimSpace(in.PaymentRef)); err != nil {
		return nil, err
	}
	return reloadInvoice(ctx, eng, inv.ID)
}

type InvoiceVoidInput struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func invoicesVoid(ctx context.Context, eng *ledger.Ledger, sc scope, in InvoiceVoidInput) (*invoice.Invoice, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, badRequest("a reason is required to void an invoice")
	}
	inv, err := loadInvoice(ctx, eng, sc, in.ID)
	if err != nil {
		return nil, err
	}
	if err := eng.MarkInvoiceVoided(ctx, inv.ID, reason); err != nil {
		return nil, err
	}
	return reloadInvoice(ctx, eng, inv.ID)
}

func invoicesSync(ctx context.Context, eng *ledger.Ledger, sc scope, in IDInput) (*provider.SyncResult, error) {
	inv, err := loadInvoice(ctx, eng, sc, in.ID)
	if err != nil {
		return nil, err
	}
	res, err := eng.SyncInvoiceToProvider(ctx, inv.ID)
	// The engine returns the result and the provider's error together when the
	// provider refuses. Passing that error on would turn a refusal into a bare
	// "internal error" and drop the result, so report the refusal as an answer:
	// Success is false and Error carries the provider's message. Only a call
	// that produced no result (the invoice is gone, or no provider is
	// configured) is an error.
	if res != nil {
		return res, nil
	}
	return nil, err
}
