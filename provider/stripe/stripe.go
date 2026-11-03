// Package stripe is a Stripe payment provider for Ledger.
//
// Ledger computes plans, prices and invoices; Stripe only collects. This
// provider keeps no plans, prices or subscriptions on the Stripe side. It
// holds one Stripe customer per tenant and the cards saved to it, captured
// through Checkout in setup mode.
package stripe

import (
	"context"
	"errors"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
)

// providerName is the identifier this provider reports from Name.
const providerName = "stripe"

// tenantMetadataKey is the Stripe metadata key that ties a Stripe object to
// the Ledger tenant it belongs to.
const tenantMetadataKey = "ledger_tenant_id"

var (
	// ErrUnsupported is returned by the Import methods: Ledger owns plans,
	// features, subscriptions and invoices, so there is nothing to pull back
	// from Stripe.
	ErrUnsupported = errors.New("stripe: operation not supported")

	// ErrNoCustomer is returned when a tenant has no Stripe customer yet.
	// Call EnsureCustomer first.
	ErrNoCustomer = errors.New("stripe: tenant has no stripe customer")

	// ErrMissingTenant is returned when a call is made without a tenant id.
	ErrMissingTenant = errors.New("stripe: tenant id is required")

	// errNotImplemented marks provider methods that are not built yet.
	errNotImplemented = errors.New("stripe: not implemented")
)

// Config configures a Provider.
type Config struct {
	// SecretKey is the Stripe secret API key. It is never logged or put in
	// an error.
	SecretKey string
	// WebhookSecret is the signing secret for Stripe webhook events.
	WebhookSecret string
	// Customers maps Ledger tenants to Stripe customer ids.
	Customers CustomerStore
	// Currency is the ISO currency code sent on setup-mode Checkout
	// sessions. Empty means "usd"; it is lowercased.
	Currency string
	// Backend overrides the Stripe API backend. Tests only; nil uses
	// Stripe's API.
	Backend stripego.Backend
}

// Provider is a provider.Provider backed by Stripe.
type Provider struct {
	client        *stripego.Client
	customers     CustomerStore
	currency      string
	webhookSecret string
}

// defaultCurrency is used when Config.Currency is empty.
const defaultCurrency = "usd"

var _ provider.Provider = (*Provider)(nil)

// New builds a Provider. SecretKey, WebhookSecret and Customers are required.
func New(cfg Config) (*Provider, error) {
	switch {
	case cfg.SecretKey == "":
		return nil, errors.New("stripe: secret key is required")
	case cfg.WebhookSecret == "":
		return nil, errors.New("stripe: webhook secret is required")
	case cfg.Customers == nil:
		return nil, errors.New("stripe: customer store is required")
	}

	var opts []stripego.ClientOption
	if cfg.Backend != nil {
		opts = append(opts, stripego.WithBackends(&stripego.Backends{
			API:         cfg.Backend,
			Connect:     cfg.Backend,
			Uploads:     cfg.Backend,
			MeterEvents: cfg.Backend,
		}))
	}

	currency := strings.ToLower(strings.TrimSpace(cfg.Currency))
	if currency == "" {
		currency = defaultCurrency
	}

	return &Provider{
		client:        stripego.NewClient(cfg.SecretKey, opts...),
		customers:     cfg.Customers,
		currency:      currency,
		webhookSecret: cfg.WebhookSecret,
	}, nil
}

// Name returns "stripe".
func (p *Provider) Name() string { return providerName }

// SyncPlan does nothing: Ledger owns plans and Stripe never sees them.
func (p *Provider) SyncPlan(context.Context, *plan.Plan) (string, error) { return "", nil }

// SyncFeature does nothing: Ledger owns features and Stripe never sees them.
func (p *Provider) SyncFeature(context.Context, *feature.Feature) (string, error) { return "", nil }

// SyncSubscription is not implemented yet.
func (p *Provider) SyncSubscription(context.Context, *subscription.Subscription) (string, error) {
	return "", errNotImplemented
}

// SyncInvoice is not implemented yet.
func (p *Provider) SyncInvoice(context.Context, *invoice.Invoice) (string, error) {
	return "", errNotImplemented
}

// HandleWebhook is not implemented yet.
func (p *Provider) HandleWebhook(context.Context, []byte) (*provider.WebhookResult, error) {
	return nil, errNotImplemented
}

// ImportPlan returns ErrUnsupported.
func (p *Provider) ImportPlan(context.Context, string) (*plan.Plan, error) {
	return nil, ErrUnsupported
}

// ImportFeature returns ErrUnsupported.
func (p *Provider) ImportFeature(context.Context, string) (*feature.Feature, error) {
	return nil, ErrUnsupported
}

// ImportSubscription returns ErrUnsupported.
func (p *Provider) ImportSubscription(context.Context, string) (*subscription.Subscription, error) {
	return nil, ErrUnsupported
}

// ImportInvoice returns ErrUnsupported.
func (p *Provider) ImportInvoice(context.Context, string) (*invoice.Invoice, error) {
	return nil, ErrUnsupported
}
