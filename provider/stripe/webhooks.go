package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	stripego "github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"

	"github.com/xraph/ledger/provider"
)

// Verify checks a webhook payload against its Stripe-Signature header using
// the configured webhook secret. It rejects a missing or wrong signature, a
// signature older than five minutes, and an event from another release
// train. stripe-go compares only the train, the part after the dot, so any
// "*.endive" version passes while a "*.dahlia" one or an undotted pre-train
// version is refused. Call it before HandleWebhook.
func (p *Provider) Verify(payload []byte, signatureHeader string) error {
	if signatureHeader == "" {
		return errors.New("stripe: verify webhook: missing Stripe-Signature header")
	}
	if _, err := webhook.ConstructEvent(payload, signatureHeader, p.webhookSecret); err != nil {
		return fmt.Errorf("stripe: verify webhook: %w", err)
	}
	return nil
}

// HandleWebhook maps a Stripe event onto Ledger. It assumes Verify has
// already passed for payload and does not check the signature again.
//
// invoice.paid and invoice.payment_failed report the Ledger invoice named in
// the Stripe invoice's metadata. setup_intent.succeeded and a setup-mode
// checkout.session.completed make the saved card the customer's default for
// invoices and report the tenant. Any other event, or one that does not
// carry Ledger metadata, comes back with Handled false and no error.
//
// Handling the same event twice gives the same result, and an event for a
// card older than the customer's current default leaves that default alone.
func (p *Provider) HandleWebhook(ctx context.Context, payload []byte) (*provider.WebhookResult, error) {
	var evt stripego.Event
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil, fmt.Errorf("stripe: parse webhook event: %w", err)
	}
	if evt.Data == nil {
		return nil, fmt.Errorf("stripe: webhook event %s has no data", evt.ID)
	}

	res := &provider.WebhookResult{
		EventType: string(evt.Type),
		Timestamp: time.Unix(evt.Created, 0),
	}
	var err error
	switch evt.Type {
	case stripego.EventTypeInvoicePaid, stripego.EventTypeInvoicePaymentFailed:
		err = handleInvoiceEvent(evt, res)
	case stripego.EventTypeSetupIntentSucceeded:
		err = p.handleSetupIntent(ctx, evt, res)
	case stripego.EventTypeCheckoutSessionCompleted:
		err = p.handleCheckoutSession(ctx, evt, res)
	}
	if err != nil {
		return nil, fmt.Errorf("stripe: handle %s event %s: %w", evt.Type, evt.ID, err)
	}
	return res, nil
}

// handleInvoiceEvent reports the Ledger invoice a Stripe invoice bills.
func handleInvoiceEvent(evt stripego.Event, res *provider.WebhookResult) error {
	var inv stripego.Invoice
	if err := json.Unmarshal(evt.Data.Raw, &inv); err != nil {
		return err
	}
	res.ProviderID = inv.ID
	ledgerID := inv.Metadata[invoiceMetadataKey]
	if ledgerID == "" {
		return nil
	}
	res.EntityType = "invoice"
	res.EntityID = ledgerID
	res.Handled = true
	return nil
}

// handleSetupIntent makes a succeeded setup intent's card the customer's
// default. It needs the tenant in the setup intent's metadata; a setup
// intent from Checkout has none and is handled through its
// checkout.session.completed event instead.
func (p *Provider) handleSetupIntent(ctx context.Context, evt stripego.Event, res *provider.WebhookResult) error {
	var si stripego.SetupIntent
	if err := json.Unmarshal(evt.Data.Raw, &si); err != nil {
		return err
	}
	res.ProviderID = si.ID
	tenantID := si.Metadata[tenantMetadataKey]
	if tenantID == "" || si.Customer == nil || si.PaymentMethod == nil {
		return nil
	}
	if err := p.setDefaultPaymentMethod(ctx, si.Customer.ID, si.PaymentMethod.ID); err != nil {
		return err
	}
	res.EntityType = "tenant"
	res.EntityID = tenantID
	res.Handled = true
	return nil
}

// handleCheckoutSession makes the card saved by a setup-mode Checkout
// session the customer's default. The event names the setup intent by id
// only, so it is fetched to find the card.
func (p *Provider) handleCheckoutSession(ctx context.Context, evt stripego.Event, res *provider.WebhookResult) error {
	var s stripego.CheckoutSession
	if err := json.Unmarshal(evt.Data.Raw, &s); err != nil {
		return err
	}
	res.ProviderID = s.ID
	tenantID := s.Metadata[tenantMetadataKey]
	if s.Mode != stripego.CheckoutSessionModeSetup || tenantID == "" || s.Customer == nil || s.SetupIntent == nil {
		return nil
	}

	si, err := p.client.V1SetupIntents.Retrieve(ctx, s.SetupIntent.ID, nil)
	if err != nil {
		return fmt.Errorf("retrieve setup intent %s: %w", s.SetupIntent.ID, err)
	}
	if si.PaymentMethod == nil {
		return nil
	}
	if err := p.setDefaultPaymentMethod(ctx, s.Customer.ID, si.PaymentMethod.ID); err != nil {
		return err
	}
	res.EntityType = "tenant"
	res.EntityID = tenantID
	res.Handled = true
	return nil
}

// setDefaultPaymentMethod makes pm the card Stripe charges for the
// customer's invoices, unless the customer's current default is that card
// already or was created after it. That keeps a late or replayed event for
// an older card from replacing a newer one. The update sends no idempotency
// key: it is a plain overwrite, and a cached replay could undo a later
// change of card.
func (p *Provider) setDefaultPaymentMethod(ctx context.Context, customerID, pmID string) error {
	get := &stripego.CustomerRetrieveParams{}
	get.AddExpand("invoice_settings.default_payment_method")
	c, err := p.client.V1Customers.Retrieve(ctx, customerID, get)
	if err != nil {
		return fmt.Errorf("retrieve customer %s: %w", customerID, err)
	}
	if c.InvoiceSettings != nil && c.InvoiceSettings.DefaultPaymentMethod != nil {
		current := c.InvoiceSettings.DefaultPaymentMethod
		if current.ID == pmID {
			return nil
		}
		incoming, err := p.client.V1PaymentMethods.Retrieve(ctx, pmID, nil)
		if err != nil {
			return fmt.Errorf("retrieve payment method %s: %w", pmID, err)
		}
		if current.Created > incoming.Created {
			return nil
		}
	}

	params := &stripego.CustomerUpdateParams{
		InvoiceSettings: &stripego.CustomerUpdateInvoiceSettingsParams{
			DefaultPaymentMethod: stripego.String(pmID),
		},
	}
	if _, err := p.client.V1Customers.Update(ctx, customerID, params); err != nil {
		return fmt.Errorf("set default payment method for customer %s: %w", customerID, err)
	}
	return nil
}
