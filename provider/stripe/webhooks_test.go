package stripe

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87/webhook"

	"github.com/xraph/ledger/provider"
)

// fixture reads a webhook event payload from testdata.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// sign returns a Stripe-Signature header for payload signed with secret now.
func sign(payload []byte, secret string) string {
	return webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  secret,
	}).Header
}

// fixtureTime is the created time of every fixture event.
var fixtureTime = time.Unix(1793750400, 0)

// setupRoutes answers the calls a setup webhook makes for cus_123 and pm_9.
// The customer has no default card yet.
func setupRoutes() map[string]string {
	return map[string]string{
		"GET /v1/customers/cus_123":    `{"id":"cus_123","object":"customer","invoice_settings":{"default_payment_method":null}}`,
		"GET /v1/setup_intents/seti_1": `{"id":"seti_1","object":"setup_intent","customer":"cus_123","payment_method":"pm_9","status":"succeeded"}`,
		"POST /v1/customers/cus_123":   `{"id":"cus_123","object":"customer","invoice_settings":{"default_payment_method":"pm_9"}}`,
	}
}

func TestVerify_AcceptsSignedPayload(t *testing.T) {
	p := newTestProvider(t, newFakeStripe(t, nil), newMemCustomers())
	payload := fixture(t, "invoice_paid.json")

	if err := p.Verify(payload, sign(payload, "whsec_123")); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_RejectsBadSignature(t *testing.T) {
	p := newTestProvider(t, newFakeStripe(t, nil), newMemCustomers())
	payload := fixture(t, "invoice_paid.json")
	good := sign(payload, "whsec_123")

	stale := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   payload,
		Secret:    "whsec_123",
		Timestamp: time.Now().Add(-time.Hour),
	}).Header

	otherTrain := []byte(strings.Replace(string(payload), "2026-10-28.endive", "2025-03-31.basil", 1))

	cases := []struct {
		name    string
		payload []byte
		header  string
	}{
		{"no header", payload, ""},
		{"garbage header", payload, "not-a-signature"},
		{"wrong secret", payload, sign(payload, "whsec_other")},
		{"tampered payload", []byte(strings.Replace(string(payload), "inv_01", "inv_02", 1)), good},
		{"too old", payload, stale},
		{"other API version", otherTrain, sign(otherTrain, "whsec_123")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Verify(tc.payload, tc.header)
			if err == nil {
				t.Fatal("Verify accepted a bad signature")
			}
			for _, secret := range []string{"whsec_123", "sk_test_123"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaks a secret: %v", err)
				}
			}
		})
	}
}

func TestHandleWebhook_InvoicePaid(t *testing.T) {
	cases := []struct {
		fixture   string
		eventType string
	}{
		{"invoice_paid.json", "invoice.paid"},
		{"invoice_payment_failed.json", "invoice.payment_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			f := newFakeStripe(t, nil)
			p := newTestProvider(t, f, newMemCustomers())

			got, err := p.HandleWebhook(context.Background(), fixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("HandleWebhook: %v", err)
			}
			want := &provider.WebhookResult{
				EventType:  tc.eventType,
				ProviderID: "in_1",
				EntityType: "invoice",
				EntityID:   "inv_01",
				Handled:    true,
				Timestamp:  fixtureTime,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("result = %+v, want %+v", got, want)
			}
			if n := f.total(); n != 0 {
				t.Fatalf("Stripe saw %d requests, want 0", n)
			}
		})
	}
}

func TestHandleWebhook_SetupSucceededSetsDefault(t *testing.T) {
	cases := []struct {
		fixture    string
		eventType  string
		providerID string
	}{
		{"setup_intent_succeeded.json", "setup_intent.succeeded", "seti_1"},
		{"checkout_session_completed_setup.json", "checkout.session.completed", "cs_1"},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			f := newFakeStripe(t, setupRoutes())
			p := newTestProvider(t, f, newMemCustomers())

			got, err := p.HandleWebhook(context.Background(), fixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("HandleWebhook: %v", err)
			}
			want := &provider.WebhookResult{
				EventType:  tc.eventType,
				ProviderID: tc.providerID,
				EntityType: "tenant",
				EntityID:   "ten_1",
				Handled:    true,
				Timestamp:  fixtureTime,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("result = %+v, want %+v", got, want)
			}

			updates := f.requests(http.MethodPost, "/v1/customers/cus_123")
			if len(updates) != 1 {
				t.Fatalf("customer updates = %d, want 1", len(updates))
			}
			if pm := updates[0].Form.Get("invoice_settings[default_payment_method]"); pm != "pm_9" {
				t.Fatalf("default_payment_method = %q, want pm_9", pm)
			}
		})
	}
}

