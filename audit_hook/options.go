package audithook

import "log/slog"

// Option configures an Extension.
type Option func(*Extension)

// WithLogger sets the logger for the extension.
func WithLogger(logger *slog.Logger) Option {
	return func(e *Extension) {
		e.logger = logger
	}
}

// WithEnabledActions sets which actions to audit.
// If not called, all actions are audited.
func WithEnabledActions(actions ...string) Option {
	return func(e *Extension) {
		e.enabled = make(map[string]bool)
		for _, action := range actions {
			e.enabled[action] = true
		}
	}
}

// WithDisabledActions sets which actions to skip.
func WithDisabledActions(actions ...string) Option {
	return func(e *Extension) {
		if e.enabled == nil {
			// Start with all enabled
			e.enabled = make(map[string]bool)
			// Add all known actions
			for _, action := range allActions() {
				e.enabled[action] = true
			}
		}
		// Disable specified actions
		for _, action := range actions {
			delete(e.enabled, action)
		}
	}
}

// allActions returns all known audit actions.
func allActions() []string {
	return []string{
		ActionPlanCreated,
		ActionPlanUpdated,
		ActionPlanArchived,
		ActionSubscriptionCreated,
		ActionSubscriptionUpgraded,
		ActionSubscriptionDowngraded,
		ActionSubscriptionCanceled,
		ActionSubscriptionExpired,
		ActionUsageIngested,
		ActionUsageFlushed,
		ActionEntitlementChecked,
		ActionEntitlementDenied,
		ActionQuotaExceeded,
		ActionSoftLimitReached,
		ActionInvoiceGenerated,
		ActionInvoiceFinalized,
		ActionInvoicePaid,
		ActionInvoiceFailed,
		ActionInvoiceVoided,
		ActionProviderSync,
		ActionWebhookReceived,
		ActionWebhookProcessed,
	}
}
