// Package audithook bridges Ledger lifecycle events to an audit trail backend.
//
// It defines a local Recorder interface so the package does not import
// Chronicle directly. Callers inject a RecorderFunc adapter that bridges
// to Chronicle at wiring time.
package audithook

import (
	"context"
	"fmt"

	log "github.com/xraph/go-utils/log"

	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/subscription"
)

// Compile-time interface checks.
var (
	_ plugin.Plugin                        = (*Extension)(nil)
	_ plugin.OnPlanCreated                 = (*Extension)(nil)
	_ plugin.OnPlanUpdated                 = (*Extension)(nil)
	_ plugin.OnPlanArchived                = (*Extension)(nil)
	_ plugin.OnSubscriptionCreated         = (*Extension)(nil)
	_ plugin.OnSubscriptionChanged         = (*Extension)(nil)
	_ plugin.OnSubscriptionCanceled        = (*Extension)(nil)
	_ plugin.OnSubscriptionCancelScheduled = (*Extension)(nil)
	_ plugin.OnSubscriptionRenewed         = (*Extension)(nil)
	_ plugin.OnSubscriptionTrialEnded      = (*Extension)(nil)
	_ plugin.OnInvoicePastDue              = (*Extension)(nil)
	_ plugin.OnInvoiceGenerated            = (*Extension)(nil)
	_ plugin.OnInvoiceFinalized            = (*Extension)(nil)
	_ plugin.OnInvoicePaid                 = (*Extension)(nil)
	_ plugin.OnInvoiceFailed               = (*Extension)(nil)
	_ plugin.OnInvoiceVoided               = (*Extension)(nil)
	_ plugin.OnQuotaExceeded               = (*Extension)(nil)
	_ plugin.OnEntitlementChecked          = (*Extension)(nil)
)

// Recorder is the interface that audit backends must implement.
// This matches chronicle.Emitter but is defined locally so that the
// audit_hook package does not import Chronicle directly — callers inject
// the concrete *chronicle.Chronicle at wiring time.
type Recorder interface {
	Record(ctx context.Context, event *AuditEvent) error
}

