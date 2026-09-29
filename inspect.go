package ledger

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/id"
)

// InspectEntitlement answers an entitlement question for a named tenant and
// app, fresh from the store, without touching the cache or firing plugin
// events. It is for operators looking at a customer, not for enforcement.
func (l *Ledger) InspectEntitlement(ctx context.Context, tenantID, appID, featureKey string) (*entitlement.Result, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("%w: a tenant is required", ErrInvalidInput)
	}
	result, _, err := l.computeEntitlement(ctx, tenantID, appID, featureKey)
	return result, err
}

// InvoiceFormats names every registered invoice formatter, sorted.
func (l *Ledger) InvoiceFormats() []string {
	formats := l.plugins.InvoiceFormats()
	if formats == nil {
		return []string{}
	}
	return formats
}

// ExportInvoice renders an invoice through the formatter registered for format.
func (l *Ledger) ExportInvoice(ctx context.Context, invID id.InvoiceID, format string) ([]byte, error) {
	f := l.plugins.GetInvoiceFormatter(format)
	if f == nil {
		return nil, fmt.Errorf("%w: no invoice formatter for format %q", ErrInvalidInput, format)
	}
	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := f.Render(ctx, inv, &buf); err != nil {
		return nil, fmt.Errorf("invoice formatter %q: %w", f.Name(), err)
	}
	return buf.Bytes(), nil
}

// ProviderNames names every registered payment provider, sorted.
func (l *Ledger) ProviderNames() []string {
	names := []string{}
	for _, p := range l.plugins.GetPaymentProviders() {
		names = append(names, p.Provider().Name())
	}
	sort.Strings(names)
	return names
}
