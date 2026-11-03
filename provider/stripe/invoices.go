package stripe

import (
	"context"
	"errors"
	"fmt"
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
// It creates a draft Stripe invoice with auto-advance off, then one invoice
// item per Ledger line, each naming the draft: in this API version a new
// invoice does not pull in the customer's pending items. Credit and discount
// lines go across as negative items. Only the finalize call turns
// auto-advance on, so Stripe never collects on a draft that is missing
// lines. If adding a line fails, the draft is deleted before the error is
// returned.
//
// Only a pending or past-due invoice is sent; any other status is an error
// and Stripe is not called. An invoice that already names a Stripe invoice
// (ProviderName "stripe" and a ProviderID) returns that id, again without
// calling Stripe.
//
// The lines must sum to the invoice total, or nothing is sent. A zero-total
// invoice is not sent and returns ("", nil); Ledger's SyncInvoiceToProvider
// marks such an invoice paid without calling SyncInvoice at all. A negative
// total is an error.
//
// Before creating anything it looks through the customer's 20 most recent
// Stripe invoices for one carrying this Ledger invoice id. An open, paid or
// uncollectible one was already sent, and its id is returned with nothing
// created, so a retry after a lost response does not bill twice. A draft one
// is a leftover from a failed attempt and is deleted. Void ones are ignored.
//
// The draft is created without an idempotency key of ours (stripe-go still
// keys its own network retries), so a retry after a failure starts a fresh
// draft instead of replaying a deleted one. The items and the finalize call
// are keyed on the draft: "invoice:<id>:<draft id>:<line id>" and
// "invoice:<id>:<draft id>:finalize". The lookup only reaches 20 invoices
// back, so the caller should still keep the returned id and not sync an
// invoice that already has one.
func (p *Provider) SyncInvoice(ctx context.Context, inv *invoice.Invoice) (string, error) {
	if inv == nil {
		return "", errors.New("stripe: sync invoice: invoice is nil")
	}
	invID := inv.ID.String()
	if inv.Status != invoice.StatusPending && inv.Status != invoice.StatusPastDue {
		return "", fmt.Errorf("stripe: sync invoice %s: status is %q, only pending or past_due invoices are sent", invID, inv.Status)
	}
	if inv.ProviderName == providerName && inv.ProviderID != "" {
		return inv.ProviderID, nil
	}
	if inv.Total.Amount < 0 {
		return "", fmt.Errorf("stripe: sync invoice %s: total %d is negative", invID, inv.Total.Amount)
	}
	if inv.Total.Amount == 0 {
		return "", nil
	}

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

	existing, err := p.findSentInvoice(ctx, customerID, invID)
	if err != nil {
		return "", err
	}
	if existing != "" {
		return existing, nil
	}

	draft, err := p.client.V1Invoices.Create(ctx, &stripego.InvoiceCreateParams{
		Customer:                    stripego.String(customerID),
		Currency:                    stripego.String(currency),
		CollectionMethod:            stripego.String(string(stripego.InvoiceCollectionMethodChargeAutomatically)),
		AutoAdvance:                 stripego.Bool(false),
		PendingInvoiceItemsBehavior: stripego.String("exclude"),
		Metadata:                    map[string]string{invoiceMetadataKey: invID},
	})
	if err != nil {
		return "", fmt.Errorf("stripe: create invoice for %s: %w", invID, err)
	}
	keyPrefix := "invoice:" + invID + ":" + draft.ID + ":"

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
		item.SetIdempotencyKey(keyPrefix + lineID)
		if _, err = p.client.V1InvoiceItems.Create(ctx, item); err != nil {
			err = fmt.Errorf("stripe: add line %s to invoice %s: %w", lineID, invID, err)
			if _, delErr := p.client.V1Invoices.Delete(ctx, draft.ID, nil); delErr != nil {
				err = errors.Join(err, fmt.Errorf("stripe: delete draft invoice %s: %w", draft.ID, delErr))
			}
			return "", err
		}
	}

	finalize := &stripego.InvoiceFinalizeInvoiceParams{AutoAdvance: stripego.Bool(true)}
	finalize.SetIdempotencyKey(keyPrefix + "finalize")
	final, err := p.client.V1Invoices.FinalizeInvoice(ctx, draft.ID, finalize)
	if err != nil {
		return "", fmt.Errorf("stripe: finalize invoice for %s: %w", invID, err)
	}
	return final.ID, nil
}

// recentInvoiceLimit is how many of the customer's newest Stripe invoices
// SyncInvoice checks for one it already sent.
const recentInvoiceLimit = 20

