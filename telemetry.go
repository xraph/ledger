package ledger

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/trace"

	gometrics "github.com/xraph/go-utils/metrics"

	"github.com/xraph/ledger/internal/telemetry"
	"github.com/xraph/ledger/types"
)

// WithMetrics records the engine's metrics on m: lifecycle runs and
// transitions, metered events, entitlement checks, invoice generation,
// payment provider calls and plugin hook failures. In a Forge app pass
// app.Metrics(); the Forge extension does that for you. A nil m, the default,
// records nothing.
//
// Spans need no option. The engine starts them on the global OpenTelemetry
// tracer provider, which forge.New installs.
func WithMetrics(m gometrics.Metrics) Option {
	return func(l *Ledger) {
		l.metricsSink = m
	}
}

// Metric names, as registered. Forge's Prometheus scrape adds its forge_
// namespace in front, so ledger_meter_events_total scrapes as
// forge_ledger_meter_events_total.
const (
	metricLifecycleRunDuration = "ledger_lifecycle_run_duration_seconds"
	metricLifecycleTransitions = "ledger_lifecycle_transitions_total"
	metricMeterEvents          = "ledger_meter_events_total"
	metricEntitlementChecks    = "ledger_entitlement_checks_total"
	metricInvoiceGenerate      = "ledger_invoice_generate_duration_seconds"
	metricProviderCallDuration = "ledger_provider_call_duration_seconds"
)

// Label values. Every label is a closed set; nothing a caller passes in
// becomes one, except a payment provider's Name, which is bounded by the
// plugins compiled in.
const (
	resultOK         = "ok"
	resultError      = "error"
	resultPanic      = "panic"
	resultStopped    = "stopped"
	resultBufferFull = "buffer_full"
	resultExists     = "exists"
	resultNotFound   = "not_found"
	resultAllowed    = "allowed"
	resultDenied     = "denied"
	resultNoAccess   = "no_access"

	cacheHit  = "hit"
	cacheMiss = "miss"
)

// engineMetrics holds every metric the engine records. Built once in New; with
// no sink every field is nil, and a nil vec records nothing.
type engineMetrics struct {
	lifecycleRuns        *telemetry.HistogramVec // result
	lifecycleTransitions *telemetry.CounterVec   // step, result
	meterEvents          *telemetry.CounterVec   // result
	entitlementChecks    *telemetry.CounterVec   // result, cache
	invoiceGenerate      *telemetry.HistogramVec // result
	providerCalls        *telemetry.HistogramVec // provider, operation, result
}

