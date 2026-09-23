package invoice

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/types"
)

// isUnbounded reports whether an UpTo value marks a tier as unbounded.
// The schema's own zero value means unbounded, but the repository's public
// documentation (README.md, docs/API.md, docs_test.go, and five pages under
// docs/content) uses -1. Both, and anything else non-positive, mean the
// same thing: this tier has no ceiling.
func isUnbounded(upTo int64) bool {
	return upTo <= 0
}

// SortTiers returns tiers ordered for evaluation: by UpTo ascending, with
// any UpTo <= 0 (0 in this package's own tests, -1 in the documented
// schema) treated as unbounded and sorted last, and Priority breaking ties.
//
// The input is not mutated. Callers hold tiers that belong to a stored
// plan, and reordering that slice in place would rewrite the plan's own
// view of its pricing.
func SortTiers(tiers []plan.PriceTier) []plan.PriceTier {
	out := make([]plan.PriceTier, len(tiers))
	copy(out, tiers)

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		aUnbounded, bUnbounded := isUnbounded(a.UpTo), isUnbounded(b.UpTo)
		switch {
		case aUnbounded && bUnbounded:
			return a.Priority < b.Priority
		case aUnbounded:
			return false
		case bUnbounded:
			return true
		case a.UpTo != b.UpTo:
			return a.UpTo < b.UpTo
		default:
			return a.Priority < b.Priority
		}
	})

	return out
}

// tierAt returns the tier that a total quantity of qty falls into. The last
// tier in a sorted ladder always matches, even when it is not marked
// unbounded: a ladder with no unbounded tier does not stop pricing at its
// highest UpTo, it extends its last tier to cover everything above it.
//
// tiers must already be sorted by SortTiers and non-empty; an empty slice
// returns the zero plan.PriceTier rather than panicking, since every caller
// in this file already guards on len(tiers) == 0 before reaching here.
func tierAt(tiers []plan.PriceTier, qty int64) plan.PriceTier {
	for i, t := range tiers {
		if isUnbounded(t.UpTo) || i == len(tiers)-1 || qty <= t.UpTo {
			return t
		}
	}
	return plan.PriceTier{}
}

// computeGraduated charges each tier for the units that fall inside its own
// range. 6000 units against a 1000/5000/unbounded ladder pays the first
// tier's rate for 1000 units, the second's for 4000, and the third's for
// the remaining 1000. A ladder with no unbounded tier extends its last tier
// to cover everything above its highest UpTo instead of pricing it at zero.
//
// tiers must already be sorted by SortTiers.
func computeGraduated(tiers []plan.PriceTier, qty int64, currency string) types.Money {
	total := types.Zero(currency)
	if qty <= 0 {
		return total
	}

	var consumed int64
	for i, t := range tiers {
		if consumed >= qty {
			break
		}

		// The number of units this tier can absorb. An unbounded tier, or
		// the last tier when nothing above it is unbounded, takes
		// everything that is left.
		var capacity int64
		if isUnbounded(t.UpTo) || i == len(tiers)-1 {
			capacity = qty - consumed
		} else {
			capacity = t.UpTo - consumed
		}
		if capacity <= 0 {
			continue
		}

		units := capacity
		if remaining := qty - consumed; remaining < units {
			units = remaining
		}

		total = total.Add(types.Money{
			Amount:   t.UnitAmount.Amount * units,
			Currency: currency,
		})
		consumed += units
	}

	return total
}

// computeVolume applies the rate of the tier covering TOTAL usage to the
// billable units (usage - included). 3000 total usage against a tier
// covering up to 5000, with no allowance, pays 3000 times that tier's rate,
// not a blend. A ladder with no unbounded tier extends its last tier's rate
// to any quantity above its highest UpTo.
//
// tiers must already be sorted by SortTiers. This is the one function
// ComputeOverage calls for the volume model, so a bug here is caught by
// both this package's direct computeVolume tests and by ComputeOverage's
// own volume-dispatch tests, instead of production silently bypassing it.
func computeVolume(tiers []plan.PriceTier, usage, included int64, currency string) types.Money {
	if usage <= 0 || len(tiers) == 0 {
		return types.Zero(currency)
	}

	billable := usage - included
	if billable <= 0 {
		return types.Zero(currency)
	}

	rate := tierAt(tiers, usage).UnitAmount.Amount
	return types.Money{Amount: rate * billable, Currency: currency}
}

