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
