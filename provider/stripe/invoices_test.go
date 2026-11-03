package stripe

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// invoiceRoutes answers the calls SyncInvoice makes for Stripe invoice in_1.
func invoiceRoutes() map[string]string {
	return map[string]string{
		"POST /v1/invoices":               `{"id":"in_1","object":"invoice","status":"draft"}`,
		"POST /v1/invoiceitems":           `{"id":"ii_1","object":"invoiceitem"}`,
		"POST /v1/invoices/in_1/finalize": `{"id":"in_1","object":"invoice","status":"open"}`,
	}
}

// testInvoice builds a ledger invoice for tenant ten_1: base 500, overage
// 800 and a 500 credit, so the total is 800.
func testInvoice() *invoice.Invoice {
	invID := id.NewInvoiceID()
	line := func(typ invoice.LineItemType, desc string, cents int64) invoice.LineItem {
		return invoice.LineItem{
			ID:          id.NewLineItemID(),
			InvoiceID:   invID,
			Description: desc,
			Quantity:    1,
			Amount:      types.USD(cents),
			Type:        typ,
		}
	}
	return &invoice.Invoice{
		ID:           invID,
		TenantID:     "ten_1",
		Currency:     "usd",
		Subtotal:     types.USD(1300),
		CreditAmount: types.USD(500),
		Total:        types.USD(800),
		LineItems: []invoice.LineItem{
			line(invoice.LineItemBase, "Pro plan", 500),
			line(invoice.LineItemOverage, "API calls over quota", 800),
			line(invoice.LineItemCredit, "Included API credit", -500),
		},
	}
}

func providerWithCustomer(t *testing.T, f *fakeStripe) *Provider {
	t.Helper()
	store := newMemCustomers()
	_ = store.SetCustomerID(context.Background(), "ten_1", "cus_123")
	return newTestProvider(t, f, store)
}

func TestSyncInvoice_LinesAndTotalsMatch(t *testing.T) {
	f := newFakeStripe(t, invoiceRoutes())
	p := providerWithCustomer(t, f)
	inv := testInvoice()

	got, err := p.SyncInvoice(context.Background(), inv)
	if err != nil {
		t.Fatalf("SyncInvoice: %v", err)
	}
	if got != "in_1" {
		t.Fatalf("SyncInvoice = %q, want in_1", got)
	}

	// The draft invoice comes first: in this API version pending items are
	// not pulled into a new invoice, so each item names the invoice.
	seen := f.all()
	if len(seen) != 5 {
		t.Fatalf("Stripe saw %d requests, want 5: %+v", len(seen), seen)
	}
	if seen[0].Method != http.MethodPost || seen[0].Path != "/v1/invoices" {
		t.Fatalf("first request = %s %s, want POST /v1/invoices", seen[0].Method, seen[0].Path)
	}
	if last := seen[4]; last.Path != "/v1/invoices/in_1/finalize" {
		t.Fatalf("last request = %s %s, want finalize", last.Method, last.Path)
	}

	draft := seen[0].Form
	wantDraft := map[string]string{
		"customer":                       "cus_123",
		"currency":                       "usd",
		"collection_method":              "charge_automatically",
		"auto_advance":                   "false",
		"pending_invoice_items_behavior": "exclude",
		"metadata[ledger_invoice_id]":    inv.ID.String(),
	}
	for k, v := range wantDraft {
		if got := draft.Get(k); got != v {
			t.Errorf("invoice %s = %q, want %q", k, got, v)
		}
	}

	items := f.requests(http.MethodPost, "/v1/invoiceitems")
	if len(items) != len(inv.LineItems) {
		t.Fatalf("invoice items = %d, want %d", len(items), len(inv.LineItems))
	}
	var sum int64
	for i, it := range items {
		li := inv.LineItems[i]
		amount, err := strconv.ParseInt(it.Form.Get("amount"), 10, 64)
		if err != nil {
			t.Fatalf("item %d amount %q: %v", i, it.Form.Get("amount"), err)
		}
		sum += amount
		if amount != li.Amount.Amount {
			t.Errorf("item %d amount = %d, want %d", i, amount, li.Amount.Amount)
		}
		want := map[string]string{
			"customer":                      "cus_123",
			"invoice":                       "in_1",
			"currency":                      "usd",
			"description":                   li.Description,
			"metadata[ledger_invoice_id]":   inv.ID.String(),
			"metadata[ledger_line_item_id]": li.ID.String(),
		}
		for k, v := range want {
			if got := it.Form.Get(k); got != v {
				t.Errorf("item %d %s = %q, want %q", i, k, got, v)
			}
		}
	}
	if sum != inv.Total.Amount {
		t.Fatalf("Stripe items sum to %d, want invoice total %d", sum, inv.Total.Amount)
	}

	if got := seen[4].Form.Get("auto_advance"); got != "true" {
		t.Errorf("finalize auto_advance = %q, want true", got)
	}
}

