package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// ──────────────────────────────────────────────────
// Provider Import (Data Recovery)
// ──────────────────────────────────────────────────

// ImportOption configures an import from a payment provider.
type ImportOption func(*importConfig)

type importConfig struct {
	appID  string
	appSet bool
}

// ImportInto files the imported record under appID. The provider interface has
// no per-app scoping and several apps can share one provider account, so the
// caller names the app. A record the provider itself files under a different
// app is refused with that entity's not-found error, and nothing is stored. An
// empty appID is the shared catalog for a feature, and the no-app scope for a
// plan, a subscription or an invoice.
func ImportInto(appID string) ImportOption {
	return func(c *importConfig) {
		c.appID = appID
		c.appSet = true
	}
}

func newImportConfig(providerID string, opts []ImportOption) (importConfig, error) {
	var c importConfig
	for _, opt := range opts {
		opt(&c)
	}
	if strings.TrimSpace(providerID) == "" {
		return c, fmt.Errorf("%w: a provider id is required", ErrInvalidInput)
	}
	return c, nil
}

// underImportLock runs the part of an import that checks for a duplicate and
// then writes, so a second import of the same record in this process waits for
// the first and then finds it. It does not cover a second replica, which still
// relies on the dashboard disabling its button while a request is pending.
func (l *Ledger) underImportLock(fn func() error) error {
	l.importMu.Lock()
	defer l.importMu.Unlock()
	return fn()
}

// app picks the app an imported record is filed under. Without ImportInto it
// is whatever the provider reported, which is how imports behaved before the
// option existed.
func (c importConfig) app(reported, providerID string, notFound error) (string, error) {
	if !c.appSet {
		return reported, nil
	}
	if reported != "" && reported != c.appID {
		return "", fmt.Errorf("%w: provider id %q", notFound, providerID)
	}
	return c.appID, nil
}

// importFrom resolves the provider and pulls one record from it. A provider
// error, or an answer with nothing in it, comes back wrapped in ErrProviderSync
// with the provider's own words.
func importFrom[T any](ctx context.Context, l *Ledger, providerName, providerID, noun string, pull func(provider.Provider) (*T, error)) (*T, provider.Provider, error) {
	prov, err := l.getProvider(providerName)
	if err != nil {
		return nil, nil, err
	}
	got, err := pull(prov)
	if err == nil && got == nil {
		err = fmt.Errorf("the provider returned no %s for %q", noun, providerID)
	}
	if err != nil {
		l.plugins.EmitProviderSync(ctx, prov.Name(), false, err)
		return nil, nil, fmt.Errorf("%w: import %s: %w", ErrProviderSync, noun, err)
	}
	return got, prov, nil
}

// ImportPlanFromProvider pulls a plan from the provider and creates it through
// CreatePlan, so it is validated like any other plan and a slug the app already
// uses is refused with ErrAlreadyExists. That is also what refuses importing
// the same plan twice.
func (l *Ledger) ImportPlanFromProvider(ctx context.Context, providerName, providerID string, opts ...ImportOption) (*plan.Plan, error) {
	cfg, err := newImportConfig(providerID, opts)
	if err != nil {
		return nil, err
	}
	p, prov, err := importFrom(ctx, l, providerName, providerID, "plan", func(pv provider.Provider) (*plan.Plan, error) {
		return pv.ImportPlan(ctx, providerID)
	})
	if err != nil {
		return nil, err
	}
	app, err := cfg.app(p.AppID, providerID, ErrPlanNotFound)
	if err != nil {
		return nil, err
	}

	p.ID = id.NewPlanID()
	p.AppID = app
	p.ProviderID = providerID
	p.ProviderName = prov.Name()
	if err := l.underImportLock(func() error { return l.CreatePlan(ctx, p) }); err != nil {
		return nil, err
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), true, nil)
	return p, nil
}

