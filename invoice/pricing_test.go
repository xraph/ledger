package invoice

import (
	"errors"
	"strings"
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
		want := []int64{1000, 5000, 0}
		for i, w := range want {
			if got[i].UpTo != w {
				t.Errorf("index %d: got UpTo %d, want %d", i, got[i].UpTo, w)
			}
		}
	})

	// R1: the repo documents -1 as unlimited (README.md:102, docs/API.md:195,
	// docs_test.go:74), not just 0. It must sort last exactly like 0 does.
	t.Run("negative UpTo sorts last alongside zero", func(t *testing.T) {
		in := []plan.PriceTier{tier(-1, 1, 0), tier(1000, 3, 0), tier(5000, 2, 0)}
		got := SortTiers(in)
		want := []int64{1000, 5000, -1}
		for i, w := range want {
			if got[i].UpTo != w {
				t.Errorf("index %d: got UpTo %d, want %d", i, got[i].UpTo, w)
			}
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
			// R11: computeVolume now takes usage and included separately so
			// that ComputeOverage can call this exact function instead of
			// repeating the same lookup-and-multiply inline. included 0
			// reproduces the old bare-quantity behaviour these rows pin.
			got := computeVolume(SortTiers(ladder), tt.qty, 0, "usd")
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
		// R6: UpTo counts TOTAL usage, so the 500 billable units (positions
		// 1001-1500) are priced where they actually sit in the ladder, in
		// the 1c tier, not re-priced from zero at the 3c tier.
		{"billable units are priced at the positions they occupy", ladder, 1500, 1000, types.USD(500)},
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

// Fix round 1, R1/R3/R6: boundaries on the same three ladders, verified
// through ComputeOverage (included 0) so sorting, unbounded-sentinel
// handling and last-tier extension all run together the way a caller
// actually hits them.
func TestComputeOverageBoundaries(t *testing.T) {
	graduated := []plan.PriceTier{tier(1000, 3, 0), tier(5000, 2, 0), tier(0, 1, 0)}
	volume := []plan.PriceTier{volumeTier(1000, 3), volumeTier(5000, 2), volumeTier(0, 1)}
	flat := []plan.PriceTier{flatTier(1000, 500), flatTier(5000, 2000), flatTier(0, 9000)}

	tests := []struct {
		name  string
		tiers []plan.PriceTier
		usage int64
		want  types.Money
	}{
		{"graduated at the second boundary", graduated, 5000, types.USD(11000)},
		{"graduated one past the second boundary", graduated, 5001, types.USD(11001)},
		{"volume at the second boundary", volume, 5000, types.USD(10000)},
		{"volume one past the second boundary", volume, 5001, types.USD(5001)},
		{"flat at the first boundary", flat, 1000, types.USD(500)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeOverage(tt.tiers, tt.usage, 0, "usd")
			if !got.Equal(tt.want) {
				t.Errorf("ComputeOverage(usage=%d): got %v, want %v", tt.usage, got, tt.want)
			}
		})
	}
}

// R1: -1 is documented as unlimited (README.md:102, docs/API.md:195,
// docs_test.go:74), not just 0. The README's own example plan bills its
// allowance at $0 up to 10000 calls, then 1c/unit with no upper bound.
func TestComputeOverageNegativeUpToIsUnbounded(t *testing.T) {
	readmePlan := []plan.PriceTier{tier(10000, 0, 0), tier(-1, 1, 0)}

	tests := []struct {
		name     string
		usage    int64
		included int64
		want     types.Money
	}{
		{"included matches the allowance tier", 50000, 10000, types.USD(40000)},
		{"no included allowance, the $0 tier still isn't billed twice", 50000, 0, types.USD(40000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeOverage(readmePlan, tt.usage, tt.included, "usd")
			if !got.Equal(tt.want) {
				t.Errorf("ComputeOverage(usage=%d, included=%d): got %v, want %v",
					tt.usage, tt.included, got, tt.want)
			}
		})
	}
}

// R2: ComputeOverage must normalise currency once at entry. Before this fix,
// types.Zero("USD") produced "usd" while the tier arithmetic kept "USD",
// and Money.Add panicked on the mismatch.
func TestComputeOverageNormalisesCurrency(t *testing.T) {
	graduated := []plan.PriceTier{tier(1000, 3, 0), tier(0, 1, 0)}
	volume := []plan.PriceTier{volumeTier(1000, 3), volumeTier(0, 1)}
	flat := []plan.PriceTier{flatTier(1000, 500), flatTier(0, 9000)}

	t.Run("graduated charge uses lowercase currency", func(t *testing.T) {
		got := ComputeOverage(graduated, 100, 0, "USD")
		want := types.Money{Amount: 300, Currency: "usd"}
		if !got.Equal(want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	})

	t.Run("volume charge uses lowercase currency", func(t *testing.T) {
		got := ComputeOverage(volume, 100, 0, "USD")
		want := types.Money{Amount: 300, Currency: "usd"}
		if !got.Equal(want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	})

	t.Run("flat charge uses lowercase currency", func(t *testing.T) {
		got := ComputeOverage(flat, 100, 0, "USD")
		want := types.Money{Amount: 500, Currency: "usd"}
		if !got.Equal(want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	})

	t.Run("zero charge still uses lowercase currency", func(t *testing.T) {
		got := ComputeOverage(graduated, 0, 0, "USD")
		want := types.Money{Amount: 0, Currency: "usd"}
		if !got.Equal(want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	})
}

// R3: a ladder with no unbounded tier must not price anything above its
// highest UpTo at zero. The last tier after sorting extends to infinity.
func TestComputeOverageLastTierExtends(t *testing.T) {
	graduated := []plan.PriceTier{tier(1000, 3, 0), tier(5000, 2, 0)}
	volume := []plan.PriceTier{volumeTier(1000, 3), volumeTier(5000, 2)}
	flat := []plan.PriceTier{flatTier(1000, 500), flatTier(5000, 2000)}

	tests := []struct {
		name  string
		tiers []plan.PriceTier
		usage int64
		want  types.Money
	}{
		{"graduated one past the top", graduated, 5001, types.USD(11002)},
		{"graduated deep past the top", graduated, 100000, types.USD(201000)},
		{"volume one past the top", volume, 5001, types.USD(10002)},
		{"flat one past the top", flat, 5001, types.USD(2000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeOverage(tt.tiers, tt.usage, 0, "usd")
			if !got.Equal(tt.want) {
				t.Errorf("ComputeOverage(usage=%d): got %v, want %v", tt.usage, got, tt.want)
			}
		})
	}
}

// R6: UpTo counts TOTAL usage, so volume and flat must look at usage, not
// the billable remainder, to find the applicable tier.
func TestComputeOverageAllowanceNotGivenTwice(t *testing.T) {
	volume := []plan.PriceTier{volumeTier(1000, 3), volumeTier(0, 2)}
	flat := []plan.PriceTier{flatTier(1000, 500), flatTier(0, 2000)}

	t.Run("volume charges the total-usage tier's rate on only the excess", func(t *testing.T) {
		got := ComputeOverage(volume, 1500, 1000, "usd")
		want := types.USD(1000)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	// R9: flat is a differential like graduated, fee(usage) - fee(included).
	// fee(1500) = 2000 (unbounded $20 band), fee(1000) = 500 (the $5 band
	// the 1000th unit still belongs to) -> 2000-500 = 1500, not the full
	// $20 band fee. Changed from 1500 (was 2000 pre-round-2).
	t.Run("flat charges only the fee difference between usage and included", func(t *testing.T) {
		got := ComputeOverage(flat, 1500, 1000, "usd")
		want := types.USD(1500)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("flat charges nothing until usage exceeds included", func(t *testing.T) {
		got := ComputeOverage(flat, 800, 1000, "usd")
		want := types.USD(0)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// R7: a negative included allowance means unlimited, matching
// plan.Feature.Limit == -1.
func TestComputeOverageNegativeIncludedIsUnlimited(t *testing.T) {
	ladder := []plan.PriceTier{tier(1000, 3, 0), tier(0, 1, 0)}
	got := ComputeOverage(ladder, 100, -1, "usd")
	want := types.USD(0)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Every other ComputeOverage test above passes pre-sorted tiers, so a
// missing internal SortTiers call would still pass all of them. This one
// passes the ladder unbounded-first: without sorting, the unbounded tier
// would absorb all 1500 units at 1c (1500) instead of 1000 at 3c plus 500 at
// 1c (3500), and this test would catch that a different, wrong way than a
// panic or compile error would.
func TestComputeOverageSortsUnsortedInput(t *testing.T) {
	unsorted := []plan.PriceTier{tier(0, 1, 0), tier(1000, 3, 0)}
	got := ComputeOverage(unsorted, 1500, 0, "usd")
	want := types.USD(3500)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Fix round 2, R9: flat overage is max(0, fee(usage) - fee(included)),
// exactly like graduated, not the full band fee whenever usage exceeds the
// allowance. Ladder: 1000:$5.00 / 5000:$20.00 / ∞:$90.00.
func TestComputeOverageFlatIsADifferential(t *testing.T) {
	flat := []plan.PriceTier{flatTier(1000, 500), flatTier(5000, 2000), flatTier(0, 9000)}

	tests := []struct {
		name     string
		usage    int64
		included int64
		want     types.Money
	}{
		// fee(3001) = 2000 (the $20 band), fee(3000) = 2000 (same band):
		// both included and usage already sit inside it, so nothing new is
		// owed for crossing from 3000 to 3001.
		{"both usage and included are already in the same band", 3001, 3000, types.USD(0)},
		// fee(1001) = 2000 (the $20 band), fee(900) = 500 (the $5 band):
		// the allowance sat in the cheaper band, so the difference is the
		// $20 band's fee minus the $5 band's, not the full $20.
		{"included sits in a lower band than usage", 1001, 900, types.USD(1500)},
		// fee(1001) = 2000, fee(0) = 0: no allowance at all, so this
		// matches the plain no-allowance behaviour from before R9.
		{"no allowance behaves as before", 1001, 0, types.USD(2000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeOverage(flat, tt.usage, tt.included, "usd")
			if !got.Equal(tt.want) {
				t.Errorf("ComputeOverage(usage=%d, included=%d): got %v, want %v",
					tt.usage, tt.included, got, tt.want)
			}
		})
	}
}

func TestValidateTiers(t *testing.T) {
	t.Run("valid graduated ladder", func(t *testing.T) {
		valid := []plan.PriceTier{tier(1000, 3, 0), tier(0, 1, 0)}
		if err := ValidateTiers(valid, "usd"); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		if err := ValidateTiers(nil, "usd"); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("mixed types", func(t *testing.T) {
		mixed := []plan.PriceTier{tier(1000, 3, 0), flatTier(0, 100)}
		if err := ValidateTiers(mixed, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("empty type", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.Type = ""
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("unrecognised type", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.Type = "tiered"
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("unit amount currency mismatch", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.UnitAmount = types.EUR(3)
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("unit amount currency matches case-insensitively", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.UnitAmount = types.Money{Amount: 3, Currency: "USD"}
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("zero-value flat amount with empty currency is exempt", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.FlatAmount = types.Money{}
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("mismatched feature keys", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t1 := tier(0, 1, 0)
		t1.FeatureKey = "seats"
		if err := ValidateTiers([]plan.PriceTier{t0, t1}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("duplicate UpTo", func(t *testing.T) {
		dup := []plan.PriceTier{tier(1000, 3, 0), tier(1000, 2, 1)}
		if err := ValidateTiers(dup, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("zero and negative UpTo are the same unbounded slot", func(t *testing.T) {
		dup := []plan.PriceTier{tier(0, 1, 0), tier(-1, 2, 1)}
		if err := ValidateTiers(dup, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("graduated tier with a flat amount", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.FlatAmount = types.USD(100)
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("flat tier with a unit amount", func(t *testing.T) {
		t0 := flatTier(1000, 500)
		t0.UnitAmount = types.USD(3)
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	// R10: a negative rate quietly reduces an invoice instead of erroring.
	t.Run("negative unit amount", func(t *testing.T) {
		t0 := tier(1000, -3, 0)
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("negative flat amount", func(t *testing.T) {
		t0 := flatTier(1000, -500)
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	// R12: rule 3's FlatAmount half had no discriminating test — every
	// existing currency-mismatch row exercised UnitAmount only, so a bug
	// specific to the FlatAmount check could have shipped unnoticed.
	t.Run("flat amount currency mismatch", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.FlatAmount = types.Money{Amount: 0, Currency: "eur"}
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	t.Run("flat amount currency matches case-insensitively", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.FlatAmount = types.Money{Amount: 0, Currency: "USD"}
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("zero-value unit amount with empty currency is exempt", func(t *testing.T) {
		t0 := tier(1000, 3, 0)
		t0.UnitAmount = types.Money{}
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	// R12: rule 6 was only tested for graduated tiers; volume shares the
	// same branch but had no row of its own.
	t.Run("volume tier with a flat amount", func(t *testing.T) {
		t0 := volumeTier(1000, 3)
		t0.FlatAmount = types.USD(100)
		if err := ValidateTiers([]plan.PriceTier{t0}, "usd"); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("got %v, want ErrInvalidTiers", err)
		}
	})

	// R12: every error must name the offending tier by index and UpTo, not
	// only FeatureKey, so a plan with several tiers doesn't leave the
	// caller guessing which one is bad.
	t.Run("error message names the offending tier by index", func(t *testing.T) {
		good := tier(1000, 3, 0)
		bad := tier(0, 1, 0)
		bad.Type = "tiered"
		err := ValidateTiers([]plan.PriceTier{good, bad}, "usd")
		if !errors.Is(err, ErrInvalidTiers) {
			t.Fatalf("got %v, want ErrInvalidTiers", err)
		}
		if !strings.Contains(err.Error(), "tier 1") {
			t.Errorf("error %q does not name the offending tier by index (want to contain %q)", err.Error(), "tier 1")
		}
	})
}
