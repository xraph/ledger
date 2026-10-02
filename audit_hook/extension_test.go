package audithook_test

import (
	"context"
	"testing"
	"time"

	audithook "github.com/xraph/ledger/audit_hook"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/subscription"
)

// Every subscription and invoice event names its resource, and carries the
// tenant and app, so an event the lifecycle clock fires, which has no tenant
// or actor in its context, still says whose row changed.
func TestAuditEventsNameTheirResource(t *testing.T) {
	var got []*audithook.AuditEvent
	ext := audithook.New(audithook.RecorderFunc(func(_ context.Context, e *audithook.AuditEvent) error {
		got = append(got, e)
		return nil
	}))
	ctx := context.Background()
	sub := &subscription.Subscription{ID: id.NewSubscriptionID(), TenantID: "acme", AppID: "app_1"}
	inv := &invoice.Invoice{ID: id.NewInvoiceID(), TenantID: "acme", AppID: "app_1", SubscriptionID: sub.ID}
	renewal := &subscription.Renewal{Subscription: sub, Ended: []subscription.Period{
		{Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
	}}

	calls := []struct {
		action, id string
		call       func() error
	}{
		{audithook.ActionSubscriptionCreated, sub.ID.String(), func() error { return ext.OnSubscriptionCreated(ctx, sub) }},
		{audithook.ActionSubscriptionUpgraded, sub.ID.String(), func() error { return ext.OnSubscriptionChanged(ctx, sub, nil, nil) }},
		{audithook.ActionSubscriptionCanceled, sub.ID.String(), func() error { return ext.OnSubscriptionCanceled(ctx, sub) }},
		{audithook.ActionSubscriptionCancelScheduled, sub.ID.String(), func() error { return ext.OnSubscriptionCancelScheduled(ctx, sub) }},
		{audithook.ActionSubscriptionRenewed, sub.ID.String(), func() error { return ext.OnSubscriptionRenewed(ctx, renewal) }},
		{audithook.ActionSubscriptionTrialEnded, sub.ID.String(), func() error { return ext.OnSubscriptionTrialEnded(ctx, sub) }},
		{audithook.ActionInvoiceGenerated, inv.ID.String(), func() error { return ext.OnInvoiceGenerated(ctx, inv) }},
		{audithook.ActionInvoiceFinalized, inv.ID.String(), func() error { return ext.OnInvoiceFinalized(ctx, inv) }},
		{audithook.ActionInvoicePaid, inv.ID.String(), func() error { return ext.OnInvoicePaid(ctx, inv) }},
		{audithook.ActionInvoiceFailed, inv.ID.String(), func() error { return ext.OnInvoiceFailed(ctx, inv, nil) }},
		{audithook.ActionInvoiceVoided, inv.ID.String(), func() error { return ext.OnInvoiceVoided(ctx, inv, "dup") }},
		{audithook.ActionInvoicePastDue, inv.ID.String(), func() error { return ext.OnInvoicePastDue(ctx, inv) }},
	}
	for _, c := range calls {
		if err := c.call(); err != nil {
			t.Fatalf("%s: %v", c.action, err)
		}
	}
	if len(got) != len(calls) {
		t.Fatalf("recorded %d events, want %d", len(got), len(calls))
	}
	for i, c := range calls {
		e := got[i]
		if e.Action != c.action || e.ResourceID != c.id {
			t.Errorf("event %d: action %q resource %q, want %q on %q", i, e.Action, e.ResourceID, c.action, c.id)
		}
		if e.Metadata["tenant_id"] != "acme" || e.Metadata["app_id"] != "app_1" {
			t.Errorf("%s: metadata %v, want the tenant and app", c.action, e.Metadata)
		}
	}
	if got[4].Metadata["periods_ended"] != 1 {
		t.Errorf("renewal metadata %v, want periods_ended 1", got[4].Metadata)
	}
}
