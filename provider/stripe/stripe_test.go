package stripe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/xraph/ledger/provider"
)

// memCustomers is an in-memory CustomerStore.
type memCustomers struct {
	mu  sync.Mutex
	ids map[string]string
}

func newMemCustomers() *memCustomers { return &memCustomers{ids: map[string]string{}} }

func (m *memCustomers) CustomerID(_ context.Context, tenantID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ids[tenantID], nil
}

func (m *memCustomers) SetCustomerID(_ context.Context, tenantID, customerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ids[tenantID] = customerID
	return nil
}

// seenRequest is one request the fake Stripe API received.
type seenRequest struct {
	Method         string
	Path           string
	Form           url.Values
	IdempotencyKey string
}

// fakeStripe is an httptest server standing in for api.stripe.com. Routes
// map "METHOD /path" to a JSON body answered with 200.
type fakeStripe struct {
	t      *testing.T
	srv    *httptest.Server
	routes map[string]string

	mu   sync.Mutex
	seen []seenRequest
}

func newFakeStripe(t *testing.T, routes map[string]string) *fakeStripe {
	t.Helper()
	f := &fakeStripe{t: t, routes: routes}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeStripe) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read body: %v", err)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		f.t.Errorf("parse body: %v", err)
	}
	if r.Method == http.MethodGet {
		form = r.URL.Query()
	}
	f.mu.Lock()
	f.seen = append(f.seen, seenRequest{
		Method:         r.Method,
		Path:           r.URL.Path,
		Form:           form,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	f.mu.Unlock()

	resp, ok := f.routes[r.Method+" "+r.URL.Path]
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"no route"}}`)
		return
	}
	_, _ = io.WriteString(w, resp)
}

// requests returns the requests seen for METHOD /path.
func (f *fakeStripe) requests(method, path string) []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []seenRequest
	for _, s := range f.seen {
		if s.Method == method && s.Path == path {
			out = append(out, s)
		}
	}
	return out
}

// all returns every request the fake has seen, in the order it saw them.
func (f *fakeStripe) all() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.seen...)
}

// total returns how many requests the fake has seen.
func (f *fakeStripe) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

func (f *fakeStripe) backend() stripego.Backend {
	return stripego.GetBackendWithConfig(stripego.APIBackend, &stripego.BackendConfig{
		URL:               stripego.String(f.srv.URL),
		HTTPClient:        f.srv.Client(),
		MaxNetworkRetries: stripego.Int64(0),
		LeveledLogger:     &stripego.LeveledLogger{Level: stripego.LevelNull},
	})
}

func newTestProvider(t *testing.T, f *fakeStripe, store CustomerStore) *Provider {
	t.Helper()
	p, err := New(Config{
		SecretKey:     "sk_test_123",
		WebhookSecret: "whsec_123",
		Customers:     store,
		Backend:       f.backend(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestEnsureCustomer_CreatesOnceThenReuses(t *testing.T) {
	f := newFakeStripe(t, map[string]string{
		"POST /v1/customers": `{"id":"cus_123","object":"customer"}`,
	})
	store := newMemCustomers()
	p := newTestProvider(t, f, store)
	ctx := context.Background()

	id, err := p.EnsureCustomer(ctx, "ten_1", "ada@example.com", "Ada")
	if err != nil {
		t.Fatalf("EnsureCustomer: %v", err)
	}
	if id != "cus_123" {
		t.Fatalf("id = %q, want cus_123", id)
	}
	if got, _ := store.CustomerID(ctx, "ten_1"); got != "cus_123" {
		t.Fatalf("stored id = %q, want cus_123", got)
	}

	again, err := p.EnsureCustomer(ctx, "ten_1", "ada@example.com", "Ada")
	if err != nil {
		t.Fatalf("second EnsureCustomer: %v", err)
	}
	if again != "cus_123" {
		t.Fatalf("second id = %q, want cus_123", again)
	}

	creates := f.requests(http.MethodPost, "/v1/customers")
	if len(creates) != 1 {
		t.Fatalf("POST /v1/customers calls = %d, want 1", len(creates))
	}
	c := creates[0]
	if got := c.Form.Get("metadata[ledger_tenant_id]"); got != "ten_1" {
		t.Errorf("metadata[ledger_tenant_id] = %q, want ten_1", got)
	}
	if got := c.Form.Get("email"); got != "ada@example.com" {
		t.Errorf("email = %q", got)
	}
	if got := c.Form.Get("name"); got != "Ada" {
		t.Errorf("name = %q", got)
	}
	if c.IdempotencyKey != "customer:ten_1" {
		t.Errorf("Idempotency-Key = %q, want customer:ten_1", c.IdempotencyKey)
	}
}

func TestCreateSetupSession_ModeSetup(t *testing.T) {
	f := newFakeStripe(t, map[string]string{
		"POST /v1/checkout/sessions": `{"id":"cs_1","object":"checkout.session","url":"https://checkout.stripe.com/c/cs_1"}`,
	})
	store := newMemCustomers()
	_ = store.SetCustomerID(context.Background(), "ten_1", "cus_123")
	p := newTestProvider(t, f, store)

	got, err := p.CreateSetupSession(context.Background(), "ten_1", "https://app.test/ok", "https://app.test/cancel")
	if err != nil {
		t.Fatalf("CreateSetupSession: %v", err)
	}
	if got != "https://checkout.stripe.com/c/cs_1" {
		t.Fatalf("url = %q", got)
	}

	reqs := f.requests(http.MethodPost, "/v1/checkout/sessions")
	if len(reqs) != 1 {
		t.Fatalf("POST /v1/checkout/sessions calls = %d, want 1", len(reqs))
	}
	form := reqs[0].Form
	want := map[string]string{
		"mode":                            "setup",
		"currency":                        "usd",
		"customer":                        "cus_123",
		"allowed_payment_method_types[0]": "card",
		"metadata[ledger_tenant_id]":      "ten_1",
		"success_url":                     "https://app.test/ok",
		"cancel_url":                      "https://app.test/cancel",
	}
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestCreateSetupSession_NoCustomer(t *testing.T) {
	f := newFakeStripe(t, nil)
	p := newTestProvider(t, f, newMemCustomers())

	_, err := p.CreateSetupSession(context.Background(), "ten_1", "https://app.test/ok", "https://app.test/cancel")
	if !errors.Is(err, ErrNoCustomer) {
		t.Fatalf("err = %v, want ErrNoCustomer", err)
	}
}

func TestListPaymentMethods_MarksDefault(t *testing.T) {
	f := newFakeStripe(t, map[string]string{
		"GET /v1/customers/cus_123": `{"id":"cus_123","object":"customer","invoice_settings":{"default_payment_method":"pm_2"}}`,
		"GET /v1/customers/cus_123/payment_methods": `{"object":"list","url":"/v1/customers/cus_123/payment_methods","has_more":false,"data":[
			{"id":"pm_1","object":"payment_method","type":"card","card":{"brand":"visa","last4":"4242","exp_month":4,"exp_year":2030}},
			{"id":"pm_2","object":"payment_method","type":"card","card":{"brand":"mastercard","last4":"4444","exp_month":12,"exp_year":2031}}
		]}`,
	})
	store := newMemCustomers()
	_ = store.SetCustomerID(context.Background(), "ten_1", "cus_123")
	p := newTestProvider(t, f, store)

	got, err := p.ListPaymentMethods(context.Background(), "ten_1")
	if err != nil {
		t.Fatalf("ListPaymentMethods: %v", err)
	}
	want := []provider.PaymentMethod{
		{ID: "pm_1", Type: "card", Last4: "4242", Brand: "visa", ExpiryMonth: 4, ExpiryYear: 2030, ProviderName: "stripe", ProviderID: "pm_1"},
		{ID: "pm_2", Type: "card", Last4: "4444", Brand: "mastercard", ExpiryMonth: 12, ExpiryYear: 2031, IsDefault: true, ProviderName: "stripe", ProviderID: "pm_2"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d methods, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("method %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	lists := f.requests(http.MethodGet, "/v1/customers/cus_123/payment_methods")
	if len(lists) != 1 || lists[0].Form.Get("type") != "card" {
		t.Errorf("payment_methods list requests = %+v, want one with type=card", lists)
	}
}

func TestListPaymentMethods_NoCustomer(t *testing.T) {
	f := newFakeStripe(t, nil)
	p := newTestProvider(t, f, newMemCustomers())

	got, err := p.ListPaymentMethods(context.Background(), "ten_1")
	if err != nil {
		t.Fatalf("ListPaymentMethods: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("Stripe saw %d requests, want 0 without a customer", n)
	}
}

func TestNew_RequiresKeysAndStore(t *testing.T) {
	store := newMemCustomers()
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no secret key", Config{WebhookSecret: "whsec_1", Customers: store}},
		{"no webhook secret", Config{SecretKey: "sk_test_1", Customers: store}},
		{"no customer store", Config{SecretKey: "sk_test_1", WebhookSecret: "whsec_1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(tc.cfg)
			if err == nil {
				t.Fatalf("New returned %v, want error", p)
			}
			if strings.Contains(err.Error(), "sk_test_1") {
				t.Fatalf("error leaks the secret key: %v", err)
			}
		})
	}

	p, err := New(Config{SecretKey: "sk_test_1", WebhookSecret: "whsec_1", Customers: store})
	if err != nil {
		t.Fatalf("New with full config: %v", err)
	}
	if p.Name() != "stripe" {
		t.Fatalf("Name = %q, want stripe", p.Name())
	}
}

func TestStripeErrorsDoNotLeakKey(t *testing.T) {
	f := newFakeStripe(t, nil) // every route 404s
	p := newTestProvider(t, f, newMemCustomers())

	_, err := p.EnsureCustomer(context.Background(), "ten_1", "ada@example.com", "Ada")
	if err == nil {
		t.Fatal("expected an error from a failing Stripe API")
	}
	if strings.Contains(err.Error(), "sk_test_123") {
		t.Fatalf("error leaks the secret key: %v", err)
	}
}

func TestPlugin_ExposesProvider(t *testing.T) {
	f := newFakeStripe(t, nil)
	p := newTestProvider(t, f, newMemCustomers())

	pl := NewPlugin(p)
	if pl.Name() != "stripe" {
		t.Fatalf("plugin Name = %q, want stripe", pl.Name())
	}
	if pl.Provider() != provider.Provider(p) {
		t.Fatal("plugin Provider() is not the wrapped provider")
	}
}

func TestUnsupportedMethods(t *testing.T) {
	f := newFakeStripe(t, nil)
	p := newTestProvider(t, f, newMemCustomers())
	ctx := context.Background()

	if id, err := p.SyncPlan(ctx, nil); id != "" || err != nil {
		t.Errorf("SyncPlan = (%q, %v), want (\"\", nil)", id, err)
	}
	if id, err := p.SyncFeature(ctx, nil); id != "" || err != nil {
		t.Errorf("SyncFeature = (%q, %v), want (\"\", nil)", id, err)
	}
	if _, err := p.ImportPlan(ctx, "x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ImportPlan err = %v, want ErrUnsupported", err)
	}
	if _, err := p.ImportFeature(ctx, "x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ImportFeature err = %v, want ErrUnsupported", err)
	}
	if _, err := p.ImportSubscription(ctx, "x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ImportSubscription err = %v, want ErrUnsupported", err)
	}
	if _, err := p.ImportInvoice(ctx, "x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ImportInvoice err = %v, want ErrUnsupported", err)
	}
}

func TestCreateSetupSession_CurrencyFromConfig(t *testing.T) {
	f := newFakeStripe(t, map[string]string{
		"POST /v1/checkout/sessions": `{"id":"cs_1","object":"checkout.session","url":"https://checkout.stripe.com/c/cs_1"}`,
	})
	store := newMemCustomers()
	_ = store.SetCustomerID(context.Background(), "ten_1", "cus_123")
	p, err := New(Config{
		SecretKey:     "sk_test_123",
		WebhookSecret: "whsec_123",
		Customers:     store,
		Currency:      "EUR",
		Backend:       f.backend(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := p.CreateSetupSession(context.Background(), "ten_1", "https://app.test/ok", "https://app.test/cancel"); err != nil {
		t.Fatalf("CreateSetupSession: %v", err)
	}
	reqs := f.requests(http.MethodPost, "/v1/checkout/sessions")
	if len(reqs) != 1 || reqs[0].Form.Get("currency") != "eur" {
		t.Fatalf("checkout requests = %+v, want one with currency=eur", reqs)
	}
}

func TestListPaymentMethods_NoDefault(t *testing.T) {
	f := newFakeStripe(t, map[string]string{
		"GET /v1/customers/cus_123": `{"id":"cus_123","object":"customer","invoice_settings":null}`,
		"GET /v1/customers/cus_123/payment_methods": `{"object":"list","url":"/v1/customers/cus_123/payment_methods","has_more":false,"data":[
			{"id":"pm_1","object":"payment_method","type":"card","card":{"brand":"visa","last4":"4242","exp_month":4,"exp_year":2030}}
		]}`,
	})
	store := newMemCustomers()
	_ = store.SetCustomerID(context.Background(), "ten_1", "cus_123")
	p := newTestProvider(t, f, store)

	got, err := p.ListPaymentMethods(context.Background(), "ten_1")
	if err != nil {
		t.Fatalf("ListPaymentMethods: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d methods, want 1", len(got))
	}
	if got[0].IsDefault {
		t.Fatalf("method %+v marked default with no invoice default set", got[0])
	}
}

func TestEmptyTenantRejected(t *testing.T) {
	f := newFakeStripe(t, nil)
	p := newTestProvider(t, f, newMemCustomers())
	ctx := context.Background()

	if _, err := p.EnsureCustomer(ctx, "", "ada@example.com", "Ada"); !errors.Is(err, ErrMissingTenant) {
		t.Errorf("EnsureCustomer err = %v, want ErrMissingTenant", err)
	}
	if _, err := p.CreateSetupSession(ctx, "", "https://app.test/ok", "https://app.test/cancel"); !errors.Is(err, ErrMissingTenant) {
		t.Errorf("CreateSetupSession err = %v, want ErrMissingTenant", err)
	}
	if _, err := p.ListPaymentMethods(ctx, ""); !errors.Is(err, ErrMissingTenant) {
		t.Errorf("ListPaymentMethods err = %v, want ErrMissingTenant", err)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("Stripe saw %d requests, want 0", n)
	}
}

// failingCustomers is a CustomerStore whose reads or writes fail.
type failingCustomers struct {
	getErr error
	setErr error
}

func (s failingCustomers) CustomerID(context.Context, string) (string, error) {
	return "", s.getErr
}

func (s failingCustomers) SetCustomerID(context.Context, string, string) error {
	return s.setErr
}

func TestCustomerStoreErrorsPropagate(t *testing.T) {
	ctx := context.Background()

	t.Run("CustomerID fails", func(t *testing.T) {
		errGet := errors.New("store down")
		f := newFakeStripe(t, nil)
		p := newTestProvider(t, f, failingCustomers{getErr: errGet})

		if _, err := p.EnsureCustomer(ctx, "ten_1", "ada@example.com", "Ada"); !errors.Is(err, errGet) {
			t.Errorf("EnsureCustomer err = %v, want %v", err, errGet)
		}
		if _, err := p.CreateSetupSession(ctx, "ten_1", "https://app.test/ok", "https://app.test/cancel"); !errors.Is(err, errGet) {
			t.Errorf("CreateSetupSession err = %v, want %v", err, errGet)
		}
		if _, err := p.ListPaymentMethods(ctx, "ten_1"); !errors.Is(err, errGet) {
			t.Errorf("ListPaymentMethods err = %v, want %v", err, errGet)
		}
		if n := f.total(); n != 0 {
			t.Fatalf("Stripe saw %d requests, want 0", n)
		}
	})

	t.Run("SetCustomerID fails", func(t *testing.T) {
		errSet := errors.New("store read-only")
		f := newFakeStripe(t, map[string]string{
			"POST /v1/customers": `{"id":"cus_123","object":"customer"}`,
		})
		p := newTestProvider(t, f, failingCustomers{setErr: errSet})

		if _, err := p.EnsureCustomer(ctx, "ten_1", "ada@example.com", "Ada"); !errors.Is(err, errSet) {
			t.Errorf("EnsureCustomer err = %v, want %v", err, errSet)
		}
	})
}