// ImportFeatureFromProvider pulls a catalog feature from the provider and
// creates it. A key the catalog already uses is refused with ErrAlreadyExists,
// which also refuses importing the same feature twice.
func (l *Ledger) ImportFeatureFromProvider(ctx context.Context, providerName, providerID string, opts ...ImportOption) (*feature.Feature, error) {
	cfg, err := newImportConfig(providerID, opts)
	if err != nil {
		return nil, err
	}
	f, prov, err := importFrom(ctx, l, providerName, providerID, "feature", func(pv provider.Provider) (*feature.Feature, error) {
		return pv.ImportFeature(ctx, providerID)
	})
	if err != nil {
		return nil, err
	}
	app, err := cfg.app(f.AppID, providerID, ErrFeatureNotFound)
	if err != nil {
		return nil, err
	}

	f.Key = strings.TrimSpace(f.Key)
	err = ValidateFeature(f)
	if err != nil {
		return nil, err
	}
	if f.Status == "" {
		f.Status = feature.StatusActive
	}
	if f.Status != feature.StatusActive && f.Status != feature.StatusArchived {
		return nil, fmt.Errorf("%w: unknown feature status %q", ErrInvalidInput, f.Status)
	}

	f.ID = id.NewFeatureID()
	f.AppID = app
	f.ProviderID = providerID
	f.ProviderName = prov.Name()
	err = l.underImportLock(func() error {
		existing, getErr := l.store.GetFeatureByKey(ctx, f.Key, app)
		switch {
		case getErr == nil && existing != nil:
			return fmt.Errorf("%w: feature key %q is already used by %s", ErrAlreadyExists, f.Key, existing.ID)
		case getErr != nil && !errors.Is(getErr, ErrFeatureNotFound):
			return getErr
		}
		return l.CreateFeature(ctx, f)
	})
	if err != nil {
		return nil, err
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), true, nil)
	return f, nil
}

// ImportSubscriptionFromProvider pulls a subscription from the provider and
// creates it through CreateSubscription. Its plan must already be a plan in the
// same app, so the plan is imported first. A provider id the tenant already
// has in the app is refused with ErrAlreadyExists.
func (l *Ledger) ImportSubscriptionFromProvider(ctx context.Context, providerName, providerID string, opts ...ImportOption) (*subscription.Subscription, error) {
	cfg, err := newImportConfig(providerID, opts)
	if err != nil {
		return nil, err
	}
	s, prov, err := importFrom(ctx, l, providerName, providerID, "subscription", func(pv provider.Provider) (*subscription.Subscription, error) {
		return pv.ImportSubscription(ctx, providerID)
	})
	if err != nil {
		return nil, err
	}
	app, err := cfg.app(s.AppID, providerID, ErrSubscriptionNotFound)
	if err != nil {
		return nil, err
	}

	if !importableSubscriptionStatus(s.Status) {
		return nil, fmt.Errorf("%w: unknown subscription status %q", ErrInvalidInput, s.Status)
	}
	s.TenantID = strings.TrimSpace(s.TenantID)
	if s.TenantID == "" {
		return nil, fmt.Errorf("%w: the provider's subscription %q has no tenant id", ErrInvalidInput, providerID)
	}
	err = l.importedPlanInApp(ctx, s.PlanID, app)
	if err != nil {
		return nil, err
	}

	s.ID = id.NewSubscriptionID()
	s.AppID = app
	s.ProviderID = providerID
	s.ProviderName = prov.Name()
	err = l.underImportLock(func() error {
		dup, dupErr := l.storedSubscriptionFor(ctx, s.TenantID, app, prov.Name(), providerID)
		if dupErr != nil {
			return dupErr
		}
		if dup != nil {
			return fmt.Errorf("%w: provider subscription %q is already stored as %s", ErrAlreadyExists, providerID, dup.ID)
		}
		return l.CreateSubscription(ctx, s)
	})
	if err != nil {
		return nil, err
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), true, nil)
	return s, nil
}

// ImportInvoiceFromProvider pulls an invoice from the provider and stores it.
// Its subscription must already be this app's subscription for the invoice's
// tenant. It is refused with ErrAlreadyExists when the provider id is already
// stored, or when the subscription already has a live invoice for the same
// period: the rule GenerateInvoice keeps, because two would bill twice.
func (l *Ledger) ImportInvoiceFromProvider(ctx context.Context, providerName, providerID string, opts ...ImportOption) (*invoice.Invoice, error) {
	cfg, err := newImportConfig(providerID, opts)
	if err != nil {
		return nil, err
	}
	inv, prov, err := importFrom(ctx, l, providerName, providerID, "invoice", func(pv provider.Provider) (*invoice.Invoice, error) {
		return pv.ImportInvoice(ctx, providerID)
	})
	if err != nil {
		return nil, err
	}
	app, err := cfg.app(inv.AppID, providerID, ErrInvoiceNotFound)
	if err != nil {
		return nil, err
	}

	if inv.Status == "" {
		inv.Status = invoice.StatusDraft
	}
	if !importableInvoiceStatus(inv.Status) {
		return nil, fmt.Errorf("%w: unknown invoice status %q", ErrInvalidInput, inv.Status)
	}
	inv.TenantID = strings.TrimSpace(inv.TenantID)
	if inv.TenantID == "" {
		return nil, fmt.Errorf("%w: the provider's invoice %q has no tenant id", ErrInvalidInput, providerID)
	}
	sub, err := l.importedInvoiceSubscription(ctx, inv, app)
	if err != nil {
		return nil, err
	}
	p, err := l.store.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return nil, err
	}
	err = validateImportedInvoice(inv, p)
	if err != nil {
		return nil, err
	}

	inv.ID = id.NewInvoiceID()
	inv.Entity = types.NewEntity()
	inv.AppID = app
	inv.ProviderID = providerID
	inv.ProviderName = prov.Name()
	// Mongo cannot read a line item back without an id (MIGRATION.md, known
	// constraint 18), and a provider need not send one.
	for i := range inv.LineItems {
		if inv.LineItems[i].ID.IsNil() {
			inv.LineItems[i].ID = id.NewLineItemID()
		}
		inv.LineItems[i].InvoiceID = inv.ID
	}
	err = l.underImportLock(func() error {
		if refuseErr := l.refuseStoredInvoice(ctx, inv, sub, app, prov.Name(), providerID); refuseErr != nil {
			return refuseErr
		}
		return l.store.CreateInvoice(ctx, inv)
	})
	if err != nil {
		return nil, err
	}

	l.plugins.EmitInvoiceGenerated(ctx, inv)
	l.plugins.EmitProviderSync(ctx, prov.Name(), true, nil)
	return inv, nil
}

