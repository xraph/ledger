package contract

import (
	"errors"
	"fmt"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/types"
)

func notFound(what string) error {
	return &dash.Error{Code: dash.CodeNotFound, Message: what + " not found"}
}

func badRequest(format string, args ...any) error {
	return &dash.Error{Code: dash.CodeBadRequest, Message: fmt.Sprintf(format, args...)}
}

func conflict(format string, args ...any) error {
	return &dash.Error{Code: dash.CodeConflict, Message: fmt.Sprintf(format, args...)}
}

var (
	notFoundErrs = []error{
		ledger.ErrNotFound, ledger.ErrPlanNotFound, ledger.ErrFeatureNotFound,
		ledger.ErrSubscriptionNotFound, ledger.ErrInvoiceNotFound, ledger.ErrCouponNotFound,
		ledger.ErrNoActiveSubscription,
	}
	badRequestErrs = []error{
		ledger.ErrInvalidInput, ledger.ErrCouponInvalid, ledger.ErrCouponExpired,
		ledger.ErrCouponNotStarted, ledger.ErrInvalidPricing, ledger.ErrDuplicateFeature,
		ledger.ErrInvalidDiscount, ledger.ErrInvalidQuantity, invoice.ErrInvalidTiers,
		types.ErrOverflow,
	}
	conflictErrs = []error{
		ledger.ErrAlreadyExists, ledger.ErrCouponAlreadyApplied, ledger.ErrCouponExhausted,
		ledger.ErrPlanInUse, ledger.ErrPlanArchived, ledger.ErrFeatureArchived,
		ledger.ErrSubscriptionExists, ledger.ErrSubscriptionCanceled, ledger.ErrSubscriptionExpired,
		ledger.ErrInvoiceFinalized, ledger.ErrInvoicePaid, ledger.ErrInvoiceVoided,
	}
	unavailableErrs = []error{
		ledger.ErrProviderNotConfigured, ledger.ErrProviderNotFound, ledger.ErrStoreNotReady,
		ledger.ErrStoreClosed,
	}
)

func isAny(err error, targets []error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}

// toContractError translates an engine error to a contract error. It is the
// only place that mapping lives. An internal error never echoes its text to
// the client, since driver errors can carry hostnames and user names.
func toContractError(err error) error {
	if err == nil {
		return nil
	}

	var ce *dash.Error
	if errors.As(err, &ce) {
		return ce
	}

	switch {
	case isAny(err, notFoundErrs):
		return &dash.Error{Code: dash.CodeNotFound, Message: err.Error()}
	case isAny(err, badRequestErrs):
		return &dash.Error{Code: dash.CodeBadRequest, Message: err.Error()}
	case isAny(err, conflictErrs):
		return &dash.Error{Code: dash.CodeConflict, Message: err.Error()}
	case isAny(err, unavailableErrs):
		return &dash.Error{Code: dash.CodeUnavailable, Message: err.Error()}
	default:
		return &dash.Error{Code: dash.CodeInternal, Message: "internal error"}
	}
}
