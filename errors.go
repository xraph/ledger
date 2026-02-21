package ledger

import (
	"errors"
	"fmt"
)

// Sentinel errors for common failure scenarios.
var (
	// General errors
	ErrNotFound      = errors.New("ledger: not found")
	ErrAlreadyExists = errors.New("ledger: already exists")
	ErrInvalidInput  = errors.New("ledger: invalid input")
	ErrUnauthorized  = errors.New("ledger: unauthorized")
	ErrForbidden     = errors.New("ledger: forbidden")

	// Plan errors
	ErrPlanNotFound     = errors.New("ledger: plan not found")
	ErrPlanArchived     = errors.New("ledger: plan is archived")
	ErrPlanInUse        = errors.New("ledger: plan is in use by subscriptions")
	ErrFeatureNotFound  = errors.New("ledger: feature not found")
	ErrInvalidPricing   = errors.New("ledger: invalid pricing configuration")
	ErrDuplicateFeature = errors.New("ledger: duplicate feature key")

	// Subscription errors
	ErrSubscriptionNotFound = errors.New("ledger: subscription not found")
	ErrSubscriptionExists   = errors.New("ledger: subscription already exists")
	ErrSubscriptionCanceled = errors.New("ledger: subscription is canceled")
	ErrSubscriptionExpired  = errors.New("ledger: subscription is expired")
	ErrInvalidUpgrade       = errors.New("ledger: invalid plan upgrade")
	ErrInvalidDowngrade     = errors.New("ledger: invalid plan downgrade")
	ErrTrialExpired         = errors.New("ledger: trial period has expired")
	ErrNoActiveSubscription = errors.New("ledger: no active subscription")

	// Metering errors
	ErrMeterBufferFull = errors.New("ledger: meter buffer full")
	ErrInvalidQuantity = errors.New("ledger: invalid usage quantity")
	ErrDuplicateEvent  = errors.New("ledger: duplicate usage event")
	ErrEventTooOld     = errors.New("ledger: usage event too old")

	// Entitlement errors
	ErrQuotaExceeded    = errors.New("ledger: quota exceeded")
	ErrFeatureDisabled  = errors.New("ledger: feature disabled")
	ErrHardLimitReached = errors.New("ledger: hard limit reached")
	ErrSoftLimitReached = errors.New("ledger: soft limit reached (warning)")
	ErrNoEntitlement    = errors.New("ledger: no entitlement for feature")

	// Invoice errors
	ErrInvoiceNotFound   = errors.New("ledger: invoice not found")
	ErrInvoiceFinalized  = errors.New("ledger: invoice already finalized")
	ErrInvoicePaid       = errors.New("ledger: invoice already paid")
	ErrInvoiceVoided     = errors.New("ledger: invoice is voided")
	ErrInvoiceIncomplete = errors.New("ledger: invoice incomplete")
	ErrInvalidDiscount   = errors.New("ledger: invalid discount")

	// Coupon errors
	ErrCouponNotFound   = errors.New("ledger: coupon not found")
	ErrCouponExpired    = errors.New("ledger: coupon expired")
	ErrCouponInvalid    = errors.New("ledger: coupon invalid")
	ErrCouponExhausted  = errors.New("ledger: coupon redemptions exhausted")
	ErrCouponNotStarted = errors.New("ledger: coupon not yet valid")

	// Provider errors
	ErrProviderNotFound      = errors.New("ledger: provider not found")
	ErrProviderSync          = errors.New("ledger: provider sync failed")
	ErrProviderWebhook       = errors.New("ledger: webhook validation failed")
	ErrProviderNotConfigured = errors.New("ledger: provider not configured")

	// Store errors
	ErrStoreNotReady     = errors.New("ledger: store not ready")
	ErrStoreClosed       = errors.New("ledger: store is closed")
	ErrTransactionFailed = errors.New("ledger: transaction failed")
	ErrMigrationFailed   = errors.New("ledger: migration failed")

	// Cache errors
	ErrCacheMiss       = errors.New("ledger: cache miss")
	ErrCacheInvalidate = errors.New("ledger: cache invalidation failed")
)

// ValidationError represents a validation failure with details.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("ledger: validation failed for %s: %s", e.Field, e.Message)
}

// MultiError represents multiple errors that occurred.
type MultiError struct {
	Errors []error
}

func (e MultiError) Error() string {
	if len(e.Errors) == 0 {
		return "ledger: no errors"
	}
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	return fmt.Sprintf("ledger: %d errors occurred", len(e.Errors))
}

// Add adds an error to the multi-error.
func (e *MultiError) Add(err error) {
	if err != nil {
		e.Errors = append(e.Errors, err)
	}
}

// HasErrors returns true if there are any errors.
func (e MultiError) HasErrors() bool {
	return len(e.Errors) > 0
}

// First returns the first error or nil.
func (e MultiError) First() error {
	if len(e.Errors) > 0 {
		return e.Errors[0]
	}
	return nil
}

// IsNotFound returns true if the error is a not found error.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrPlanNotFound) ||
		errors.Is(err, ErrSubscriptionNotFound) ||
		errors.Is(err, ErrFeatureNotFound) ||
		errors.Is(err, ErrInvoiceNotFound) ||
		errors.Is(err, ErrCouponNotFound)
}

// IsQuotaError returns true if the error is related to quota/limits.
func IsQuotaError(err error) bool {
	return errors.Is(err, ErrQuotaExceeded) ||
		errors.Is(err, ErrHardLimitReached) ||
		errors.Is(err, ErrSoftLimitReached) ||
		errors.Is(err, ErrNoEntitlement) ||
		errors.Is(err, ErrFeatureDisabled)
}

// IsRetryable returns true if the error is temporary and the operation can be retried.
func IsRetryable(err error) bool {
	return errors.Is(err, ErrMeterBufferFull) ||
		errors.Is(err, ErrStoreNotReady) ||
		errors.Is(err, ErrTransactionFailed) ||
		errors.Is(err, ErrProviderSync) ||
		errors.Is(err, ErrCacheInvalidate)
}