// AuditEvent is a local representation of an audit event.
// It mirrors chronicle/audit.Event but avoids a module dependency.
type AuditEvent struct {
	Action     string         `json:"action"`
	Resource   string         `json:"resource"`
	Category   string         `json:"category"`
	ResourceID string         `json:"resource_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Outcome    string         `json:"outcome"`
	Severity   string         `json:"severity"`
	Reason     string         `json:"reason,omitempty"`
}

// RecorderFunc is an adapter to use a plain function as a Recorder.
type RecorderFunc func(ctx context.Context, event *AuditEvent) error

// Record implements Recorder.
func (f RecorderFunc) Record(ctx context.Context, event *AuditEvent) error {
	return f(ctx, event)
}

// Extension bridges Ledger lifecycle events to an audit trail backend.
type Extension struct {
	recorder Recorder
	enabled  map[string]bool // nil = all enabled
	logger   log.Logger
}

// New creates an Extension that emits audit events through the provided Recorder.
func New(r Recorder, opts ...Option) *Extension {
	e := &Extension{
		recorder: r,
		logger:   log.NewNoopLogger(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name implements plugin.Plugin.
func (e *Extension) Name() string { return "audit-hook" }

// ──────────────────────────────────────────────────
// Plan lifecycle hooks
// ──────────────────────────────────────────────────

// OnPlanCreated implements plugin.OnPlanCreated.
func (e *Extension) OnPlanCreated(ctx context.Context, _ interface{}) error {
	// Would extract plan details from the interface
	return e.record(ctx, ActionPlanCreated, SeverityInfo, OutcomeSuccess,
		ResourcePlan, "", CategoryBilling, nil,
		"event", "plan_created",
	)
}

// OnPlanUpdated implements plugin.OnPlanUpdated.
func (e *Extension) OnPlanUpdated(ctx context.Context, _, _ interface{}) error {
	return e.record(ctx, ActionPlanUpdated, SeverityInfo, OutcomeSuccess,
		ResourcePlan, "", CategoryBilling, nil,
		"event", "plan_updated",
	)
}

// OnPlanArchived implements plugin.OnPlanArchived.
func (e *Extension) OnPlanArchived(ctx context.Context, planID string) error {
	return e.record(ctx, ActionPlanArchived, SeverityInfo, OutcomeSuccess,
		ResourcePlan, planID, CategoryBilling, nil,
		"plan_id", planID,
	)
}

// ──────────────────────────────────────────────────
// Subscription lifecycle hooks
// ──────────────────────────────────────────────────

// OnSubscriptionCreated implements plugin.OnSubscriptionCreated.
func (e *Extension) OnSubscriptionCreated(ctx context.Context, sub interface{}) error {
	return e.record(ctx, ActionSubscriptionCreated, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, subscriptionID(sub), CategorySubscription, nil,
		subscriptionMeta(sub, "event", "subscription_created")...,
	)
}

// OnSubscriptionChanged implements plugin.OnSubscriptionChanged.
func (e *Extension) OnSubscriptionChanged(ctx context.Context, sub, _, _ interface{}) error {
	// Determine if upgrade or downgrade
	action := ActionSubscriptionUpgraded
	// Would need to compare plans to determine actual direction

	return e.record(ctx, action, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, subscriptionID(sub), CategorySubscription, nil,
		subscriptionMeta(sub, "event", "subscription_changed")...,
	)
}

// OnSubscriptionCanceled implements plugin.OnSubscriptionCanceled.
func (e *Extension) OnSubscriptionCanceled(ctx context.Context, sub interface{}) error {
	return e.record(ctx, ActionSubscriptionCanceled, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, subscriptionID(sub), CategorySubscription, nil,
		subscriptionMeta(sub, "event", "subscription_canceled")...,
	)
}

// OnSubscriptionCancelScheduled implements plugin.OnSubscriptionCancelScheduled.
// The operator's action is recorded when it is taken; subscription.canceled
// follows when the cancellation takes effect.
func (e *Extension) OnSubscriptionCancelScheduled(ctx context.Context, sub interface{}) error {
	return e.record(ctx, ActionSubscriptionCancelScheduled, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, subscriptionID(sub), CategorySubscription, nil,
		subscriptionMeta(sub, "event", "subscription_cancel_scheduled")...,
	)
}

// OnSubscriptionRenewed implements plugin.OnSubscriptionRenewed. The lifecycle
// clock fires it, so the context carries no operator; the tenant and app come
// from the subscription.
func (e *Extension) OnSubscriptionRenewed(ctx context.Context, renewal interface{}) error {
	kv := []any{"event", "subscription_renewed"}
	if r, ok := renewal.(*subscription.Renewal); ok && r != nil {
		kv = append(kv, "periods_ended", len(r.Ended))
		if len(r.Ended) > 0 {
			kv = append(kv, "ended_from", r.Ended[0].Start, "ended_to", r.Ended[len(r.Ended)-1].End)
		}
		return e.record(ctx, ActionSubscriptionRenewed, SeverityInfo, OutcomeSuccess,
			ResourceSubscription, subscriptionID(r.Subscription), CategorySubscription, nil,
			subscriptionMeta(r.Subscription, kv...)...,
		)
	}
	return e.record(ctx, ActionSubscriptionRenewed, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, "", CategorySubscription, nil, kv...)
}

// OnSubscriptionTrialEnded implements plugin.OnSubscriptionTrialEnded.
func (e *Extension) OnSubscriptionTrialEnded(ctx context.Context, sub interface{}) error {
	return e.record(ctx, ActionSubscriptionTrialEnded, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, subscriptionID(sub), CategorySubscription, nil,
		subscriptionMeta(sub, "event", "subscription_trial_ended")...,
	)
}

// ──────────────────────────────────────────────────
// Invoice lifecycle hooks
// ──────────────────────────────────────────────────

// OnInvoiceGenerated implements plugin.OnInvoiceGenerated.
func (e *Extension) OnInvoiceGenerated(ctx context.Context, inv interface{}) error {
	return e.record(ctx, ActionInvoiceGenerated, SeverityInfo, OutcomeSuccess,
		ResourceInvoice, invoiceID(inv), CategoryPayment, nil,
		invoiceMeta(inv, "event", "invoice_generated")...,
	)
}

// OnInvoiceFinalized implements plugin.OnInvoiceFinalized.
func (e *Extension) OnInvoiceFinalized(ctx context.Context, inv interface{}) error {
	return e.record(ctx, ActionInvoiceFinalized, SeverityInfo, OutcomeSuccess,
		ResourceInvoice, invoiceID(inv), CategoryPayment, nil,
		invoiceMeta(inv, "event", "invoice_finalized")...,
	)
}

// OnInvoicePaid implements plugin.OnInvoicePaid.
func (e *Extension) OnInvoicePaid(ctx context.Context, inv interface{}) error {
	return e.record(ctx, ActionInvoicePaid, SeverityInfo, OutcomeSuccess,
		ResourceInvoice, invoiceID(inv), CategoryPayment, nil,
		invoiceMeta(inv, "event", "invoice_paid")...,
	)
}

// OnInvoiceFailed implements plugin.OnInvoiceFailed.
func (e *Extension) OnInvoiceFailed(ctx context.Context, inv interface{}, err error) error {
	return e.record(ctx, ActionInvoiceFailed, SeverityCritical, OutcomeFailure,
		ResourceInvoice, invoiceID(inv), CategoryPayment, err,
		invoiceMeta(inv, "event", "invoice_failed")...,
	)
}

// OnInvoiceVoided implements plugin.OnInvoiceVoided.
func (e *Extension) OnInvoiceVoided(ctx context.Context, inv interface{}, reason string) error {
	return e.record(ctx, ActionInvoiceVoided, SeverityWarning, OutcomeSuccess,
		ResourceInvoice, invoiceID(inv), CategoryPayment, nil,
		invoiceMeta(inv, "event", "invoice_voided", "void_reason", reason)...,
	)
}

// OnInvoicePastDue implements plugin.OnInvoicePastDue.
func (e *Extension) OnInvoicePastDue(ctx context.Context, inv interface{}) error {
	return e.record(ctx, ActionInvoicePastDue, SeverityWarning, OutcomeSuccess,
		ResourceInvoice, invoiceID(inv), CategoryPayment, nil,
		invoiceMeta(inv, "event", "invoice_past_due")...,
	)
}

// ──────────────────────────────────────────────────
// Entitlement lifecycle hooks
// ──────────────────────────────────────────────────

// OnQuotaExceeded implements plugin.OnQuotaExceeded.
func (e *Extension) OnQuotaExceeded(ctx context.Context, tenantID, featureKey string, used, limit int64) error {
	return e.record(ctx, ActionQuotaExceeded, SeverityWarning, OutcomeFailure,
		ResourceEntitlement, featureKey, CategoryAccess, nil,
		"tenant_id", tenantID,
		"feature", featureKey,
		"used", used,
		"limit", limit,
	)
}

// OnEntitlementChecked implements plugin.OnEntitlementChecked.
func (e *Extension) OnEntitlementChecked(_ context.Context, _ interface{}) error {
	// Only audit denied checks to reduce noise
	// Would need to inspect result to determine if denied
	return nil
}

// ──────────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────────

// subscriptionID is the id of the subscription a hook received, or "" for
// anything else.
func subscriptionID(v interface{}) string {
	if sub, ok := v.(*subscription.Subscription); ok && sub != nil {
		return sub.ID.String()
	}
	return ""
}

// subscriptionMeta adds the subscription's tenant and app to kv. Events the
// lifecycle clock fires run with no tenant or actor in the context, so this
// is how the trail says whose subscription it was.
func subscriptionMeta(v interface{}, kv ...any) []any {
	if sub, ok := v.(*subscription.Subscription); ok && sub != nil {
		kv = append(kv, "tenant_id", sub.TenantID, "app_id", sub.AppID)
	}
	return kv
}

// invoiceID is the id of the invoice a hook received, or "" for anything
// else.
func invoiceID(v interface{}) string {
	if inv, ok := v.(*invoice.Invoice); ok && inv != nil {
		return inv.ID.String()
	}
	return ""
}

// invoiceMeta adds the invoice's tenant, app and subscription to kv.
func invoiceMeta(v interface{}, kv ...any) []any {
	if inv, ok := v.(*invoice.Invoice); ok && inv != nil {
		kv = append(kv, "tenant_id", inv.TenantID, "app_id", inv.AppID, "subscription_id", inv.SubscriptionID.String())
	}
	return kv
}

// record builds and sends an audit event if the action is enabled.
func (e *Extension) record(
	ctx context.Context,
	action, severity, outcome string,
	resource, resourceID, category string,
	err error,
	kvPairs ...any,
) error {
	if e.enabled != nil && !e.enabled[action] {
		return nil
	}

	meta := make(map[string]any, len(kvPairs)/2+1)
	for i := 0; i+1 < len(kvPairs); i += 2 {
		key, ok := kvPairs[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", kvPairs[i])
		}
		meta[key] = kvPairs[i+1]
	}

	var reason string
	if err != nil {
		reason = err.Error()
		meta["error"] = err.Error()
	}

	evt := &AuditEvent{
		Action:     action,
		Resource:   resource,
		Category:   category,
		ResourceID: resourceID,
		Metadata:   meta,
		Outcome:    outcome,
		Severity:   severity,
		Reason:     reason,
	}

	if recErr := e.recorder.Record(ctx, evt); recErr != nil {
		e.logger.Warn("audit_hook: failed to record audit event",
			log.String("action", action),
			log.String("resource_id", resourceID),
			log.Error(recErr),
		)
	}
	return nil
}