func TestSyncInvoice_IdempotentRetry(t *testing.T) {
	f := newFakeStripe(t, invoiceRoutes())
	p := providerWithCustomer(t, f)
	inv := testInvoice()
	ctx := context.Background()

	if _, err := p.SyncInvoice(ctx, inv); err != nil {
		t.Fatalf("first SyncInvoice: %v", err)
	}
	first := f.all()
	if _, err := p.SyncInvoice(ctx, inv); err != nil {
		t.Fatalf("second SyncInvoice: %v", err)
	}
	second := f.all()[len(first):]

	if len(second) != len(first) {
		t.Fatalf("retry made %d requests, first call made %d", len(second), len(first))
	}

	// The draft carries no key of ours, so a retry after a deleted draft
	// cannot replay it. stripe-go still sends a fresh key per call.
	if k := first[0].IdempotencyKey; k == "" || strings.HasPrefix(k, "invoice:") {
		t.Errorf("draft key = %q, want stripe-go's own", k)
	}
	if first[0].IdempotencyKey == second[0].IdempotencyKey {
		t.Errorf("both drafts sent key %q, want a fresh one per call", first[0].IdempotencyKey)
	}

	// Items and finalize are keyed on the draft they belong to; the fake
	// hands back in_1 both times, so the keys match.
	prefix := "invoice:" + inv.ID.String() + ":in_1:"
	want := []string{
		prefix + inv.LineItems[0].ID.String(),
		prefix + inv.LineItems[1].ID.String(),
		prefix + inv.LineItems[2].ID.String(),
		prefix + "finalize",
	}
	for i := 1; i < len(first); i++ {
		if first[i].IdempotencyKey != want[i-1] {
			t.Errorf("request %d key = %q, want %q", i, first[i].IdempotencyKey, want[i-1])
		}
		if second[i].IdempotencyKey != first[i].IdempotencyKey {
			t.Errorf("retry request %d key = %q, first call sent %q", i, second[i].IdempotencyKey, first[i].IdempotencyKey)
		}
	}
	for i := range first {
		if second[i].Form.Encode() != first[i].Form.Encode() {
			t.Errorf("retry request %d params differ: %q vs %q", i, second[i].Form.Encode(), first[i].Form.Encode())
		}
	}
}

