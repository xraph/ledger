package audithook

// Action constants for audit events.
const (
	// Plan actions
	ActionPlanCreated  = "plan.created"
	ActionPlanUpdated  = "plan.updated"
	ActionPlanArchived = "plan.archived"

	// Subscription actions
	ActionSubscriptionCreated    = "subscription.created"
	ActionSubscriptionUpgraded   = "subscription.upgraded"
	ActionSubscriptionDowngraded = "subscription.downgraded"
	ActionSubscriptionCanceled   = "subscription.canceled"
	ActionSubscriptionExpired    = "subscription.expired"

	// Usage actions
	ActionUsageIngested = "usage.ingested"
	ActionUsageFlushed  = "usage.flushed"

	// Entitlement actions
	ActionEntitlementChecked = "entitlement.checked"
	ActionEntitlementDenied  = "entitlement.denied"
	ActionQuotaExceeded      = "quota.exceeded"
	ActionSoftLimitReached   = "soft_limit.reached"

	// Invoice actions
	ActionInvoiceGenerated = "invoice.generated"
	ActionInvoiceFinalized = "invoice.finalized"
	ActionInvoicePaid      = "invoice.paid"
	ActionInvoiceFailed    = "invoice.failed"
	ActionInvoiceVoided    = "invoice.voided"

	// Provider actions
	ActionProviderSync     = "provider.sync"
	ActionWebhookReceived  = "webhook.received"
	ActionWebhookProcessed = "webhook.processed"
)

// Resource constants for audit events.
const (
	ResourcePlan         = "plan"
	ResourceSubscription = "subscription"
	ResourceUsage        = "usage"
	ResourceEntitlement  = "entitlement"
	ResourceInvoice      = "invoice"
	ResourceProvider     = "provider"
	ResourceWebhook      = "webhook"
)

// Category constants for audit events.
const (
	CategoryBilling      = "billing"
	CategorySubscription = "subscription"
	CategoryUsage        = "usage"
	CategoryAccess       = "access"
	CategoryPayment      = "payment"
	CategoryIntegration  = "integration"
)

// Severity levels for audit events.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityError    = "error"
	SeverityCritical = "critical"
)

// Outcome values for audit events.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomePartial = "partial"
)
