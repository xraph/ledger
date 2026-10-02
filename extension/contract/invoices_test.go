package contract

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
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

func TestInvoicesGenerateForANamedPeriod(t *testing.T) {
	h := newHarness(t)
	p := h.activePlan("app_a", "p")
	utc := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	at := func(y int, m time.Month, d int) *time.Time { v := utc(y, m, d); return &v }
	sub := &subscription.Subscription{
		Entity: types.Entity{CreatedAt: utc(2026, 1, 1), UpdatedAt: utc(2026, 1, 1)},
		ID:     id.NewSubscriptionID(), TenantID: "acme", PlanID: p.ID, AppID: "app_a",
		Status:             subscription.StatusActive,
		CurrentPeriodStart: utc(2026, 3, 1), CurrentPeriodEnd: utc(2026, 4, 1),
	}
	if err := h.store.CreateSubscription(ctxBackground(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	subID := sub.ID.String()

	previous := InvoiceGenerateInput{SubscriptionID: subID, PeriodStart: at(2026, 2, 1), PeriodEnd: at(2026, 3, 1)}
	inv := mustCall(h, "app_a", invoicesGenerate, previous)
	if !inv.PeriodStart.Equal(*previous.PeriodStart) || !inv.PeriodEnd.Equal(*previous.PeriodEnd) {
		t.Errorf("invoice period %v to %v, want February", inv.PeriodStart, inv.PeriodEnd)
	}
	if _, err := call(h, "app_a", invoicesGenerate, previous); codeOf(err) != dash.CodeConflict {
		t.Errorf("a second invoice for February: got %v, want CONFLICT", err)
	}

	for name, in := range map[string]InvoiceGenerateInput{
		"only a start":             {SubscriptionID: subID, PeriodStart: at(2026, 2, 1)},
		"only an end":              {SubscriptionID: subID, PeriodEnd: at(2026, 3, 1)},
		"not a period it had":      {SubscriptionID: subID, PeriodStart: at(2026, 2, 15), PeriodEnd: at(2026, 3, 15)},
		"after the current period": {SubscriptionID: subID, PeriodStart: at(2026, 4, 1), PeriodEnd: at(2026, 5, 1)},
	} {
		if _, err := call(h, "app_a", invoicesGenerate, in); codeOf(err) != dash.CodeBadRequest {
			t.Errorf("%s: got %v, want BAD_REQUEST", name, err)
		}
	}

	current := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: subID})
	if !current.PeriodStart.Equal(utc(2026, 3, 1)) {
		t.Errorf("with no period: starts %v, want the current period", current.PeriodStart)
	}
}

// TestInvoicesGenerateBadRequestMessages pins the words the operator reads.
func TestInvoicesGenerateBadRequestMessages(t *testing.T) {
	h := newHarness(t)
	p := h.activePlan("app_a", "p")
	sub := h.subscribe("app_a", "acme", p)
	start, end := sub.CurrentPeriodStart, sub.CurrentPeriodEnd

	_, err := call(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String(), PeriodStart: &start})
	if codeOf(err) != dash.CodeBadRequest || !strings.Contains(err.Error(), "period_start and period_end go together") {
		t.Errorf("only a start: got %v", err)
	}
	_, err = call(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String(), PeriodEnd: &end})
	if codeOf(err) != dash.CodeBadRequest || !strings.Contains(err.Error(), "period_start and period_end go together") {
		t.Errorf("only an end: got %v", err)
	}
	future := end.AddDate(0, 1, 0)
	_, err = call(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String(), PeriodStart: &end, PeriodEnd: &future})
	if codeOf(err) != dash.CodeBadRequest || !strings.Contains(err.Error(), "had no billing period") {
		t.Errorf("a period it never had: got %v", err)
	}
}

// TestInvoicesGenerateTakesThePeriodTheEngineWrote sends periods straight back
// from the engine's own JSON. Periods carry a sub-second time of day, so a
// parse that dropped the fraction would refuse a period the subscription had.
func TestInvoicesGenerateTakesThePeriodTheEngineWrote(t *testing.T) {
	h := newHarness(t)
	p := h.activePlan("app_a", "p")
	t0 := time.Date(2026, 3, 1, 10, 11, 12, 123456789, time.UTC)
	sub := &subscription.Subscription{
		Entity: types.Entity{CreatedAt: t0.AddDate(0, -2, 0), UpdatedAt: t0},
		ID:     id.NewSubscriptionID(), TenantID: "acme", PlanID: p.ID, AppID: "app_a",
		Status:             subscription.StatusActive,
		CurrentPeriodStart: t0, CurrentPeriodEnd: t0.AddDate(0, 1, 0),
	}
	if err := h.store.CreateSubscription(ctxBackground(), sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	subID := sub.ID.String()

	// The current period, as subscriptions.detail writes it.
	detail := mustCall(h, "app_a", subscriptionsDetail, IDInput{ID: subID})
	raw, err := json.Marshal(detail.Subscription)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		Start string `json:"current_period_start"`
		End   string `json:"current_period_end"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(wire.Start, ".123456789") {
		t.Fatalf("current_period_start %q lost its nanoseconds on the wire", wire.Start)
	}
	var in InvoiceGenerateInput
	body := `{"subscription_id":"` + subID + `","period_start":"` + wire.Start + `","period_end":"` + wire.End + `"}`
	if err = json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	cur := mustCall(h, "app_a", invoicesGenerate, in)
	if !cur.PeriodStart.Equal(t0) {
		t.Errorf("current period sent back: starts %v, want %v", cur.PeriodStart, t0)
	}

	// A previous period, taken from an invoice's JSON and sent back.
	prevStart, prevEnd := t0.AddDate(0, -1, 0), t0
	first := mustCall(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: subID, PeriodStart: &prevStart, PeriodEnd: &prevEnd})
	raw, err = json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var invWire struct {
		Start string `json:"period_start"`
		End   string `json:"period_end"`
	}
	if err = json.Unmarshal(raw, &invWire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	body = `{"subscription_id":"` + subID + `","period_start":"` + invWire.Start + `","period_end":"` + invWire.End + `"}`
	if err = json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if _, err := call(h, "app_a", invoicesGenerate, in); codeOf(err) != dash.CodeConflict {
		t.Errorf("the invoice's own period sent back: got %v, want CONFLICT (BAD_REQUEST would mean it was not read exactly)", err)
	}
}

// TestInvoicesGenerateRefusesAPausedPeriod: a resume moves a paused period's
// end, so the period cannot be billed while paused, and the refusal says why.
func TestInvoicesGenerateRefusesAPausedPeriod(t *testing.T) {
	h := newHarness(t)
	p := h.activePlan("app_a", "p")
	sub := h.subscribe("app_a", "acme", p)
	mustCall(h, "app_a", subscriptionsPause, IDInput{ID: sub.ID.String()})
	_, err := call(h, "app_a", invoicesGenerate, InvoiceGenerateInput{SubscriptionID: sub.ID.String()})
	if codeOf(err) != dash.CodeBadRequest || !strings.Contains(err.Error(), "is paused; its period is billed when it ends") {
		t.Errorf("a paused subscription: got %v, want BAD_REQUEST saying its period is billed when it ends", err)
	}
}