func TestSyncInvoice_FailedLineDeletesDraftAndRetryStartsFresh(t *testing.T) {
	f := newFakeStripe(t, map[string]string{
		"DELETE /v1/invoices/in_1":        `{"id":"in_1","object":"invoice","deleted":true}`,
		"POST /v1/invoices/in_2/finalize": `{"id":"in_2","object":"invoice","status":"open"}`,
	})
	f.script("POST /v1/invoices",
		fakeResp{Body: `{"id":"in_1","object":"invoice","status":"draft"}`},
		fakeResp{Body: `{"id":"in_2","object":"invoice","status":"draft"}`},
	)
	f.script("POST /v1/invoiceitems",
		fakeResp{Body: `{"id":"ii_1","object":"invoiceitem"}`},
		fakeResp{Status: http.StatusInternalServerError, Body: `{"error":{"type":"api_error","message":"boom"}}`},
		fakeResp{Body: `{"id":"ii_2","object":"invoiceitem"}`},
	)
	p := providerWithCustomer(t, f)
	inv := testInvoice()
	ctx := context.Background()

	if _, err := p.SyncInvoice(ctx, inv); err == nil {
		t.Fatal("SyncInvoice succeeded although a line failed")
	}
	drafts := f.requests(http.MethodPost, "/v1/invoices")
	if len(drafts) != 1 || drafts[0].Form.Get("auto_advance") != "false" {
		t.Fatalf("draft requests = %+v, want one with auto_advance=false", drafts)
	}
	if n := len(f.requests(http.MethodDelete, "/v1/invoices/in_1")); n != 1 {
		t.Fatalf("draft in_1 deleted %d times, want 1", n)
	}
	if n := len(f.requests(http.MethodPost, "/v1/invoices/in_1/finalize")); n != 0 {
		t.Fatalf("partial draft in_1 finalized %d times, want 0", n)
	}

	before := len(f.all())
	got, err := p.SyncInvoice(ctx, inv)
	if err != nil {
		t.Fatalf("retry SyncInvoice: %v", err)
	}
	if got != "in_2" {
		t.Fatalf("retry SyncInvoice = %q, want the fresh draft in_2", got)
	}
	retry := f.all()[before:]
	if retry[0].Path != "/v1/invoices" || retry[0].IdempotencyKey == drafts[0].IdempotencyKey {
		t.Fatalf("retry draft = %+v, want a new draft with a new key", retry[0])
	}
	var sum int64
	for _, r := range retry {
		if r.Path != "/v1/invoiceitems" {
			continue
		}
		if r.Form.Get("invoice") != "in_2" {
			t.Errorf("retry item on invoice %q, want in_2", r.Form.Get("invoice"))
		}
		if !strings.Contains(r.IdempotencyKey, ":in_2:") {
			t.Errorf("retry item key = %q, want it keyed on in_2", r.IdempotencyKey)
		}
		n, _ := strconv.ParseInt(r.Form.Get("amount"), 10, 64)
		sum += n
	}
	if sum != inv.Total.Amount {
		t.Fatalf("retry items sum to %d, want %d", sum, inv.Total.Amount)
	}
	if n := len(f.requests(http.MethodPost, "/v1/invoices/in_2/finalize")); n != 1 {
		t.Fatalf("in_2 finalized %d times, want 1", n)
	}
}

func TestSyncInvoice_NegativeTotalRejected(t *testing.T) {
	f := newFakeStripe(t, invoiceRoutes())
	p := providerWithCustomer(t, f)
	inv := testInvoice()
	inv.Total = types.USD(-100)

	if got, err := p.SyncInvoice(context.Background(), inv); err == nil {
		t.Fatalf("SyncInvoice = (%q, nil), want an error for a negative total", got)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("Stripe saw %d requests, want 0", n)
	}
}

func TestSyncInvoice_ZeroTotalSkipped(t *testing.T) {
	f := newFakeStripe(t, invoiceRoutes())
	p := providerWithCustomer(t, f)
	inv := testInvoice()
	inv.LineItems[1].Amount = types.USD(0)
	inv.Subtotal = types.USD(500)
	inv.Total = types.USD(0)

	got, err := p.SyncInvoice(context.Background(), inv)
	if err != nil || got != "" {
		t.Fatalf("SyncInvoice = (%q, %v), want (\"\", nil)", got, err)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("Stripe saw %d requests, want 0 for a zero-total invoice", n)
	}
}

func TestSyncInvoice_NoCustomer(t *testing.T) {
	f := newFakeStripe(t, invoiceRoutes())
	p := newTestProvider(t, f, newMemCustomers())

	if _, err := p.SyncInvoice(context.Background(), testInvoice()); !errors.Is(err, ErrNoCustomer) {
		t.Fatalf("err = %v, want ErrNoCustomer", err)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("Stripe saw %d requests, want 0", n)
	}
}

func TestSyncInvoice_LinesMustSumToTotal(t *testing.T) {
	f := newFakeStripe(t, invoiceRoutes())
	p := providerWithCustomer(t, f)
	inv := testInvoice()
	inv.Total = types.USD(900)

	_, err := p.SyncInvoice(context.Background(), inv)
	if err == nil || !strings.Contains(err.Error(), "sum") {
		t.Fatalf("err = %v, want a lines-do-not-sum error", err)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("Stripe saw %d requests, want 0 for a mismatched invoice", n)
	}
}