// computeFlat charges the flat fee attached to the tier the quantity reaches.
// It is a fee for being in a band, not a per-unit rate, so the quantity
// selects a tier and is then discarded. A ladder with no unbounded tier
// extends its last tier's fee to any quantity above its highest UpTo.
//
// tiers must already be sorted by SortTiers.
func computeFlat(tiers []plan.PriceTier, qty int64, currency string) types.Money {
	if qty <= 0 || len(tiers) == 0 {
		return types.Zero(currency)
	}

	return types.Money{Amount: tierAt(tiers, qty).FlatAmount.Amount, Currency: currency}
}

// ComputeOverage prices the billable excess of usage over an included
// allowance, using whichever tier model the plan declares.
//
// currency is normalised to lowercase once, here, at entry. Every Money
// this function and its helpers construct downstream uses that normalised
// value, so a caller passing "USD" gets an amount back in "usd" rather than
// hitting the currency-mismatch panic in types.Money.Add/Subtract.
//
// UpTo counts TOTAL period usage, not units past the allowance: a plan that
// prices its allowance as a $0 first tier (as this package's README example
// does) needs `usage`, not `usage - included`, to land in the right tier,
// or the allowance would be priced twice. Concretely:
//   - graduated: price(usage) - price(included). Each unit is charged at
//     the rate of the tier position it occupies in the ladder; billing only
//     the delta between the two totals prices the included units once, at
//     graduated's per-position rate, not again on top of it.
//   - volume: the tier reached by TOTAL usage sets the rate, applied to
//     usage - included units.
//   - flat: max(0, fee(usage) - fee(included)), where fee(q) is the
//     FlatAmount of the tier reached by total quantity q. Charging the
//     whole band fee for any usage past included would re-bill the band
//     the allowance already sat in: included 3000 with usage 3001 must not
//     bill a fresh fee for one unit inside a band already paid for.
//
// A ladder with no unbounded tier does not stop pricing at its highest
// UpTo: the last tier after sorting extends to cover everything above it
// (see computeGraduated, computeVolume, computeFlat, and tierAt).
//
// included < 0 means an unlimited allowance, matching plan.Feature.Limit ==
// -1, and prices at zero regardless of usage. usage <= included also prices
// at zero.
//
// An empty tier slice prices at zero: a metered feature whose plan declares
// no tiers is a gap in the catalogue, not a billing failure. Callers must
// call ValidateTiers before ComputeOverage, which cannot return an error
// and prices an invalid ladder by best effort; the default branch below,
// for a tier type ValidateTiers would have rejected, also prices at zero,
// but ValidateTiers is what is meant to keep callers from reaching it.
func ComputeOverage(tiers []plan.PriceTier, usage, included int64, currency string) types.Money {
	currency = strings.ToLower(currency)

	if len(tiers) == 0 {
		return types.Zero(currency)
	}
	if included < 0 {
		return types.Zero(currency)
	}
	if usage <= included {
		return types.Zero(currency)
	}

	sorted := SortTiers(tiers)

	switch sorted[0].Type {
	case plan.TierVolume:
		return computeVolume(sorted, usage, included, currency)
	case plan.TierFlat:
		// R9: flat is a differential exactly like graduated. Charging the
		// full band fee for usage would double-charge for the band the
		// allowance already sat in: included 3000 with usage 3001 must not
		// bill a whole new $20 fee for crossing one unit inside a band the
		// allowance already paid for.
		fee := computeFlat(sorted, usage, currency).Subtract(computeFlat(sorted, included, currency))
		return fee.Max(types.Zero(currency))
	case plan.TierGraduated:
		return computeGraduated(sorted, usage, currency).Subtract(computeGraduated(sorted, included, currency))
	default:
		// An unrecognised tier type prices at zero rather than guessing.
		// ValidateTiers rejects this before ComputeOverage is ever called
		// with it; this branch only guards a caller who skipped that step.
		return types.Zero(currency)
	}
}

// ErrInvalidTiers is wrapped by every ValidateTiers failure.
var ErrInvalidTiers = errors.New("invoice: invalid price tiers")

