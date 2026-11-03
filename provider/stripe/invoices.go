package stripe

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// invoiceMetadataKey is the Stripe metadata key that ties a Stripe invoice,
// and each of its items, to the Ledger invoice it bills.
const invoiceMetadataKey = "ledger_invoice_id"

// lineItemMetadataKey ties a Stripe invoice item to its Ledger line item.
const lineItemMetadataKey = "ledger_line_item_id"

// SyncSubscription makes sure the subscription's tenant has a Stripe
// customer and returns the customer id. Stripe never holds the subscription
// itself.
func (p *Provider) SyncSubscription(ctx context.Context, s *subscription.Subscription) (string, error) {
	if s == nil {
		return "", errors.New("stripe: sync subscription: subscription is nil")
	}
	return p.EnsureCustomer(ctx, s.TenantID, "", "")
}

// SyncInvoice copies a Ledger invoice to Stripe and finalizes it, so Stripe
// charges the tenant's default card. It returns the Stripe invoice id.
//
// It creates a draft Stripe invoice first and then one invoice item per
// Ledger line, each naming the draft: in this API version a new invoice does
// not pull in the customer's pending items. Credit and discount lines go
// across as negative items. The lines must sum to the invoice total, or
// nothing is sent. A zero-total invoice is not sent and returns ("", nil).
//
// Every call carries an idempotency key ("invoice:<id>", "invoice:<id>:<line
// id>" and "invoice:<id>:finalize"), so a retry inside Stripe's 24 hour
// window returns the same Stripe invoice instead of billing twice.
func (p *Provider) SyncInvoice(ctx context.Context, inv *invoice.Invoice) (string, error) {
	if inv == nil {
		return "", errors.New("stripe: sync invoice: invoice is nil")
	}
	if inv.Total.Amount <= 0 {
		return "", nil
	}

	invID := inv.ID.String()
	currency := strings.ToLower(inv.Currency)
	if currency == "" {
		currency = strings.ToLower(inv.Total.Currency)
	}
	if err := checkLines(inv, currency); err != nil {
		return "", fmt.Errorf("stripe: sync invoice %s: %w", invID, err)
	}

	customerID, err := p.customerID(ctx, inv.TenantID)
	if err != nil {
		return "", err
	}
	if customerID == "" {
		return "", ErrNoCustomer
	}

	params := &stripego.InvoiceCreateParams{
		Customer:                    stripego.String(customerID),
		Currency:                    stripego.String(currency),
		CollectionMethod:            stripego.String(string(stripego.InvoiceCollectionMethodChargeAutomatically)),
		AutoAdvance:                 stripego.Bool(true),
		PendingInvoiceItemsBehavior: stripego.String("exclude"),
		Metadata:                    map[string]string{invoiceMetadataKey: invID},
	}
	params.SetIdempotencyKey("invoice:" + invID)
	draft, err := p.client.V1Invoices.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("stripe: create invoice for %s: %w", invID, err)
	}

	for _, li := range inv.LineItems {
		lineID := li.ID.String()
		item := &stripego.InvoiceItemCreateParams{
			Customer:    stripego.String(customerID),
			Invoice:     stripego.String(draft.ID),
			Amount:      stripego.Int64(li.Amount.Amount),
			Currency:    stripego.String(currency),
			Description: stripego.String(li.Description),
			Metadata: map[string]string{
				invoiceMetadataKey:  invID,
				lineItemMetadataKey: lineID,
			},
		}
		item.SetIdempotencyKey("invoice:" + invID + ":" + lineID)
		if _, err = p.client.V1InvoiceItems.Create(ctx, item); err != nil {
			return "", fmt.Errorf("stripe: add line %s to invoice %s: %w", lineID, invID, err)
		}
	}

	finalize := &stripego.InvoiceFinalizeInvoiceParams{AutoAdvance: stripego.Bool(true)}
	finalize.SetIdempotencyKey("invoice:" + invID + ":finalize")
	final, err := p.client.V1Invoices.FinalizeInvoice(ctx, draft.ID, finalize)
	if err != nil {
		return "", fmt.Errorf("stripe: finalize invoice for %s: %w", invID, err)
	}
	return final.ID, nil
}

