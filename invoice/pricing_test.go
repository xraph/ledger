package invoice

import (
	"testing"

	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/types"
)

// tier is a terse constructor so the tables below read as pricing rather
// than as struct literals.
func tier(upTo int64, unitCents int64, priority int) plan.PriceTier {
	return plan.PriceTier{
		FeatureKey: "api_calls",
		Type:       plan.TierGraduated,
		UpTo:       upTo,
		UnitAmount: types.USD(unitCents),
		FlatAmount: types.USD(0),
		Priority:   priority,
	}
}

func TestSortTiers(t *testing.T) {
	t.Run("orders by UpTo ascending", func(t *testing.T) {
		in := []plan.PriceTier{tier(5000, 2, 0), tier(1000, 3, 0)}
		got := SortTiers(in)
		if got[0].UpTo != 1000 || got[1].UpTo != 5000 {
			t.Errorf("got order [%d %d], want [1000 5000]", got[0].UpTo, got[1].UpTo)
		}
	})

	t.Run("UpTo zero sorts last as unbounded", func(t *testing.T) {
		in := []plan.PriceTier{tier(0, 1, 0), tier(1000, 3, 0), tier(5000, 2, 0)}
		got := SortTiers(in)
		if got[2].UpTo != 0 {
			t.Errorf("unbounded tier at index 2: got UpTo %d, want 0", got[2].UpTo)
		}
	})

	t.Run("Priority breaks ties", func(t *testing.T) {
		in := []plan.PriceTier{tier(1000, 3, 5), tier(1000, 9, 1)}
		got := SortTiers(in)
		if got[0].Priority != 1 {
			t.Errorf("got first Priority %d, want 1", got[0].Priority)
		}
	})

	t.Run("does not mutate the input", func(t *testing.T) {
		in := []plan.PriceTier{tier(5000, 2, 0), tier(1000, 3, 0)}
		_ = SortTiers(in)
		if in[0].UpTo != 5000 {
			t.Errorf("input was mutated: got UpTo %d at index 0, want 5000", in[0].UpTo)
		}
	})
}

func TestComputeGraduated(t *testing.T) {
	// Restated from _project-files/ledger-design.md:1105 in whole minor
	// units, because types.Money cannot express the sub-cent rates the
	// original example used. See "Unresolved: sub-cent unit pricing".
	//
	//   first 1000 at 3c  = $30.00
	//   next  4000 at 2c  = $80.00
	//   above 5000 at 1c  = $10.00 for 1000 units
	//   6000 units        = $120.00
	ladder := []plan.PriceTier{tier(1000, 3, 0), tier(5000, 2, 0), tier(0, 1, 0)}

	tests := []struct {
		name string
		qty  int64
		want types.Money
	}{
		{"zero quantity", 0, types.USD(0)},
		{"inside the first tier", 500, types.USD(1500)},
		{"exactly the first tier boundary", 1000, types.USD(3000)},
		{"spanning two tiers", 3000, types.USD(3000 + 4000)},
		{"the worked example", 6000, types.USD(12000)},
		{"deep into the unbounded tier", 10000, types.USD(3000 + 8000 + 5000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeGraduated(SortTiers(ladder), tt.qty, "usd")
			if !got.Equal(tt.want) {
				t.Errorf("computeGraduated(%d): got %v, want %v", tt.qty, got, tt.want)
			}
		})
	}
}

// Review Focus 4: usage below the included limit must be free.
func TestComputeGraduatedZeroAndNegativeQuantity(t *testing.T) {
	ladder := []plan.PriceTier{tier(1000, 3, 0), tier(0, 1, 0)}

	for _, qty := range []int64{0, -1, -5000} {
		got := computeGraduated(SortTiers(ladder), qty, "usd")
		if !got.IsZero() {
			t.Errorf("computeGraduated(%d): got %v, want zero", qty, got)
		}
		if got.IsNegative() {
			t.Errorf("computeGraduated(%d): returned a negative charge %v", qty, got)
		}
	}
}
