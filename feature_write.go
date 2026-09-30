package ledger

import (
	"fmt"
	"strings"

	"github.com/xraph/ledger/feature"
)

// ValidateFeature checks the shape of a catalog feature: a key, a known type
// and period, and a default limit of -1 (unlimited) or more. The feature
// writes themselves store whatever they are given, so the dashboard contract
// and the provider imports call this first. A problem wraps ErrInvalidInput,
// and the text after its prefix says what is wrong. It does not look at the
// status: a draft feature is a valid shape, and each caller decides which
// statuses it accepts.
func ValidateFeature(f *feature.Feature) error {
	if strings.TrimSpace(f.Key) == "" {
		return fmt.Errorf("%w: a feature needs a key", ErrInvalidInput)
	}
	switch f.Type {
	case feature.FeatureMetered, feature.FeatureBoolean, feature.FeatureSeat:
	default:
		return fmt.Errorf("%w: unknown feature type %q", ErrInvalidInput, f.Type)
	}
	switch f.Period {
	case "", feature.PeriodMonthly, feature.PeriodYearly, feature.PeriodNone:
	default:
		return fmt.Errorf("%w: unknown feature period %q", ErrInvalidInput, f.Period)
	}
	if f.DefaultLimit < -1 {
		return fmt.Errorf("%w: default_limit %d is below -1; use -1 for unlimited", ErrInvalidInput, f.DefaultLimit)
	}
	return nil
}