func newEngineMetrics(m gometrics.Metrics) engineMetrics {
	return engineMetrics{
		lifecycleRuns: telemetry.NewHistogramVec(m, metricLifecycleRunDuration,
			"Duration of one lifecycle clock run (Advance).",
			[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
			"result"),
		lifecycleTransitions: telemetry.NewCounterVec(m, metricLifecycleTransitions,
			"Rows a lifecycle step moved, or failed to move.",
			"step", "result"),
		meterEvents: telemetry.NewCounterVec(m, metricMeterEvents,
			"Usage events by what became of them: stored, lost to a failed flush, or refused by a full buffer.",
			"result"),
		entitlementChecks: telemetry.NewCounterVec(m, metricEntitlementChecks,
			"Entitlement checks by answer and by whether the cache answered.",
			"result", "cache"),
		invoiceGenerate: telemetry.NewHistogramVec(m, metricInvoiceGenerate,
			"Duration of GenerateInvoice.",
			[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			"result"),
		providerCalls: telemetry.NewHistogramVec(m, metricProviderCallDuration,
			"Duration of one call into a payment provider plugin.",
			[]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			"provider", "operation", "result"),
	}
}

// Span attribute keys.
const (
	attrBatchSize        = "ledger.meter.batch_size"
	attrFeatureKey       = "ledger.feature.key"
	attrEntitlement      = "ledger.entitlement.result"
	attrCacheHit         = "ledger.entitlement.cache_hit"
	attrSubscriptionID   = "ledger.subscription.id"
	attrInvoiceID        = "ledger.invoice.id"
	attrInvoicePeriod    = "ledger.invoice.period"
	attrInvoiceLineItems = "ledger.invoice.line_items"
	attrInvoiceResult    = "ledger.invoice.result"
	attrPlanID           = "ledger.plan.id"
	attrFeatureID        = "ledger.feature.id"
	attrProviderName     = "ledger.provider.name"
	attrWebhookEvent     = "ledger.webhook.event_type"
	attrWebhookHandled   = "ledger.webhook.handled"
	attrStepChanged      = "ledger.lifecycle.changed"
	attrStepFailed       = "ledger.lifecycle.failed"
	attrPeriodsAdvanced  = "ledger.lifecycle.periods_advanced"
	attrCancelsEnacted   = "ledger.lifecycle.cancels_enacted"
	attrTrialsEnded      = "ledger.lifecycle.trials_ended"
	attrInvoicesPastDue  = "ledger.lifecycle.invoices_past_due"
)

// endSpan ends span, marking it failed when err is one. A missing record or
// an invoice that already exists is an answer, not a failure, and is left
// unmarked.
func endSpan(span trace.Span, err error) {
	if err != nil && !IsNotFound(err) && !errors.Is(err, ErrAlreadyExists) {
		telemetry.Fail(span, errorType(err))
	}
	span.End()
}

// sentinels are the errors errorType names, in the order it tries them.
var sentinels = []struct {
	err  error
	name string
}{
	{ErrNotFound, "ledger.ErrNotFound"},
	{ErrAlreadyExists, "ledger.ErrAlreadyExists"},
	{ErrInvalidInput, "ledger.ErrInvalidInput"},
	{ErrUnauthorized, "ledger.ErrUnauthorized"},
	{ErrForbidden, "ledger.ErrForbidden"},
	{ErrPlanNotFound, "ledger.ErrPlanNotFound"},
	{ErrPlanArchived, "ledger.ErrPlanArchived"},
	{ErrPlanInUse, "ledger.ErrPlanInUse"},
	{ErrFeatureNotFound, "ledger.ErrFeatureNotFound"},
	{ErrFeatureArchived, "ledger.ErrFeatureArchived"},
	{ErrInvalidPricing, "ledger.ErrInvalidPricing"},
	{ErrDuplicateFeature, "ledger.ErrDuplicateFeature"},
	{ErrSubscriptionNotFound, "ledger.ErrSubscriptionNotFound"},
	{ErrSubscriptionExists, "ledger.ErrSubscriptionExists"},
	{ErrSubscriptionCanceled, "ledger.ErrSubscriptionCanceled"},
	{ErrSubscriptionExpired, "ledger.ErrSubscriptionExpired"},
	{ErrInvalidUpgrade, "ledger.ErrInvalidUpgrade"},
	{ErrInvalidDowngrade, "ledger.ErrInvalidDowngrade"},
	{ErrTrialExpired, "ledger.ErrTrialExpired"},
	{ErrNoActiveSubscription, "ledger.ErrNoActiveSubscription"},
	{ErrMeterBufferFull, "ledger.ErrMeterBufferFull"},
	{ErrInvalidQuantity, "ledger.ErrInvalidQuantity"},
	{ErrDuplicateEvent, "ledger.ErrDuplicateEvent"},
	{ErrEventTooOld, "ledger.ErrEventTooOld"},
	{ErrQuotaExceeded, "ledger.ErrQuotaExceeded"},
	{ErrFeatureDisabled, "ledger.ErrFeatureDisabled"},
	{ErrHardLimitReached, "ledger.ErrHardLimitReached"},
	{ErrSoftLimitReached, "ledger.ErrSoftLimitReached"},
	{ErrNoEntitlement, "ledger.ErrNoEntitlement"},
	{ErrInvoiceNotFound, "ledger.ErrInvoiceNotFound"},
	{ErrInvoiceFinalized, "ledger.ErrInvoiceFinalized"},
	{ErrInvoicePaid, "ledger.ErrInvoicePaid"},
	{ErrInvoiceVoided, "ledger.ErrInvoiceVoided"},
	{ErrInvoiceIncomplete, "ledger.ErrInvoiceIncomplete"},
	{ErrInvalidDiscount, "ledger.ErrInvalidDiscount"},
	{ErrCouponNotFound, "ledger.ErrCouponNotFound"},
	{ErrCouponExpired, "ledger.ErrCouponExpired"},
	{ErrCouponInvalid, "ledger.ErrCouponInvalid"},
	{ErrCouponExhausted, "ledger.ErrCouponExhausted"},
	{ErrCouponNotStarted, "ledger.ErrCouponNotStarted"},
	{ErrCouponAlreadyApplied, "ledger.ErrCouponAlreadyApplied"},
	{ErrProviderNotFound, "ledger.ErrProviderNotFound"},
	{ErrProviderSync, "ledger.ErrProviderSync"},
	{ErrProviderWebhook, "ledger.ErrProviderWebhook"},
	{ErrProviderNotConfigured, "ledger.ErrProviderNotConfigured"},
	{ErrStoreNotReady, "ledger.ErrStoreNotReady"},
	{ErrStoreClosed, "ledger.ErrStoreClosed"},
	{ErrTransactionFailed, "ledger.ErrTransactionFailed"},
	{ErrMigrationFailed, "ledger.ErrMigrationFailed"},
	{ErrCacheMiss, "ledger.ErrCacheMiss"},
	{ErrCacheInvalidate, "ledger.ErrCacheInvalidate"},
	{types.ErrOverflow, "types.ErrOverflow"},
}

// errorType names err's class for a span: a context error or a ledger
// sentinel by name, anything else by the Go type at the bottom of its chain.
// It never returns the message.
func errorType(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "context.Canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context.DeadlineExceeded"
	}
	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	return telemetry.TypeOf(err)
}

// resultOf is the result label for a call that returned err.
func resultOf(err error) string {
	if err != nil {
		return resultError
	}
	return resultOK
}
