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

func volumeTier(upTo, unitCents int64) plan.PriceTier {
	t := tier(upTo, unitCents, 0)
	t.Type = plan.TierVolume
	return t
}

func flatTier(upTo, flatCents int64) plan.PriceTier {
	t := tier(upTo, 0, 0)
	t.Type = plan.TierFlat
	t.FlatAmount = types.USD(flatCents)
	return t
}

func TestComputeVolume(t *testing.T) {
	// The tier covering the total quantity applies to ALL units.
	ladder := []plan.PriceTier{volumeTier(1000, 3), volumeTier(5000, 2), volumeTier(0, 1)}

	tests := []struct {
		name string
		qty  int64
		want types.Money
	}{
		{"zero quantity", 0, types.USD(0)},
		{"lands in the first tier", 500, types.USD(1500)},
		{"lands in the second tier, priced whole", 3000, types.USD(6000)},
		{"boundary belongs to the tier it names", 1000, types.USD(3000)},
		{"lands in the unbounded tier", 10000, types.USD(10000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeVolume(SortTiers(ladder), tt.qty, "usd")
			if !got.Equal(tt.want) {
				t.Errorf("computeVolume(%d): got %v, want %v", tt.qty, got, tt.want)
			}
		})
	}
}

func TestComputeFlat(t *testing.T) {
	// A fee for reaching a tier, not a per-unit rate.
	ladder := []plan.PriceTier{flatTier(1000, 500), flatTier(5000, 2000), flatTier(0, 9000)}

	tests := []struct {
		name string
		qty  int64
		want types.Money
	}{
		{"zero quantity pays nothing", 0, types.USD(0)},
		{"first tier", 500, types.USD(500)},
		{"second tier", 3000, types.USD(2000)},
		{"unbounded tier", 99999, types.USD(9000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeFlat(SortTiers(ladder), tt.qty, "usd")
			if !got.Equal(tt.want) {
				t.Errorf("computeFlat(%d): got %v, want %v", tt.qty, got, tt.want)
			}
		})
	}
}

func TestComputeOverage(t *testing.T) {
	ladder := []plan.PriceTier{tier(1000, 3, 0), tier(0, 1, 0)}

	tests := []struct {
		name     string
		tiers    []plan.PriceTier
		usage    int64
		included int64
		want     types.Money
	}{
		{"usage under the included limit is free", ladder, 400, 1000, types.USD(0)},
		{"usage exactly at the limit is free", ladder, 1000, 1000, types.USD(0)},
		{"only the excess is billed", ladder, 1500, 1000, types.USD(1500)},
		{"no included allowance bills everything", ladder, 500, 0, types.USD(1500)},
		// Review Focus 5: a metered feature with no tier priced is a
		// catalogue gap, not a billing failure.
		{"no tiers bills nothing", nil, 9999, 0, types.USD(0)},
		{"empty tier slice bills nothing", []plan.PriceTier{}, 9999, 0, types.USD(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeOverage(tt.tiers, tt.usage, tt.included, "usd")
			if !got.Equal(tt.want) {
				t.Errorf("ComputeOverage(usage=%d, included=%d): got %v, want %v",
					tt.usage, tt.included, got, tt.want)
			}
		})
	}
}

func TestComputeOverageDispatchesOnTierType(t *testing.T) {
	// Same ladder shape, three types, three answers. 3000 billable units.
	graduated := []plan.PriceTier{tier(1000, 3, 0), tier(0, 2, 0)}
	volume := []plan.PriceTier{volumeTier(1000, 3), volumeTier(0, 2)}
	flat := []plan.PriceTier{flatTier(1000, 300), flatTier(0, 700)}

	cases := []struct {
		name  string
		tiers []plan.PriceTier
		want  types.Money
	}{
		{"graduated", graduated, types.USD(3000 + 4000)},
		{"volume", volume, types.USD(6000)},
		{"flat", flat, types.USD(700)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ComputeOverage(c.tiers, 3000, 0, "usd")
			if !got.Equal(c.want) {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			}
		})
	}
}