func TestHandleWebhook_UnknownEventIgnored(t *testing.T) {
	for _, name := range []string{
		"customer_updated.json",
		"checkout_session_completed_payment.json",
		"invoice_paid_no_ledger_metadata.json",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeStripe(t, nil)
			p := newTestProvider(t, f, newMemCustomers())

			got, err := p.HandleWebhook(context.Background(), fixture(t, name))
			if err != nil {
				t.Fatalf("HandleWebhook: %v", err)
			}
			if got.Handled {
				t.Fatalf("result = %+v, want Handled false", got)
			}
			if got.EventType == "" {
				t.Fatalf("result = %+v, want the event type reported", got)
			}
			if n := f.total(); n != 0 {
				t.Fatalf("Stripe saw %d requests, want 0", n)
			}
		})
	}
}

func TestHandleWebhook_RejectsMalformedPayload(t *testing.T) {
	p := newTestProvider(t, newFakeStripe(t, nil), newMemCustomers())

	if _, err := p.HandleWebhook(context.Background(), []byte("{not json")); err == nil {
		t.Fatal("HandleWebhook accepted malformed JSON")
	}
}

func TestHandleWebhook_ReplayIsSafe(t *testing.T) {
	f := newFakeStripe(t, setupRoutes())
	f.script("GET /v1/customers/cus_123",
		fakeResp{Body: `{"id":"cus_123","object":"customer","invoice_settings":{"default_payment_method":null}}`},
		fakeResp{Body: `{"id":"cus_123","object":"customer","invoice_settings":{"default_payment_method":{"id":"pm_9","object":"payment_method","created":100}}}`},
	)
	p := newTestProvider(t, f, newMemCustomers())
	ctx := context.Background()

	for _, name := range []string{"invoice_paid.json", "setup_intent_succeeded.json"} {
		payload := fixture(t, name)
		first, err := p.HandleWebhook(ctx, payload)
		if err != nil {
			t.Fatalf("%s first delivery: %v", name, err)
		}
		second, err := p.HandleWebhook(ctx, payload)
		if err != nil {
			t.Fatalf("%s replay: %v", name, err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("%s replay result = %+v, first was %+v", name, second, first)
		}
	}

	// The replay finds pm_9 already the default and leaves it alone.
	updates := f.requests(http.MethodPost, "/v1/customers/cus_123")
	if len(updates) != 1 {
		t.Fatalf("customer updates = %d, want 1", len(updates))
	}
}

// setupEvent builds a setup_intent.succeeded event for cus_123 saving pm.
func setupEvent(eventID, pm string) []byte {
	return []byte(`{"id":"` + eventID + `","object":"event","api_version":"2026-10-28.endive","created":1793750400,
		"type":"setup_intent.succeeded","data":{"object":{"id":"seti_` + pm + `","object":"setup_intent",
		"customer":"cus_123","payment_method":"` + pm + `","status":"succeeded","metadata":{"ledger_tenant_id":"ten_1"}}}}`)
}

func TestHandleWebhook_OutOfOrderSetupKeepsNewerCard(t *testing.T) {
	f := newFakeStripe(t, map[string]string{
		"GET /v1/payment_methods/pm_a": `{"id":"pm_a","object":"payment_method","type":"card","created":100}`,
		"GET /v1/payment_methods/pm_b": `{"id":"pm_b","object":"payment_method","type":"card","created":200}`,
		"POST /v1/customers/cus_123":   `{"id":"cus_123","object":"customer"}`,
	})
	// The customer's default as each delivery finds it: none, then A, then B.
	f.script("GET /v1/customers/cus_123",
		fakeResp{Body: `{"id":"cus_123","object":"customer","invoice_settings":{"default_payment_method":null}}`},
		fakeResp{Body: `{"id":"cus_123","object":"customer","invoice_settings":{"default_payment_method":{"id":"pm_a","object":"payment_method","created":100}}}`},
		fakeResp{Body: `{"id":"cus_123","object":"customer","invoice_settings":{"default_payment_method":{"id":"pm_b","object":"payment_method","created":200}}}`},
	)
	p := newTestProvider(t, f, newMemCustomers())
	ctx := context.Background()

	for _, evt := range [][]byte{
		setupEvent("evt_a", "pm_a"),
		setupEvent("evt_b", "pm_b"),
		setupEvent("evt_a", "pm_a"), // late redelivery of A
	} {
		res, err := p.HandleWebhook(ctx, evt)
		if err != nil {
			t.Fatalf("HandleWebhook: %v", err)
		}
		if !res.Handled || res.EntityID != "ten_1" {
			t.Fatalf("result = %+v, want handled for ten_1", res)
		}
	}

	gets := f.requests(http.MethodGet, "/v1/customers/cus_123")
	if len(gets) != 3 || gets[0].Form.Get("expand[0]") != "invoice_settings.default_payment_method" {
		t.Fatalf("customer reads = %+v, want 3 expanding the default card", gets)
	}
	updates := f.requests(http.MethodPost, "/v1/customers/cus_123")
	set := make([]string, 0, len(updates))
	for _, u := range updates {
		set = append(set, u.Form.Get("invoice_settings[default_payment_method]"))
	}
	if !reflect.DeepEqual(set, []string{"pm_a", "pm_b"}) {
		t.Fatalf("default card updates = %q, want [pm_a pm_b] with the late A skipped", set)
	}
}