func TestSyncSubscription_EnsuresCustomer(t *testing.T) {
	f := newFakeStripe(t, map[string]string{
		"POST /v1/customers": `{"id":"cus_123","object":"customer"}`,
	})
	store := newMemCustomers()
	p := newTestProvider(t, f, store)

	got, err := p.SyncSubscription(context.Background(), &subscription.Subscription{TenantID: "ten_1"})
	if err != nil {
		t.Fatalf("SyncSubscription: %v", err)
	}
	if got != "cus_123" {
		t.Fatalf("SyncSubscription = %q, want cus_123", got)
	}
	if id, _ := store.CustomerID(context.Background(), "ten_1"); id != "cus_123" {
		t.Fatalf("stored customer = %q, want cus_123", id)
	}
}

// refundRoutes answers RefundInvoice for Stripe invoice in_1, paid 800 by
// pi_1 whose charge had prior refunded before this refund. The refund is for
// amount and leaves the charge with refunded in total.
func refundRoutes(prior, amount, refunded int64) map[string]string {
	return map[string]string{
		"GET /v1/invoice_payments": `{"object":"list","url":"/v1/invoice_payments","has_more":false,"data":[
			{"id":"inpay_1","object":"invoice_payment","amount_paid":800,"currency":"usd","status":"paid","is_default":true,
			 "payment":{"type":"payment_intent","payment_intent":{"id":"pi_1","object":"payment_intent",
			  "latest_charge":{"id":"ch_1","object":"charge","amount":800,"amount_refunded":` + strconv.FormatInt(prior, 10) + `,"currency":"usd"}}}}
		]}`,
		"POST /v1/refunds": `{"id":"re_1","object":"refund","amount":` + strconv.FormatInt(amount, 10) +
			`,"payment_intent":"pi_1","charge":{"id":"ch_1","object":"charge","amount":800,"amount_refunded":` +
			strconv.FormatInt(refunded, 10) + `,"currency":"usd"}}`,
	}
}

