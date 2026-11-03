//go:build stripelive

package stripe

// This file runs the provider against Stripe's real API in test mode. It only
// builds with the stripelive tag and skips unless STRIPE_TEST_SECRET_KEY is
// set:
//
//	STRIPE_TEST_SECRET_KEY=sk_test_... go test -tags stripelive -run Live -v ./provider/stripe/
//
// It refuses any key that isn't a secret test-mode key (sk_test_), and it
// never logs the key. Everything it creates hangs off one new customer, which
// it deletes at the end.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/types"
)

func liveProvider(t *testing.T) *Provider {
	t.Helper()
	return liveProviderWith(t, newMemCustomers(), nil)
}

// liveProviderWith builds a live provider on customers. A non-nil transport
// sits between the provider and Stripe.
func liveProviderWith(t *testing.T, customers CustomerStore, transport http.RoundTripper) *Provider {
	t.Helper()
	key := os.Getenv("STRIPE_TEST_SECRET_KEY")
	if key == "" {
		t.Skip("STRIPE_TEST_SECRET_KEY is not set")
	}
	if !strings.HasPrefix(key, "sk_test_") {
		t.Fatal("STRIPE_TEST_SECRET_KEY must be a test-mode secret key (sk_test_); refusing to run")
	}
	cfg := Config{
		SecretKey:     key,
		WebhookSecret: "whsec_unused_by_this_test",
		Customers:     customers,
		Currency:      "USD",
	}
	if transport != nil {
		cfg.backend = stripego.GetBackendWithConfig(stripego.APIBackend, &stripego.BackendConfig{
			HTTPClient:        &http.Client{Transport: transport},
			MaxNetworkRetries: stripego.Int64(0),
			LeveledLogger:     &stripego.LeveledLogger{Level: stripego.LevelNull},
		})
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// liveInvoice builds a pending ledger invoice whose lines sum to its total.
func liveInvoice(tenantID string, cents ...int64) *invoice.Invoice {
	invID := id.NewInvoiceID()
	inv := &invoice.Invoice{ID: invID, TenantID: tenantID, Status: invoice.StatusPending, Currency: "usd"}
	var total int64
	for i, c := range cents {
		typ := invoice.LineItemBase
		if c < 0 {
			typ = invoice.LineItemCredit
		}
		inv.LineItems = append(inv.LineItems, invoice.LineItem{
			ID: id.NewLineItemID(), InvoiceID: invID, Quantity: 1,
			Description: fmt.Sprintf("ledger live test line %d", i+1),
			Amount:      types.USD(c), Type: typ,
		})
		total += c
	}
	inv.Total = types.USD(total)
	return inv
}

// failSecondItem passes every request through to Stripe except the second
// invoice item create, which it answers with a 400 of its own.
type failSecondItem struct {
	mu    sync.Mutex
	items int
}

func (f *failSecondItem) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost && r.URL.Path == "/v1/invoiceitems" {
		f.mu.Lock()
		f.items++
		n := f.items
		f.mu.Unlock()
		if n == 2 {
			body := `{"error":{"type":"invalid_request_error","message":"refused by the live test"}}`
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}

// pendingItemsFor returns the customer's pending invoice items that carry
// the ledger invoice id invID.
func pendingItemsFor(ctx context.Context, t *testing.T, p *Provider, customerID, invID string) []string {
	t.Helper()
	var ids []string
	list := p.client.V1InvoiceItems.List(ctx, &stripego.InvoiceItemListParams{
		Customer: stripego.String(customerID),
		Pending:  stripego.Bool(true),
	})
	for item, err := range list.All(ctx) {
		if err != nil {
			t.Fatalf("list pending invoice items: %v", err)
		}
		if item.Metadata[invoiceMetadataKey] == invID {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func assertInvoiceGone(ctx context.Context, t *testing.T, p *Provider, stripeInvoiceID string) {
	t.Helper()
	_, err := p.client.V1Invoices.Retrieve(ctx, stripeInvoiceID, nil)
	var serr *stripego.Error
	if !errors.As(err, &serr) || serr.HTTPStatusCode != 404 {
		t.Fatalf("retrieve deleted draft %s: err = %v, want a 404", stripeInvoiceID, err)
	}
}

func TestLiveStripe(t *testing.T) {
	p := liveProvider(t)
	ctx := context.Background()
	tenantID := fmt.Sprintf("ledger-live-%d", time.Now().UnixNano())

	customerID, err := p.EnsureCustomer(ctx, tenantID, "", "Ledger live test")
	if err != nil {
		t.Fatalf("EnsureCustomer: %v", err)
	}
	t.Cleanup(func() {
		if _, delErr := p.client.V1Customers.Delete(context.Background(), customerID, nil); delErr != nil {
			t.Logf("delete test customer %s: %v", customerID, delErr)
		}
	})
	t.Logf("customer %s", customerID)

	t.Run("setup session carries the currency", func(t *testing.T) {
		url, err := p.CreateSetupSession(ctx, tenantID, "https://example.com/ok", "https://example.com/cancel")
		if err != nil {
			t.Fatalf("CreateSetupSession: %v", err)
		}
		if url == "" {
			t.Fatal("CreateSetupSession returned no url")
		}
		var found *stripego.CheckoutSession
		for s, err := range p.client.V1CheckoutSessions.List(ctx, &stripego.CheckoutSessionListParams{
			Customer: stripego.String(customerID),
		}).All(ctx) {
			if err != nil {
				t.Fatalf("list checkout sessions: %v", err)
			}
			found = s
			break
		}
		if found == nil || found.Mode != stripego.CheckoutSessionModeSetup || found.Currency != "usd" {
			t.Fatalf("session = %+v, want a setup session in usd", found)
		}
		t.Logf("setup session %s: mode %s, currency %s", found.ID, found.Mode, found.Currency)
	})

	t.Run("a failed item deletes the draft and its items", func(t *testing.T) {
		// Stripe takes the first line, the transport refuses the second, so
		// the real draft already holds a real item when the provider deletes
		// it.
		flaky := liveProviderWith(t, p.customers, &failSecondItem{})
		inv := liveInvoice(tenantID, 500, 800, -500)
		_, err := flaky.SyncInvoice(ctx, inv)
		if err == nil || !strings.Contains(err.Error(), "refused by the live test") {
			t.Fatalf("SyncInvoice error = %v, want the refused second line", err)
		}
		t.Logf("SyncInvoice failed as expected: %v", err)

		var drafts []string
		for in, err := range p.client.V1Invoices.List(ctx, &stripego.InvoiceListParams{
			Customer: stripego.String(customerID),
		}).All(ctx) {
			if err != nil {
				t.Fatalf("list invoices: %v", err)
			}
			if in.Metadata[invoiceMetadataKey] == inv.ID.String() {
				drafts = append(drafts, in.ID)
			}
		}
		if len(drafts) != 0 {
			t.Fatalf("invoices left for %s: %v, want none", inv.ID, drafts)
		}
		if left := pendingItemsFor(ctx, t, p, customerID, inv.ID.String()); len(left) != 0 {
			t.Fatalf("pending invoice items left: %v, want none", left)
		}
	})

	t.Run("a leftover draft with items is deleted on retry", func(t *testing.T) {
		inv := liveInvoice(tenantID, 500, 800, -500)
		draft, err := p.client.V1Invoices.Create(ctx, &stripego.InvoiceCreateParams{
			Customer:                    stripego.String(customerID),
			Currency:                    stripego.String("usd"),
			AutoAdvance:                 stripego.Bool(false),
			PendingInvoiceItemsBehavior: stripego.String("exclude"),
			Metadata:                    map[string]string{invoiceMetadataKey: inv.ID.String()},
		})
		if err != nil {
			t.Fatalf("create leftover draft: %v", err)
		}
		_, err = p.client.V1InvoiceItems.Create(ctx, &stripego.InvoiceItemCreateParams{
			Customer: stripego.String(customerID),
			Invoice:  stripego.String(draft.ID),
			Amount:   stripego.Int64(500),
			Currency: stripego.String("usd"),
			Metadata: map[string]string{invoiceMetadataKey: inv.ID.String()},
		})
		if err != nil {
			t.Fatalf("add item to leftover draft: %v", err)
		}

		got, err := p.SyncInvoice(ctx, inv)
		if err != nil {
			t.Fatalf("SyncInvoice: %v", err)
		}
		if got == draft.ID {
			t.Fatalf("SyncInvoice reused the leftover draft %s", draft.ID)
		}
		assertInvoiceGone(ctx, t, p, draft.ID)
		if left := pendingItemsFor(ctx, t, p, customerID, inv.ID.String()); len(left) != 0 {
			t.Fatalf("pending invoice items left: %v, want none", left)
		}
		t.Logf("leftover draft %s deleted, fresh invoice %s", draft.ID, got)
	})

	t.Run("credit line, finalize, pay and refund with a caller key", func(t *testing.T) {
		pm, err := p.client.V1PaymentMethods.Attach(ctx, "pm_card_visa", &stripego.PaymentMethodAttachParams{
			Customer: stripego.String(customerID),
		})
		if err != nil {
			t.Fatalf("attach test card: %v", err)
		}

		inv := liveInvoice(tenantID, 1500, 800, -500)
		stripeID, err := p.SyncInvoice(ctx, inv)
		if err != nil {
			t.Fatalf("SyncInvoice: %v", err)
		}
		final, err := p.client.V1Invoices.Retrieve(ctx, stripeID, nil)
		if err != nil {
			t.Fatalf("retrieve invoice: %v", err)
		}
		if final.Status != stripego.InvoiceStatusOpen || final.Total != 1800 || final.Currency != "usd" {
			t.Fatalf("invoice %s is %s for %d %s, want open for 1800 usd", final.ID, final.Status, final.Total, final.Currency)
		}
		var credit bool
		for _, line := range final.Lines.Data {
			if line.Amount == -500 {
				credit = true
			}
		}
		if !credit {
			t.Fatalf("invoice %s has no -500 credit line", final.ID)
		}
		t.Logf("invoice %s finalized: open, total %d, credit line -500", final.ID, final.Total)

		again, err := p.SyncInvoice(ctx, inv)
		if err != nil || again != stripeID {
			t.Fatalf("second SyncInvoice = (%q, %v), want the sent invoice %s back", again, err, stripeID)
		}

		paid, err := p.client.V1Invoices.Pay(ctx, stripeID, &stripego.InvoicePayParams{
			PaymentMethod: stripego.String(pm.ID),
		})
		if err != nil {
			t.Fatalf("pay invoice: %v", err)
		}
		if paid.Status != stripego.InvoiceStatusPaid {
			t.Fatalf("invoice is %s after pay, want paid", paid.Status)
		}

		key := "live-" + inv.ID.String()
		part := types.USD(300)
		for i := range 2 {
			full, refundErr := p.RefundInvoice(ctx, stripeID, &part, key)
			if refundErr != nil {
				t.Fatalf("RefundInvoice attempt %d: %v", i+1, refundErr)
			}
			if full {
				t.Fatalf("RefundInvoice attempt %d reported a full refund for 300 of 1800", i+1)
			}
		}
		payment, err := p.paidPayment(ctx, stripeID)
		if err != nil {
			t.Fatalf("paidPayment: %v", err)
		}
		if payment.Payment.PaymentIntent == nil {
			t.Fatal("payment has no payment intent")
		}
		var refunds []int64
		for r, err := range p.client.V1Refunds.List(ctx, &stripego.RefundListParams{
			PaymentIntent: stripego.String(payment.Payment.PaymentIntent.ID),
		}).All(ctx) {
			if err != nil {
				t.Fatalf("list refunds: %v", err)
			}
			refunds = append(refunds, r.Amount)
		}
		if len(refunds) != 1 || refunds[0] != 300 {
			t.Fatalf("refunds = %v, want one refund of 300 after two calls with one key", refunds)
		}

		full, err := p.RefundInvoice(ctx, stripeID, nil, key+"-rest")
		if err != nil {
			t.Fatalf("refund the rest: %v", err)
		}
		if !full {
			t.Fatal("refunding the rest did not report a full refund")
		}
		t.Logf("refunded 300 twice under one key (one refund), then the rest under a second key (full)")
	})
}
