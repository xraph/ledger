package invoice

import (
	"sort"

	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/types"
)

// SortTiers returns tiers ordered for evaluation: by UpTo ascending, with
// an UpTo of zero treated as unbounded and sorted last, and Priority
// breaking ties.
//
// The input is not mutated. Callers hold tiers that belong to a stored
// plan, and reordering that slice in place would rewrite the plan's own
// view of its pricing.
func SortTiers(tiers []plan.PriceTier) []plan.PriceTier {
	out := make([]plan.PriceTier, len(tiers))
	copy(out, tiers)

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.UpTo == 0 && b.UpTo == 0:
			return a.Priority < b.Priority
		case a.UpTo == 0:
			return false
		case b.UpTo == 0:
			return true
		case a.UpTo != b.UpTo:
			return a.UpTo < b.UpTo
		default:
			return a.Priority < b.Priority
		}
	})

	return out
}

// computeGraduated charges each tier for the units that fall inside its own
// range. 6000 units against a 1000/5000/unbounded ladder pays the first
// tier's rate for 1000 units, the second's for 4000, and the third's for
// the remaining 1000.
//
// tiers must already be sorted by SortTiers.
func computeGraduated(tiers []plan.PriceTier, qty int64, currency string) types.Money {
	total := types.Zero(currency)
	if qty <= 0 {
		return total
	}

	var consumed int64
	for _, t := range tiers {
		if consumed >= qty {
			break
		}

		// The number of units this tier can absorb. An UpTo of zero is
		// unbounded, so it takes everything that is left.
		var capacity int64
		if t.UpTo == 0 {
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

// computeVolume applies the rate of the tier covering the total quantity to
// every unit. 3000 units against a tier covering up to 5000 pays 3000 times
// that tier's rate, not a blend.
//
// tiers must already be sorted by SortTiers.
func computeVolume(tiers []plan.PriceTier, qty int64, currency string) types.Money {
	total := types.Zero(currency)
	if qty <= 0 {
		return total
	}

	for _, t := range tiers {
		if t.UpTo == 0 || qty <= t.UpTo {
			return types.Money{Amount: t.UnitAmount.Amount * qty, Currency: currency}
		}
	}

	return total
}

// computeFlat charges the flat fee attached to the tier the quantity reaches.
// It is a fee for being in a band, not a per-unit rate, so the quantity
// selects a tier and is then discarded.
//
// tiers must already be sorted by SortTiers.
func computeFlat(tiers []plan.PriceTier, qty int64, currency string) types.Money {
	total := types.Zero(currency)
	if qty <= 0 {
		return total
	}

	for _, t := range tiers {
		if t.UpTo == 0 || qty <= t.UpTo {
			return types.Money{Amount: t.FlatAmount.Amount, Currency: currency}
		}
	}

	return total
}

// ComputeOverage prices the billable excess of usage over an included
// allowance, using whichever tier model the plan declares.
//
// All arithmetic is integer. UnitAmount and FlatAmount are whole minor
// units, so a rate below one cent per unit cannot be expressed. Plans that
// need sub-cent unit pricing need a scaled money type, which is a change to
// types.Money and out of scope here.
//
// An empty tier slice prices at zero. A metered feature whose plan declares
// no tiers is a gap in the catalogue, and failing invoice generation over it
// would block billing for every other feature on the plan.
func ComputeOverage(tiers []plan.PriceTier, usage, included int64, currency string) types.Money {
	if len(tiers) == 0 {
		return types.Zero(currency)
	}

	billable := usage - included
	if billable <= 0 {
		return types.Zero(currency)
	}

	sorted := SortTiers(tiers)

	switch sorted[0].Type {
	case plan.TierVolume:
		return computeVolume(sorted, billable, currency)
	case plan.TierFlat:
		return computeFlat(sorted, billable, currency)
	case plan.TierGraduated:
		return computeGraduated(sorted, billable, currency)
	default:
		// An unrecognised tier type prices at zero rather than guessing.
		return types.Zero(currency)
	}
}