// importedPlanInApp refuses a subscription whose plan is missing or belongs to
// another app, with one message for both, so the refusal says nothing about
// another app's plans. It also refuses a plan that is not active, naming the
// plan so the operator knows what to activate. CreateSubscription refuses the
// same plan through subscribablePlan; this only words it for an import.
func (l *Ledger) importedPlanInApp(ctx context.Context, planID id.PlanID, app string) error {
	if planID.IsNil() {
		return fmt.Errorf("%w: the provider's subscription names no plan", ErrInvalidInput)
	}
	notHere := fmt.Errorf("%w: the provider's subscription is on plan %s, which is not a plan in this app; import the plan first", ErrInvalidInput, planID)
	p, err := l.store.GetPlan(ctx, planID)
	switch {
	case errors.Is(err, ErrPlanNotFound):
		return notHere
	case err != nil:
		return err
	case p.AppID != app:
		return notHere
	case p.Status != plan.StatusActive:
		return fmt.Errorf("%w: plan %q is %s, not active; activate plan %s before importing its subscriptions",
			ErrInvalidInput, p.Slug, p.Status, p.Slug)
	}
	return nil
}

// storedSubscriptionFor finds a subscription the tenant already has in app
// under this provider id. No store indexes provider ids, so it scans the
// tenant's subscriptions in the app, which every store does index.
func (l *Ledger) storedSubscriptionFor(ctx context.Context, tenantID, app, providerName, providerID string) (*subscription.Subscription, error) {
	subs, err := l.store.ListSubscriptions(ctx, tenantID, app, subscription.ListOpts{})
	if err != nil {
		return nil, err
	}
	for _, s := range subs {
		if s.AppID == app && s.ProviderName == providerName && s.ProviderID == providerID {
			return s, nil
		}
	}
	return nil, nil
}

// importedInvoiceSubscription loads the subscription an imported invoice is
// for, and refuses one that is missing, in another app, or for another tenant,
// with one message for all three.
func (l *Ledger) importedInvoiceSubscription(ctx context.Context, inv *invoice.Invoice, app string) (*subscription.Subscription, error) {
	if inv.SubscriptionID.IsNil() {
		return nil, fmt.Errorf("%w: the provider's invoice names no subscription", ErrInvalidInput)
	}
	notHere := fmt.Errorf("%w: the provider's invoice is for subscription %s, which is not this app's subscription for tenant %q; import the subscription first",
		ErrInvalidInput, inv.SubscriptionID, inv.TenantID)
	sub, err := l.store.GetSubscription(ctx, inv.SubscriptionID)
	switch {
	case errors.Is(err, ErrSubscriptionNotFound):
		return nil, notHere
	case err != nil:
		return nil, err
	case sub.AppID != app || sub.TenantID != inv.TenantID:
		return nil, notHere
	}
	return sub, nil
}

// refuseStoredInvoice lists the tenant's invoices inside the imported period,
// which every store indexes, and refuses the provider id if one of them holds
// it, or a second live invoice for the subscription's period.
func (l *Ledger) refuseStoredInvoice(ctx context.Context, inv *invoice.Invoice, sub *subscription.Subscription, app, providerName, providerID string) error {
	stored, err := l.store.ListInvoices(ctx, inv.TenantID, app, invoice.ListOpts{Start: inv.PeriodStart, End: inv.PeriodEnd})
	if err != nil {
		return err
	}
	for _, s := range stored {
		if s.AppID != app {
			continue
		}
		if s.ProviderName == providerName && s.ProviderID == providerID {
			return fmt.Errorf("%w: provider invoice %q is already stored as %s", ErrAlreadyExists, providerID, s.ID)
		}
		live := s.Status != invoice.StatusVoided && inv.Status != invoice.StatusVoided
		if live && s.SubscriptionID == sub.ID && s.PeriodStart.Equal(inv.PeriodStart) && s.PeriodEnd.Equal(inv.PeriodEnd) {
			return fmt.Errorf("%w: subscription %s already has invoice %s for this period", ErrAlreadyExists, sub.ID, s.ID)
		}
	}
	return nil
}

