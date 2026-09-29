package contract

import (
	"context"
	"errors"
	"strings"
	"testing"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/types"
)

func TestInvoicesManifest(t *testing.T) {
	assertIntents(t, map[string]string{
		"invoices.list": "query", "invoices.detail": "query", "invoices.pending": "query", "invoices.export": "query",
		"invoices.generate": "command", "invoices.finalize": "command", "invoices.markPaid": "command",
		"invoices.void": "command", "invoices.syncToProvider": "command",
	})
	transitions := []string{"invoices.list", "invoices.detail", "invoices.pending", "overview.stats", "overview.recentInvoices"}
	assertInvalidates(t, map[string][]string{
		"invoices.generate":       {"invoices.list", "invoices.pending", "subscriptions.detail", "overview.stats", "overview.recentInvoices"},
		"invoices.finalize":       transitions,
		"invoices.markPaid":       transitions,
		"invoices.void":           transitions,
		"invoices.syncToProvider": {"invoices.detail"},
	})
}

func TestInvoicesGenerateIsScopedAndOncePerPeriod(t *testing.T) {
	h := newHarness(t)
	own := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	foreign := h.subscribe("app_b", "acme", h.activePlan("app_b", "q"))

	inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: own.ID.String()})
	if inv.AppID != "app_a" || inv.Status != invoice.StatusDraft {
		t.Errorf("got app %q status %q", inv.AppID, inv.Status)
	}
	if _, err := call(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: own.ID.String()}); codeOf(err) != dash.CodeConflict {
		t.Errorf("second generation for the period: got %v, want CONFLICT", err)
	}
	if _, err := call(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: foreign.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("another app's subscription: got %v, want NOT_FOUND", err)
	}
}

func TestInvoicesTransitions(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})

	fin := mustCall(h, "app_a", invoicesFinalize, IDInput{ID: inv.ID.String()})
	if fin.Status != invoice.StatusPending {
		t.Errorf("after finalize: %q", fin.Status)
	}
	if pending := mustCall(h, "app_a", invoicesPending, struct{}{}); len(pending) != 1 {
		t.Errorf("pending = %d, want 1", len(pending))
	}
	if _, err := call(h, "app_a", invoicesFinalize, IDInput{ID: inv.ID.String()}); codeOf(err) != dash.CodeConflict {
		t.Errorf("finalize twice: got %v, want CONFLICT", err)
	}
	paid := mustCall(h, "app_a", invoicesMarkPaid, InvoiceMarkPaidInput{ID: inv.ID.String(), PaymentRef: "ch_123"})
	if paid.Status != invoice.StatusPaid || paid.PaymentRef != "ch_123" || paid.PaidAt == nil {
		t.Errorf("after mark paid: %+v", paid)
	}
	if _, err := call(h, "app_a", invoicesVoid, InvoiceVoidInput{ID: inv.ID.String(), Reason: "oops"}); codeOf(err) != dash.CodeConflict {
		t.Errorf("void a paid invoice: got %v, want CONFLICT", err)
	}
}

func TestInvoicesVoidNeedsAReason(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})
	if _, err := call(h, "app_a", invoicesVoid, InvoiceVoidInput{ID: inv.ID.String()}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("void without a reason: got %v, want BAD_REQUEST", err)
	}
}

func TestInvoicesDetailScopingAndWireShape(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})

	got := mustCall(h, "app_a", invoicesDetail, IDInput{ID: inv.ID.String()})
	if got.Subscription == nil || got.ExportFormats == nil {
		t.Errorf("detail: %+v", got)
	}
	for _, key := range []string{"id", "status", "currency", "subtotal", "tax_amount", "discount_amount", "total", "line_items", "period_start", "period_end"} {
		if !wireKeys(t, got.Invoice)[key] {
			t.Errorf("invoice on the wire lacks %q", key)
		}
	}
	if _, err := call(h, "app_b", invoicesDetail, IDInput{ID: inv.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("another app reading the invoice: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_a", invoicesExport, InvoiceExportInput{ID: inv.ID.String(), Format: "pdf"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("export with no formatter registered: got %v, want BAD_REQUEST", err)
	}
}

func TestInvoicesListFiltersWithinTheApp(t *testing.T) {
	h := newHarness(t)
	a := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	b := h.subscribe("app_b", "acme", h.activePlan("app_b", "q"))
	mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: a.ID.String()})
	mustCall(h, "app_b", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: b.ID.String()})

	got := mustCall(h, "app_a", invoicesList, InvoicesListInput{})
	if len(got.Items) != 1 || got.Items[0].AppID != "app_a" {
		t.Errorf("app_a list: %+v", got.Items)
	}
	if _, err := call(h, "app_a", invoicesList, InvoicesListInput{Status: "uncollectible"}); codeOf(err) != dash.CodeBadRequest {
		t.Errorf("unknown status: got %v, want BAD_REQUEST", err)
	}
}

func TestInvoicesRefuseTheEmptyScope(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "visible"))
	mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})
	if _, err := call(h, "", invoicesList, InvoicesListInput{}); codeOf(err) != dash.CodePermissionDenied {
		t.Errorf("invoices.list from an empty scope: got %v, want PERMISSION_DENIED (never every app's invoices)", err)
	}
}