// checkLines makes sure every line is in the invoice currency and that the
// lines sum to the invoice total, so the Stripe invoice bills what Ledger
// computed. The sum differs when credits and discounts exceed the subtotal
// and Ledger clamps the total at zero before tax.
func checkLines(inv *invoice.Invoice, currency string) error {
	var sum int64
	for _, li := range inv.LineItems {
		if c := strings.ToLower(li.Amount.Currency); c != "" && c != currency {
			return fmt.Errorf("line %s is in %s, invoice is in %s", li.ID, c, currency)
		}
		sum += li.Amount.Amount
	}
	if sum != inv.Total.Amount {
		return fmt.Errorf("lines sum to %d, invoice total is %d", sum, inv.Total.Amount)
	}
	return nil
}

// RefundInvoice refunds the payment that paid a Stripe invoice. A nil amount
// refunds whatever is left of the payment; otherwise amount, in minor units
// of the payment's currency, is refunded. full reports whether the payment is
// now refunded in total.
//
// The refund carries the idempotency key "refund:<stripe invoice>:<amount>",
// with "full" for a nil amount, so a retry does not refund twice. Two
// separate partial refunds of the same amount on one invoice share that key
// too: inside Stripe's 24 hour window the second returns the first.
func (p *Provider) RefundInvoice(ctx context.Context, stripeInvoiceID string, amount *types.Money) (bool, error) {
	if stripeInvoiceID == "" {
		return false, errors.New("stripe: refund invoice: stripe invoice id is required")
	}
	if amount != nil && amount.Amount <= 0 {
		return false, fmt.Errorf("stripe: refund invoice %s: amount must be positive", stripeInvoiceID)
	}

	payment, err := p.paidPayment(ctx, stripeInvoiceID)
	if err != nil {
		return false, err
	}
	if amount != nil && !strings.EqualFold(amount.Currency, string(payment.Currency)) {
		return false, fmt.Errorf("stripe: refund invoice %s: amount is in %s, payment is in %s",
			stripeInvoiceID, amount.Currency, payment.Currency)
	}

	params := &stripego.RefundCreateParams{}
	switch {
	case payment.Payment.PaymentIntent != nil:
		params.PaymentIntent = stripego.String(payment.Payment.PaymentIntent.ID)
	case payment.Payment.Charge != nil:
		params.Charge = stripego.String(payment.Payment.Charge.ID)
	default:
		return false, fmt.Errorf("stripe: refund invoice %s: payment %s has no charge to refund", stripeInvoiceID, payment.ID)
	}
	key := "full"
	if amount != nil {
		params.Amount = stripego.Int64(amount.Amount)
		key = strconv.FormatInt(amount.Amount, 10)
	}
	params.AddExpand("charge")
	params.SetIdempotencyKey("refund:" + stripeInvoiceID + ":" + key)

	refund, err := p.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return false, fmt.Errorf("stripe: refund invoice %s: %w", stripeInvoiceID, err)
	}
	if refund.Charge == nil {
		return false, fmt.Errorf("stripe: refund invoice %s: refund %s names no charge", stripeInvoiceID, refund.ID)
	}
	return refund.Charge.AmountRefunded >= payment.AmountPaid, nil
}

// paidPayment returns the paid payment on a Stripe invoice. In this API
// version an invoice no longer names its charge or payment intent; the
// invoice payments list does.
func (p *Provider) paidPayment(ctx context.Context, stripeInvoiceID string) (*stripego.InvoicePayment, error) {
	list := p.client.V1InvoicePayments.List(ctx, &stripego.InvoicePaymentListParams{
		Invoice: stripego.String(stripeInvoiceID),
		Status:  stripego.String("paid"),
	})
	for payment, err := range list.All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("stripe: list payments for invoice %s: %w", stripeInvoiceID, err)
		}
		if payment.Payment != nil && payment.AmountPaid > 0 {
			return payment, nil
		}
	}
	return nil, ErrNotPaid
}