// ValidateTiers reports whether one feature's tiers can be priced
// unambiguously in the given currency. Callers must validate before
// ComputeOverage, which cannot return an error and prices an invalid ladder
// by best effort.
//
// Every returned error names the offending tier by its index in the slice
// and its UpTo, not only its FeatureKey: a plan with several tiers sharing
// one feature key would otherwise leave the caller guessing which tier is
// bad.
//
// An empty slice is valid: a metered feature with no tiers yet is a
// catalogue gap for ComputeOverage to price at zero, not a validation
// failure. Otherwise ValidateTiers rejects a ladder where:
//  1. the tiers do not all share one Type;
//  2. any Type is empty or not one of graduated, volume, or flat;
//  3. any tier's UnitAmount.Currency or FlatAmount.Currency is non-empty
//     and differs from currency, case-insensitively;
//  4. the tiers carry more than one distinct FeatureKey;
//  5. two tiers have the same UpTo, treating every UpTo <= 0 as the same
//     unbounded value (so a 0 and a -1 together are a duplicate);
//  6. a graduated or volume tier has a non-zero FlatAmount, or a flat tier
//     has a non-zero UnitAmount;
//  7. any tier's UnitAmount.Amount or FlatAmount.Amount is negative. A
//     negative rate would quietly reduce an invoice instead of erroring.
func ValidateTiers(tiers []plan.PriceTier, currency string) error {
	if len(tiers) == 0 {
		return nil
	}

	currency = strings.ToLower(currency)
	wantType := tiers[0].Type
	wantFeatureKey := tiers[0].FeatureKey
	seenUpTo := make(map[int64]bool, len(tiers))

	for idx, t := range tiers {
		if t.Type != wantType {
			return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) mixes types %q and %q",
				ErrInvalidTiers, idx, t.UpTo, t.FeatureKey, wantType, t.Type)
		}

		switch t.Type {
		case plan.TierGraduated, plan.TierVolume, plan.TierFlat:
		default:
			return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) has invalid type %q",
				ErrInvalidTiers, idx, t.UpTo, t.FeatureKey, t.Type)
		}

		if t.UnitAmount.Currency != "" && !strings.EqualFold(t.UnitAmount.Currency, currency) {
			return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) unit amount currency %q does not match %q",
				ErrInvalidTiers, idx, t.UpTo, t.FeatureKey, t.UnitAmount.Currency, currency)
		}
		if t.FlatAmount.Currency != "" && !strings.EqualFold(t.FlatAmount.Currency, currency) {
			return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) flat amount currency %q does not match %q",
				ErrInvalidTiers, idx, t.UpTo, t.FeatureKey, t.FlatAmount.Currency, currency)
		}

		if t.UnitAmount.Amount < 0 {
			return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) has a negative unit amount %d",
				ErrInvalidTiers, idx, t.UpTo, t.FeatureKey, t.UnitAmount.Amount)
		}
		if t.FlatAmount.Amount < 0 {
			return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) has a negative flat amount %d",
				ErrInvalidTiers, idx, t.UpTo, t.FeatureKey, t.FlatAmount.Amount)
		}

		if t.FeatureKey != wantFeatureKey {
			return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) does not match feature key %q",
				ErrInvalidTiers, idx, t.UpTo, t.FeatureKey, wantFeatureKey)
		}

		upToKey := t.UpTo
		if isUnbounded(upToKey) {
			upToKey = 0 // every non-positive UpTo shares one bucket
		}
		if seenUpTo[upToKey] {
			return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) duplicates another tier's UpTo",
				ErrInvalidTiers, idx, t.UpTo, t.FeatureKey)
		}
		seenUpTo[upToKey] = true

		switch t.Type {
		case plan.TierGraduated, plan.TierVolume:
			if t.FlatAmount.Amount != 0 {
				return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) is %s but carries a non-zero flat amount",
					ErrInvalidTiers, idx, t.UpTo, t.FeatureKey, t.Type)
			}
		case plan.TierFlat:
			if t.UnitAmount.Amount != 0 {
				return fmt.Errorf("%w: tier %d (UpTo %d, feature %q) is flat but carries a non-zero unit amount",
					ErrInvalidTiers, idx, t.UpTo, t.FeatureKey)
			}
		}
	}

	return nil
}
