// Package audithook bridges Ledger lifecycle events to an audit trail backend.
//
// It defines a local Recorder interface so the package does not import
// Chronicle directly. Callers inject a RecorderFunc adapter that bridges
// to Chronicle at wiring time.
package audithook

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/xraph/ledger/plugin"
)

// Compile-time interface checks.
var (
	_ plugin.Plugin                 = (*Extension)(nil)
	_ plugin.OnPlanCreated          = (*Extension)(nil)
	_ plugin.OnPlanUpdated          = (*Extension)(nil)
	_ plugin.OnPlanArchived         = (*Extension)(nil)
	_ plugin.OnSubscriptionCreated  = (*Extension)(nil)
	_ plugin.OnSubscriptionChanged  = (*Extension)(nil)
	_ plugin.OnSubscriptionCanceled = (*Extension)(nil)
	_ plugin.OnInvoiceGenerated     = (*Extension)(nil)
	_ plugin.OnInvoiceFinalized     = (*Extension)(nil)
	_ plugin.OnInvoicePaid          = (*Extension)(nil)
	_ plugin.OnInvoiceFailed        = (*Extension)(nil)
	_ plugin.OnInvoiceVoided        = (*Extension)(nil)
	_ plugin.OnQuotaExceeded        = (*Extension)(nil)
	_ plugin.OnEntitlementChecked   = (*Extension)(nil)
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
	logger   *slog.Logger
}

// New creates an Extension that emits audit events through the provided Recorder.
func New(r Recorder, opts ...Option) *Extension {
	e := &Extension{
		recorder: r,
		logger:   slog.Default(),
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
func (e *Extension) OnSubscriptionCreated(ctx context.Context, _ interface{}) error {
	return e.record(ctx, ActionSubscriptionCreated, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, "", CategorySubscription, nil,
		"event", "subscription_created",
	)
}

// OnSubscriptionChanged implements plugin.OnSubscriptionChanged.
func (e *Extension) OnSubscriptionChanged(ctx context.Context, _, _, _ interface{}) error {
	// Determine if upgrade or downgrade
	action := ActionSubscriptionUpgraded
	// Would need to compare plans to determine actual direction

	return e.record(ctx, action, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, "", CategorySubscription, nil,
		"event", "subscription_changed",
	)
}

// OnSubscriptionCanceled implements plugin.OnSubscriptionCanceled.
func (e *Extension) OnSubscriptionCanceled(ctx context.Context, _ interface{}) error {
	return e.record(ctx, ActionSubscriptionCanceled, SeverityInfo, OutcomeSuccess,
		ResourceSubscription, "", CategorySubscription, nil,
		"event", "subscription_canceled",
	)
}

// ──────────────────────────────────────────────────
// Invoice lifecycle hooks
// ──────────────────────────────────────────────────

// OnInvoiceGenerated implements plugin.OnInvoiceGenerated.
func (e *Extension) OnInvoiceGenerated(ctx context.Context, _ interface{}) error {
	return e.record(ctx, ActionInvoiceGenerated, SeverityInfo, OutcomeSuccess,
		ResourceInvoice, "", CategoryPayment, nil,
		"event", "invoice_generated",
	)
}

// OnInvoiceFinalized implements plugin.OnInvoiceFinalized.
func (e *Extension) OnInvoiceFinalized(ctx context.Context, _ interface{}) error {
	return e.record(ctx, ActionInvoiceFinalized, SeverityInfo, OutcomeSuccess,
		ResourceInvoice, "", CategoryPayment, nil,
		"event", "invoice_finalized",
	)
}

// OnInvoicePaid implements plugin.OnInvoicePaid.
func (e *Extension) OnInvoicePaid(ctx context.Context, _ interface{}) error {
	return e.record(ctx, ActionInvoicePaid, SeverityInfo, OutcomeSuccess,
		ResourceInvoice, "", CategoryPayment, nil,
		"event", "invoice_paid",
	)
}

// OnInvoiceFailed implements plugin.OnInvoiceFailed.
func (e *Extension) OnInvoiceFailed(ctx context.Context, _ interface{}, err error) error {
	return e.record(ctx, ActionInvoiceFailed, SeverityCritical, OutcomeFailure,
		ResourceInvoice, "", CategoryPayment, err,
		"event", "invoice_failed",
	)
}

// OnInvoiceVoided implements plugin.OnInvoiceVoided.
func (e *Extension) OnInvoiceVoided(ctx context.Context, _ interface{}, reason string) error {
	return e.record(ctx, ActionInvoiceVoided, SeverityWarning, OutcomeSuccess,
		ResourceInvoice, "", CategoryPayment, nil,
		"event", "invoice_voided",
		"void_reason", reason,
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
			"action", action,
			"resource_id", resourceID,
			"error", recErr,
		)
	}
	return nil
}