func TestRefundInvoice_FullAndPartial(t *testing.T) {
	ctx := context.Background()

	t.Run("nil amount refunds the whole payment", func(t *testing.T) {
		f := newFakeStripe(t, refundRoutes(0, 800, 800))
		p := providerWithCustomer(t, f)

		full, err := p.RefundInvoice(ctx, "in_1", nil)
		if err != nil {
			t.Fatalf("RefundInvoice: %v", err)
		}
		if !full {
			t.Fatal("full = false, want true")
		}
		lists := f.requests(http.MethodGet, "/v1/invoice_payments")
		if len(lists) != 1 || lists[0].Form.Get("invoice") != "in_1" || lists[0].Form.Get("status") != "paid" {
			t.Fatalf("invoice_payments lists = %+v, want one for in_1 with status=paid", lists)
		}
		got := []string{lists[0].Form.Get("expand[0]"), lists[0].Form.Get("expand[1]")}
		if !reflect.DeepEqual(got, []string{"data.payment.payment_intent.latest_charge", "data.payment.charge"}) {
			t.Errorf("invoice_payments expand = %q, want the charge expanded", got)
		}
		refunds := f.requests(http.MethodPost, "/v1/refunds")
		if len(refunds) != 1 {
			t.Fatalf("refunds = %d, want 1", len(refunds))
		}
		r := refunds[0]
		if got := r.Form.Get("payment_intent"); got != "pi_1" {
			t.Errorf("payment_intent = %q, want pi_1", got)
		}
		if r.Form.Has("amount") {
			t.Errorf("amount = %q, want none for a full refund", r.Form.Get("amount"))
		}
		if r.IdempotencyKey != "refund:in_1:0:full" {
			t.Errorf("Idempotency-Key = %q, want refund:in_1:0:full", r.IdempotencyKey)
		}
	})

	t.Run("partial amount", func(t *testing.T) {
		f := newFakeStripe(t, refundRoutes(0, 100, 100))
		p := providerWithCustomer(t, f)
		amount := types.USD(100)

		full, err := p.RefundInvoice(ctx, "in_1", &amount)
		if err != nil {
			t.Fatalf("RefundInvoice: %v", err)
		}
		if full {
			t.Fatal("full = true, want false for 100 of 800")
		}
		refunds := f.requests(http.MethodPost, "/v1/refunds")
		if len(refunds) != 1 {
			t.Fatalf("refunds = %d, want 1", len(refunds))
		}
		r := refunds[0]
		if got := r.Form.Get("amount"); got != "100" {
			t.Errorf("amount = %q, want 100", got)
		}
		if r.IdempotencyKey != "refund:in_1:0:100" {
			t.Errorf("Idempotency-Key = %q, want refund:in_1:0:100", r.IdempotencyKey)
		}
	})

	t.Run("partial refunds that reach the amount paid are full", func(t *testing.T) {
		f := newFakeStripe(t, refundRoutes(100, 700, 800))
		p := providerWithCustomer(t, f)
		amount := types.USD(700)

		full, err := p.RefundInvoice(ctx, "in_1", &amount)
		if err != nil {
			t.Fatalf("RefundInvoice: %v", err)
		}
		if !full {
			t.Fatal("full = false, want true once the refunded total reaches 800")
		}
		if r := f.requests(http.MethodPost, "/v1/refunds"); len(r) != 1 || r[0].IdempotencyKey != "refund:in_1:100:700" {
			t.Fatalf("refunds = %+v, want one keyed refund:in_1:100:700", r)
		}
	})

	t.Run("two equal partial refunds get different keys", func(t *testing.T) {
		f := newFakeStripe(t, refundRoutes(0, 100, 100))
		first := refundRoutes(0, 100, 100)["GET /v1/invoice_payments"]
		second := refundRoutes(100, 100, 200)["GET /v1/invoice_payments"]
		f.script("GET /v1/invoice_payments", fakeResp{Body: first}, fakeResp{Body: second})
		p := providerWithCustomer(t, f)
		amount := types.USD(100)

		for range 2 {
			if _, err := p.RefundInvoice(ctx, "in_1", &amount); err != nil {
				t.Fatalf("RefundInvoice: %v", err)
			}
		}
		r := f.requests(http.MethodPost, "/v1/refunds")
		if len(r) != 2 || r[0].IdempotencyKey != "refund:in_1:0:100" || r[1].IdempotencyKey != "refund:in_1:100:100" {
			t.Fatalf("refund keys = %+v, want refund:in_1:0:100 then refund:in_1:100:100", r)
		}
	})

	t.Run("more than one paid payment", func(t *testing.T) {
		f := newFakeStripe(t, map[string]string{
			"GET /v1/invoice_payments": `{"object":"list","url":"/v1/invoice_payments","has_more":false,"data":[
				{"id":"inpay_1","object":"invoice_payment","amount_paid":400,"currency":"usd","status":"paid",
				 "payment":{"type":"payment_intent","payment_intent":{"id":"pi_1","object":"payment_intent"}}},
				{"id":"inpay_2","object":"invoice_payment","amount_paid":400,"currency":"usd","status":"paid",
				 "payment":{"type":"payment_intent","payment_intent":{"id":"pi_2","object":"payment_intent"}}}
			]}`,
		})
		p := providerWithCustomer(t, f)

		_, err := p.RefundInvoice(ctx, "in_1", nil)
		if err == nil || !strings.Contains(err.Error(), "more than one paid payment") {
			t.Fatalf("err = %v, want a more-than-one-payment error", err)
		}
		if n := len(f.requests(http.MethodPost, "/v1/refunds")); n != 0 {
			t.Fatalf("refunds = %d, want 0", n)
		}
	})

	t.Run("wrong currency", func(t *testing.T) {
		f := newFakeStripe(t, refundRoutes(0, 100, 100))
		p := providerWithCustomer(t, f)
		amount := types.EUR(100)

		if _, err := p.RefundInvoice(ctx, "in_1", &amount); err == nil {
			t.Fatal("RefundInvoice in eur against a usd payment: want error")
		}
		if n := len(f.requests(http.MethodPost, "/v1/refunds")); n != 0 {
			t.Fatalf("refunds = %d, want 0", n)
		}
	})

	t.Run("unpaid invoice", func(t *testing.T) {
		f := newFakeStripe(t, map[string]string{
			"GET /v1/invoice_payments": `{"object":"list","url":"/v1/invoice_payments","has_more":false,"data":[]}`,
		})
		p := providerWithCustomer(t, f)

		if _, err := p.RefundInvoice(ctx, "in_1", nil); !errors.Is(err, ErrNotPaid) {
			t.Fatalf("err = %v, want ErrNotPaid", err)
		}
	})
}