func importableSubscriptionStatus(s subscription.Status) bool {
	switch s {
	case "", subscription.StatusActive, subscription.StatusTrialing, subscription.StatusPastDue,
		subscription.StatusCanceled, subscription.StatusExpired, subscription.StatusPaused:
		return true
	}
	return false
}

func importableInvoiceStatus(s invoice.Status) bool {
	switch s {
	case invoice.StatusDraft, invoice.StatusPending, invoice.StatusPaid, invoice.StatusPastDue, invoice.StatusVoided:
		return true
	}
	return false
}

// validateImportedInvoice refuses a provider invoice the engine would never
// have written, so a bad figure cannot reach the books through an import. It
// follows GenerateInvoice's own arithmetic rather than the plain reading of
// "line items sum to the subtotal": the engine's line items include discount
// lines (negative) and tax lines (positive) beside the charges, so the charge
// lines (everything else) sum to Subtotal, any discount lines sum to minus
// DiscountAmount, any tax lines sum to TaxAmount, and Total is the net amount
// clamped at zero, plus tax. A provider that sends no discount or tax lines
// and only a figure for them is still accepted, since those lines are
// optional there.
func validateImportedInvoice(inv *invoice.Invoice, p *plan.Plan) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: the provider's invoice %s", ErrInvalidInput, fmt.Sprintf(format, args...))
	}
	currency := strings.ToLower(p.Currency)
	if inv.Currency != currency {
		return bad("is in %q, but plan %q bills in lowercase %q", inv.Currency, p.Slug, currency)
	}
	for _, f := range []struct {
		name string
		m    types.Money
	}{{"subtotal", inv.Subtotal}, {"tax amount", inv.TaxAmount}, {"discount amount", inv.DiscountAmount}, {"total", inv.Total}} {
		if f.m.Currency != currency {
			return bad("has its %s in %q, want %q", f.name, f.m.Currency, currency)
		}
		if f.m.IsNegative() {
			return bad("has a negative %s %v", f.name, f.m)
		}
	}

	charges, discounts, taxes := types.Zero(currency), types.Zero(currency), types.Zero(currency)
	var hasDiscount, hasTax bool
	for i, li := range inv.LineItems {
		if li.Amount.Currency != currency || li.UnitAmount.Currency != currency {
			return bad("has line item %d in %q and %q, want %q", i+1, li.Amount.Currency, li.UnitAmount.Currency, currency)
		}
		sum := &charges
		switch li.Type {
		case invoice.LineItemDiscount:
			sum, hasDiscount = &discounts, true
		case invoice.LineItemTax:
			sum, hasTax = &taxes, true
		}
		next, err := sum.CheckedAdd(li.Amount)
		if err != nil {
			return bad("has line items that overflow: %v", err)
		}
		*sum = next
	}
	if !charges.Equal(inv.Subtotal) {
		return bad("has line items charging %v but a subtotal of %v", charges, inv.Subtotal)
	}
	if hasDiscount && !discounts.Negate().Equal(inv.DiscountAmount) {
		return bad("has discount lines of %v but a discount amount of %v", discounts.Negate(), inv.DiscountAmount)
	}
	if hasTax && !taxes.Equal(inv.TaxAmount) {
		return bad("has tax lines of %v but a tax amount of %v", taxes, inv.TaxAmount)
	}

	net, err := inv.Subtotal.CheckedSubtract(inv.DiscountAmount)
	if err != nil {
		return bad("has a subtotal and discount that overflow: %v", err)
	}
	if net.IsNegative() {
		net = types.Zero(currency)
	}
	want, err := net.CheckedAdd(inv.TaxAmount)
	if err != nil {
		return bad("has a total that overflows: %v", err)
	}
	if !want.Equal(inv.Total) {
		return bad("has a total of %v, but its subtotal, discount and tax make %v", inv.Total, want)
	}

	if inv.PeriodStart.IsZero() || inv.PeriodEnd.IsZero() {
		return bad("needs both a period start and a period end")
	}
	if !inv.PeriodEnd.After(inv.PeriodStart) {
		return bad("ends its period before it starts")
	}
	switch inv.Status {
	case invoice.StatusPaid:
		if inv.PaidAt == nil {
			return bad("is paid but has no paid-at time")
		}
	case invoice.StatusPending, invoice.StatusPastDue:
		if inv.DueDate == nil {
			return bad("is %s but has no due date", inv.Status)
		}
	}
	return nil
}