func TestInvoicesCommandsRefuseAnotherAppsInvoice(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})

	if _, err := call(h, "app_b", invoicesFinalize, IDInput{ID: inv.ID.String()}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("finalize from another app: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_b", invoicesVoid, InvoiceVoidInput{ID: inv.ID.String(), Reason: "mine now"}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("void from another app: got %v, want NOT_FOUND", err)
	}
	if _, err := call(h, "app_b", invoicesExport, InvoiceExportInput{ID: inv.ID.String(), Format: "pdf"}); codeOf(err) != dash.CodeNotFound {
		t.Errorf("export from another app: got %v, want NOT_FOUND", err)
	}
	stored, err := h.eng.Store().GetInvoice(ctxBackground(), inv.ID)
	if err != nil || stored.Status != invoice.StatusDraft {
		t.Errorf("the invoice must be untouched: %+v, %v", stored, err)
	}
}

func TestInvoicesVoidAndPendingListing(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
	inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})
	mustCall(h, "app_a", invoicesFinalize, IDInput{ID: inv.ID.String()})

	if other := mustCall(h, "app_b", invoicesPending, struct{}{}); len(other) != 0 {
		t.Errorf("another app's pending list: %+v", other)
	}
	voided := mustCall(h, "app_a", invoicesVoid, InvoiceVoidInput{ID: inv.ID.String(), Reason: "  duplicate  "})
	if voided.Status != invoice.StatusVoided {
		t.Errorf("after void: %q", voided.Status)
	}
	if pending := mustCall(h, "app_a", invoicesPending, struct{}{}); len(pending) != 0 {
		t.Errorf("pending after void = %d, want 0", len(pending))
	}
	// A voided period may be billed again.
	if _, err := call(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()}); err != nil {
		t.Errorf("regenerating after a void: %v", err)
	}
}

// invoiceProvider adds SyncInvoice to fakeProvider, which answers only for
// plans.
type invoiceProvider struct {
	fakeProvider
}

func (p *invoiceProvider) Provider() provider.Provider { return p }

func (p *invoiceProvider) SyncInvoice(context.Context, *invoice.Invoice) (string, error) {
	return p.syncID, p.syncErr
}

func TestInvoicesSyncToProvider(t *testing.T) {
	t.Run("no provider configured is unavailable", func(t *testing.T) {
		h := newHarness(t)
		sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "nosync"))
		inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})
		if _, err := call(h, "app_a", invoicesSync, IDInput{ID: inv.ID.String()}); codeOf(err) != dash.CodeUnavailable {
			t.Errorf("got %v, want UNAVAILABLE", err)
		}
	})

	t.Run("a refusal is an answer, not an internal error", func(t *testing.T) {
		h := newHarness(t)
		eng := ledger.New(h.store, ledger.WithPlugin(&invoiceProvider{fakeProvider{syncErr: errors.New("card network says no")}}))
		h.eng = eng
		h.deps = Deps{Engine: func() *ledger.Ledger { return eng }}
		sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "refused"))
		inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})

		got, err := call(h, "app_a", invoicesSync, IDInput{ID: inv.ID.String()})
		if err != nil {
			t.Fatalf("a provider refusal must not be an error, got %v", err)
		}
		if got == nil || got.Success || got.Error != "card network says no" || got.EntityID != inv.ID.String() {
			t.Errorf("got %+v, want an unsuccessful result carrying the provider's message", got)
		}
	})

	t.Run("another app's invoice is not found", func(t *testing.T) {
		h := newHarness(t)
		eng := ledger.New(h.store, ledger.WithPlugin(&invoiceProvider{fakeProvider{syncID: "in_1"}}))
		h.eng = eng
		h.deps = Deps{Engine: func() *ledger.Ledger { return eng }}
		sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "p"))
		inv := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})
		if _, err := call(h, "app_b", invoicesSync, IDInput{ID: inv.ID.String()}); codeOf(err) != dash.CodeNotFound {
			t.Errorf("got %v, want NOT_FOUND", err)
		}
	})
}

// An invoice's line items arrive as [] and never as null, even for a row a
// store hands back with a nil list.
func TestAnInvoiceWithNoLineItemsSendsAnEmptyList(t *testing.T) {
	h := newHarness(t)
	sub := h.subscribe("app_a", "acme", h.activePlan("app_a", "bare-invoice"))
	inv := &invoice.Invoice{
		Entity: types.NewEntity(), ID: id.NewInvoiceID(), TenantID: "acme", SubscriptionID: sub.ID,
		Status: invoice.StatusDraft, Currency: "usd",
		Subtotal: types.USD(0), TaxAmount: types.USD(0), DiscountAmount: types.USD(0), Total: types.USD(0),
		PeriodStart: sub.CurrentPeriodStart, PeriodEnd: sub.CurrentPeriodEnd, AppID: "app_a",
	}
	if err := h.store.CreateInvoice(context.Background(), inv); err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}

	check := func(intent, wire string) {
		t.Helper()
		if strings.Contains(wire, `"line_items":null`) || !strings.Contains(wire, `"line_items":[]`) {
			t.Errorf("%s: want \"line_items\":[] on the wire, got %s", intent, wire)
		}
	}
	check("invoices.detail", wireJSON(t, mustCall(h, "app_a", invoicesDetail, IDInput{ID: inv.ID.String()})))
	check("invoices.list", wireJSON(t, mustCall(h, "app_a", invoicesList, InvoicesListInput{})))
	check("invoices.finalize", wireJSON(t, mustCall(h, "app_a", invoicesFinalize, IDInput{ID: inv.ID.String()})))
}