// findSentInvoice looks through the customer's newest Stripe invoices for
// the Ledger invoice invID. It returns the id of one already finalized and
// deletes any draft left by a failed attempt. Stripe's list cannot filter by
// metadata, so the match is made here; the Search API is not used because it
// lags behind writes.
func (p *Provider) findSentInvoice(ctx context.Context, customerID, invID string) (string, error) {
	params := &stripego.InvoiceListParams{Customer: stripego.String(customerID)}
	params.Limit = stripego.Int64(recentInvoiceLimit)

	// Stop on the last invoice of the first page: asking the iterator for
	// one more would fetch a second page only to ignore it.
	seen := 0
	for in, err := range p.client.V1Invoices.List(ctx, params).All(ctx) {
		if err != nil {
			return "", fmt.Errorf("stripe: list invoices for %s: %w", invID, err)
		}
		seen++
		if in.Metadata[invoiceMetadataKey] == invID {
			switch in.Status {
			case stripego.InvoiceStatusOpen, stripego.InvoiceStatusPaid, stripego.InvoiceStatusUncollectible:
				return in.ID, nil
			case stripego.InvoiceStatusDraft:
				if _, delErr := p.client.V1Invoices.Delete(ctx, in.ID, nil); delErr != nil {
					return "", fmt.Errorf("stripe: delete leftover draft invoice %s: %w", in.ID, delErr)
				}
			}
		}
		if seen == recentInvoiceLimit {
			break
		}
	}
	return "", nil
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

// maxRefundKeyLen is the longest refund key RefundInvoice accepts. Stripe
// caps idempotency keys at 255 characters; the rest is left for the
// "refund:<stripe invoice>:" prefix.
const maxRefundKeyLen = 200

// RefundInvoice refunds the payment that paid a Stripe invoice. A nil amount
// refunds whatever is left of the payment; otherwise amount, in minor units
// of the payment's currency, is refunded. full reports whether the payment is
// now refunded in total. An invoice paid by more than one payment is refused,
// since it is not clear which one to refund.
//
// refundKey identifies one refund intent and is required. The caller makes a
// new key for each refund it means to issue and reuses that key when it
// retries, so a retry after a lost response does not refund twice. The
// refund is sent with the idempotency key "refund:<stripe invoice>:<key>".
// Stripe caps idempotency keys at 255 characters, so refundKey may be at
// most 200, which leaves room for the prefix.
func (p *Provider) RefundInvoice(ctx context.Context, stripeInvoiceID string, amount *types.Money, refundKey string) (bool, error) {
	if stripeInvoiceID == "" {
		return false, errors.New("stripe: refund invoice: stripe invoice id is required")
	}
	if refundKey == "" {
		return false, fmt.Errorf("stripe: refund invoice %s: refund key is required", stripeInvoiceID)
	}
	if len(refundKey) > maxRefundKeyLen {
		return false, fmt.Errorf("stripe: refund invoice %s: refund key is %d characters, at most %d are allowed",
			stripeInvoiceID, len(refundKey), maxRefundKeyLen)
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
	if amount != nil {
		params.Amount = stripego.Int64(amount.Amount)
	}
	params.AddExpand("charge")
	params.SetIdempotencyKey("refund:" + stripeInvoiceID + ":" + refundKey)

	refund, err := p.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return false, fmt.Errorf("stripe: refund invoice %s: %w", stripeInvoiceID, err)
	}
	if refund.Charge == nil {
		return false, fmt.Errorf("stripe: refund invoice %s: refund %s names no charge", stripeInvoiceID, refund.ID)
	}
	return refund.Charge.AmountRefunded >= payment.AmountPaid, nil
}

// paidPayment returns the one paid payment on a Stripe invoice. In this API
// version an invoice no longer names its charge or payment intent; the
// invoice payments list does.
func (p *Provider) paidPayment(ctx context.Context, stripeInvoiceID string) (*stripego.InvoicePayment, error) {
	params := &stripego.InvoicePaymentListParams{
		Invoice: stripego.String(stripeInvoiceID),
		Status:  stripego.String("paid"),
	}

	var found *stripego.InvoicePayment
	for payment, err := range p.client.V1InvoicePayments.List(ctx, params).All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("stripe: list payments for invoice %s: %w", stripeInvoiceID, err)
		}
		if payment.Payment == nil || payment.AmountPaid <= 0 {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("stripe: refund invoice %s: it has more than one paid payment", stripeInvoiceID)
		}
		found = payment
	}
	if found == nil {
		return nil, ErrNotPaid
	}
	return found, nil
}
