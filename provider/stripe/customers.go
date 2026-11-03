package stripe

import (
	"context"
	"fmt"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/xraph/ledger/provider"
)

// CustomerStore maps Ledger tenants to Stripe customer ids. The host app
// persists the mapping.
type CustomerStore interface {
	// CustomerID returns the tenant's Stripe customer id, or "" when it has
	// none.
	CustomerID(ctx context.Context, tenantID string) (string, error)
	// SetCustomerID records the tenant's Stripe customer id.
	SetCustomerID(ctx context.Context, tenantID, customerID string) error
}

// EnsureCustomer returns the tenant's Stripe customer id, creating the
// customer on first call. Creation uses the idempotency key
// "customer:<tenantID>", so a retry after a lost response or a failed
// SetCustomerID gets the same customer back.
func (p *Provider) EnsureCustomer(ctx context.Context, tenantID, email, name string) (string, error) {
	id, err := p.customers.CustomerID(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("stripe: look up customer for tenant %s: %w", tenantID, err)
	}
	if id != "" {
		return id, nil
	}

	params := &stripego.CustomerCreateParams{
		Metadata: map[string]string{tenantMetadataKey: tenantID},
	}
	if email != "" {
		params.Email = stripego.String(email)
	}
	if name != "" {
		params.Name = stripego.String(name)
	}
	params.SetIdempotencyKey("customer:" + tenantID)

	c, err := p.client.V1Customers.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("stripe: create customer for tenant %s: %w", tenantID, err)
	}
	if err := p.customers.SetCustomerID(ctx, tenantID, c.ID); err != nil {
		return "", fmt.Errorf("stripe: save customer for tenant %s: %w", tenantID, err)
	}
	return c.ID, nil
}

// CreateSetupSession opens a Checkout session in setup mode so the tenant can
// save a card, and returns the URL to send them to. The tenant must already
// have a customer (see EnsureCustomer); otherwise it returns ErrNoCustomer.
func (p *Provider) CreateSetupSession(ctx context.Context, tenantID, successURL, cancelURL string) (string, error) {
	customerID, err := p.customerID(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if customerID == "" {
		return "", ErrNoCustomer
	}

	params := &stripego.CheckoutSessionCreateParams{
		Mode:                      stripego.String(string(stripego.CheckoutSessionModeSetup)),
		Customer:                  stripego.String(customerID),
		AllowedPaymentMethodTypes: []*string{stripego.String("card")},
		SuccessURL:                stripego.String(successURL),
		CancelURL:                 stripego.String(cancelURL),
		Metadata:                  map[string]string{tenantMetadataKey: tenantID},
	}
	s, err := p.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("stripe: create setup session for tenant %s: %w", tenantID, err)
	}
	return s.URL, nil
}

// ListPaymentMethods returns the tenant's saved cards, live from Stripe. The
// card set as the customer's invoice default is marked IsDefault. A tenant
// with no customer has no cards.
func (p *Provider) ListPaymentMethods(ctx context.Context, tenantID string) ([]provider.PaymentMethod, error) {
	customerID, err := p.customerID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if customerID == "" {
		return []provider.PaymentMethod{}, nil
	}

	c, err := p.client.V1Customers.Retrieve(ctx, customerID, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe: retrieve customer for tenant %s: %w", tenantID, err)
	}
	var defaultID string
	if c.InvoiceSettings != nil && c.InvoiceSettings.DefaultPaymentMethod != nil {
		defaultID = c.InvoiceSettings.DefaultPaymentMethod.ID
	}

	methods := []provider.PaymentMethod{}
	list := p.client.V1Customers.ListPaymentMethods(ctx, &stripego.CustomerListPaymentMethodsParams{
		Customer: stripego.String(customerID),
		Type:     stripego.String(string(stripego.PaymentMethodTypeCard)),
	})
	for pm, err := range list.All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("stripe: list payment methods for tenant %s: %w", tenantID, err)
		}
		methods = append(methods, toPaymentMethod(pm, defaultID))
	}
	return methods, nil
}

// customerID reads the tenant's Stripe customer id from the store.
func (p *Provider) customerID(ctx context.Context, tenantID string) (string, error) {
	id, err := p.customers.CustomerID(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("stripe: look up customer for tenant %s: %w", tenantID, err)
	}
	return id, nil
}

// toPaymentMethod maps a Stripe payment method onto Ledger's read-only DTO.
func toPaymentMethod(pm *stripego.PaymentMethod, defaultID string) provider.PaymentMethod {
	out := provider.PaymentMethod{
		ID:           pm.ID,
		Type:         string(pm.Type),
		IsDefault:    pm.ID == defaultID,
		ProviderName: providerName,
		ProviderID:   pm.ID,
	}
	if pm.Card != nil {
		out.Last4 = pm.Card.Last4
		out.Brand = string(pm.Card.Brand)
		out.ExpiryMonth = int(pm.Card.ExpMonth)
		out.ExpiryYear = int(pm.Card.ExpYear)
	}
	return out
}
