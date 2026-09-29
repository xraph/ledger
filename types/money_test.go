package types

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"testing"
)

func TestMoneyConstructors(t *testing.T) {
	tests := []struct {
		name     string
		money    Money
		amount   int64
		currency string
		display  string
	}{
		{"USD", USD(4900), 4900, "usd", "$49.00"},
		{"EUR", EUR(19900), 19900, "eur", "€199.00"},
		{"GBP", GBP(9900), 9900, "gbp", "£99.00"},
		{"JPY", JPY(100), 100, "jpy", "¥100"},
		{"CAD", CAD(2500), 2500, "cad", "C$25.00"},
		{"AUD", AUD(7550), 7550, "aud", "A$75.50"},
		{"Zero USD", Zero("USD"), 0, "usd", "$0.00"},
		{"Zero EUR", Zero("EUR"), 0, "eur", "€0.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.money.Amount != tt.amount {
				t.Errorf("Amount: got %d, want %d", tt.money.Amount, tt.amount)
			}
			if tt.money.Currency != tt.currency {
				t.Errorf("Currency: got %s, want %s", tt.money.Currency, tt.currency)
			}
			if tt.money.String() != tt.display {
				t.Errorf("Display: got %s, want %s", tt.money.String(), tt.display)
			}
		})
	}
}

func TestMoneyArithmetic(t *testing.T) {
	tests := []struct {
		name     string
		op       func() Money
		expected Money
	}{
		{"Add", func() Money { return USD(100).Add(USD(200)) }, USD(300)},
		{"Subtract", func() Money { return USD(500).Subtract(USD(200)) }, USD(300)},
		{"Multiply", func() Money { return USD(100).Multiply(3) }, USD(300)},
		{"Divide", func() Money { return USD(900).Divide(3) }, USD(300)},
		{"Negate", func() Money { return USD(100).Negate() }, USD(-100)},
		{"Abs positive", func() Money { return USD(100).Abs() }, USD(100)},
		{"Abs negative", func() Money { return USD(-100).Abs() }, USD(100)},
		{"Complex", func() Money {
			return USD(1000).Add(USD(500)).Multiply(2).Subtract(USD(1000))
		}, USD(2000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.op()
			if !result.Equal(tt.expected) {
				t.Errorf("Got %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMoneyCurrencyMismatch(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for currency mismatch")
		}
	}()

	// This should panic
	_ = USD(100).Add(EUR(100))
}

func TestMoneyDivisionByZero(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for division by zero")
		}
	}()

	// This should panic
	_ = USD(100).Divide(0)
}

func TestMoneyComparison(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Money
		less    bool
		greater bool
		equal   bool
	}{
		{"Equal", USD(100), USD(100), false, false, true},
		{"Less", USD(50), USD(100), true, false, false},
		{"Greater", USD(200), USD(100), false, true, false},
		{"Zero equal", USD(0), Zero("usd"), false, false, true},
		{"Negative less", USD(-100), USD(100), true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.LessThan(tt.b); got != tt.less {
				t.Errorf("LessThan: got %v, want %v", got, tt.less)
			}
			if got := tt.a.GreaterThan(tt.b); got != tt.greater {
				t.Errorf("GreaterThan: got %v, want %v", got, tt.greater)
			}
			if got := tt.a.Equal(tt.b); got != tt.equal {
				t.Errorf("Equal: got %v, want %v", got, tt.equal)
			}
		})
	}
}

func TestMoneyMinMax(t *testing.T) {
	tests := []struct {
		name     string
		a, b     Money
		min, max Money
	}{
		{"First smaller", USD(50), USD(100), USD(50), USD(100)},
		{"Second smaller", USD(100), USD(50), USD(50), USD(100)},
		{"Equal", USD(100), USD(100), USD(100), USD(100)},
		{"Negative", USD(-50), USD(50), USD(-50), USD(50)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if minVal := tt.a.Min(tt.b); !minVal.Equal(tt.min) {
				t.Errorf("Min: got %v, want %v", minVal, tt.min)
			}
			if maxVal := tt.a.Max(tt.b); !maxVal.Equal(tt.max) {
				t.Errorf("Max: got %v, want %v", maxVal, tt.max)
			}
		})
	}
}

func TestMoneyPredicates(t *testing.T) {
	tests := []struct {
		name       string
		money      Money
		isZero     bool
		isPositive bool
		isNegative bool
	}{
		{"Zero", USD(0), true, false, false},
		{"Positive", USD(100), false, true, false},
		{"Negative", USD(-100), false, false, true},
		{"Large positive", USD(999999999), false, true, false},
		{"Large negative", USD(-999999999), false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.money.IsZero(); got != tt.isZero {
				t.Errorf("IsZero: got %v, want %v", got, tt.isZero)
			}
			if got := tt.money.IsPositive(); got != tt.isPositive {
				t.Errorf("IsPositive: got %v, want %v", got, tt.isPositive)
			}
			if got := tt.money.IsNegative(); got != tt.isNegative {
				t.Errorf("IsNegative: got %v, want %v", got, tt.isNegative)
			}
		})
	}
}

func TestMoneyFormatMajor(t *testing.T) {
	tests := []struct {
		money    Money
		expected string
	}{
		{USD(4900), "49.00"},
		{USD(100), "1.00"},
		{USD(1), "0.01"},
		{USD(0), "0.00"},
		{USD(-4900), "-49.00"},
		{USD(-1), "-0.01"},
		{EUR(9999), "99.99"},
		{JPY(100), "100"},     // No decimals
		{JPY(12345), "12345"}, // No decimals
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.money.FormatMajor(); got != tt.expected {
				t.Errorf("FormatMajor: got %s, want %s", got, tt.expected)
			}
		})
	}
}

func TestMoneyJSON(t *testing.T) {
	m := USD(4900)

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Check JSON structure
	expected := `{"amount":4900,"currency":"usd","display":"$49.00"}`
	if string(data) != expected {
		t.Errorf("JSON: got %s, want %s", string(data), expected)
	}

	// Unmarshal and verify
	var result struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Display  string `json:"display"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if result.Amount != 4900 || result.Currency != "usd" || result.Display != "$49.00" {
		t.Errorf("Unmarshaled data incorrect: %+v", result)
	}
}

func TestSum(t *testing.T) {
	tests := []struct {
		name     string
		values   []Money
		expected Money
	}{
		{"Empty", []Money{}, Zero("usd")},
		{"Single", []Money{USD(100)}, USD(100)},
		{"Multiple", []Money{USD(100), USD(200), USD(300)}, USD(600)},
		{"With negatives", []Money{USD(100), USD(-50), USD(200)}, USD(250)},
		{"All zero", []Money{USD(0), USD(0), USD(0)}, USD(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Sum(tt.values...)
			if !result.Equal(tt.expected) {
				t.Errorf("Sum: got %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCurrencySymbols(t *testing.T) {
	tests := []struct {
		currency string
		symbol   string
	}{
		{"usd", "$"},
		{"eur", "€"},
		{"gbp", "£"},
		{"jpy", "¥"},
		{"cad", "C$"},
		{"aud", "A$"},
		{"unknown", "UNKNOWN "},
	}

	for _, tt := range tests {
		t.Run(tt.currency, func(t *testing.T) {
			got := currencySymbol(tt.currency)
			if got != tt.symbol {
				t.Errorf("Symbol for %s: got %s, want %s", tt.currency, got, tt.symbol)
			}
		})
	}
}

func BenchmarkMoneyAdd(b *testing.B) {
	m1 := USD(100)
	m2 := USD(200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m1.Add(m2)
	}
}

func BenchmarkMoneyString(b *testing.B) {
	m := USD(4900)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.String()
	}
}

func BenchmarkMoneyJSON(b *testing.B) {
	m := USD(4900)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(m)
	}
}

func TestMoneyPercent(t *testing.T) {
	tests := []struct {
		name string
		base Money
		pct  int
		want Money
	}{
		{"ten percent of $49.00", USD(4900), 10, USD(490)},
		{"twenty-five percent of $49.00", USD(4900), 25, USD(1225)},
		{"zero percent", USD(4900), 0, USD(0)},
		{"one hundred percent", USD(4900), 100, USD(4900)},
		{"over one hundred percent", USD(4900), 150, USD(7350)},
		{"truncates rather than rounds", USD(101), 10, USD(10)},
		{"negative percent negates", USD(4900), -10, USD(-490)},
		{"negative percent truncates toward zero", USD(101), -10, USD(-10)},
		{"negative amount truncates toward zero", USD(-101), 10, USD(-10)},
		{"preserves currency", EUR(19900), 50, EUR(9950)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.base.Percent(tt.pct)
			if !got.Equal(tt.want) {
				t.Errorf("Percent(%d): got %v, want %v", tt.pct, got, tt.want)
			}
		})
	}
}

func TestCheckedAdd(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Money
		want    Money
		wantErr bool
	}{
		{"ordinary sum", USD(4900), USD(100), USD(5000), false},
		{"max plus zero is fine", USD(math.MaxInt64), USD(0), USD(math.MaxInt64), false},
		{"max minus one plus one is fine", USD(math.MaxInt64 - 1), USD(1), USD(math.MaxInt64), false},
		{"max plus one overflows", USD(math.MaxInt64), USD(1), Money{}, true},
		{"one plus max overflows", USD(1), USD(math.MaxInt64), Money{}, true},
		{"two large halves overflow", USD(math.MaxInt64/2 + 1), USD(math.MaxInt64/2 + 1), Money{}, true},
		{"min plus zero is fine", USD(math.MinInt64), USD(0), USD(math.MinInt64), false},
		{"min plus minus one overflows", USD(math.MinInt64), USD(-1), Money{}, true},
		{"min plus max is minus one", USD(math.MinInt64), USD(math.MaxInt64), USD(-1), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.CheckedAdd(tt.b)
			if tt.wantErr {
				if !errors.Is(err, ErrOverflow) {
					t.Fatalf("got err %v, want ErrOverflow", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckedAddCurrencyMismatchIsAnErrorNotAPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CheckedAdd panicked on a currency mismatch: %v", r)
		}
	}()

	_, err := USD(100).CheckedAdd(EUR(100))
	if err == nil {
		t.Fatal("got nil error for a currency mismatch")
	}
	if errors.Is(err, ErrOverflow) {
		t.Errorf("a currency mismatch must not be reported as an overflow: %v", err)
	}
}

func TestCheckedSubtract(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Money
		want    Money
		wantErr bool
	}{
		{"ordinary difference", USD(5000), USD(100), USD(4900), false},
		{"min minus zero is fine", USD(math.MinInt64), USD(0), USD(math.MinInt64), false},
		{"min minus one overflows", USD(math.MinInt64), USD(1), Money{}, true},
		{"max minus minus one overflows", USD(math.MaxInt64), USD(-1), Money{}, true},
		{"zero minus min overflows", USD(0), USD(math.MinInt64), Money{}, true},
		{"max minus max is zero", USD(math.MaxInt64), USD(math.MaxInt64), USD(0), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.CheckedSubtract(tt.b)
			if tt.wantErr {
				if !errors.Is(err, ErrOverflow) {
					t.Fatalf("got err %v, want ErrOverflow", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	if _, err := USD(1).CheckedSubtract(EUR(1)); err == nil || errors.Is(err, ErrOverflow) {
		t.Errorf("got %v, want a non-overflow error for a currency mismatch", err)
	}
}

func TestCheckedMultiply(t *testing.T) {
	tests := []struct {
		name    string
		m       Money
		qty     int64
		want    Money
		wantErr bool
	}{
		{"ordinary product", USD(3), 1500, USD(4500), false},
		{"zero amount", USD(0), math.MaxInt64, USD(0), false},
		{"zero quantity", USD(math.MaxInt64), 0, USD(0), false},
		{"times one", USD(math.MaxInt64), 1, USD(math.MaxInt64), false},
		{"max amount times two overflows", USD(math.MaxInt64), 2, Money{}, true},
		{"just inside the boundary", USD(math.MaxInt64 / 3), 3, USD(math.MaxInt64 / 3 * 3), false},
		{"just past the boundary", USD(math.MaxInt64/3 + 1), 3, Money{}, true},
		{"three cents by half of max overflows", USD(3), math.MaxInt64 / 2, Money{}, true},
		{"negative amount inside the boundary", USD(math.MinInt64 / 2), 2, USD(math.MinInt64), false},
		{"negative amount past the boundary", USD(math.MinInt64/2 - 1), 2, Money{}, true},
		{"negative quantity inside the boundary", USD(2), math.MinInt64 / 2, USD(math.MinInt64), false},
		{"negative quantity past the boundary", USD(3), math.MinInt64/2 - 1, Money{}, true},
		{"min times minus one overflows", USD(math.MinInt64), -1, Money{}, true},
		{"minus one times min overflows", USD(-1), math.MinInt64, Money{}, true},
		{"two negatives overflow", USD(math.MinInt64/2 - 1), -2, Money{}, true},
		{"two negatives inside the boundary", USD(-3), -1000, USD(3000), false},
		{"min times one", USD(math.MinInt64), 1, USD(math.MinInt64), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.m.CheckedMultiply(tt.qty)
			if tt.wantErr {
				if !errors.Is(err, ErrOverflow) {
					t.Fatalf("got %v, err %v, want ErrOverflow", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckedPercent(t *testing.T) {
	if _, err := USD(math.MaxInt64).CheckedPercent(2); !errors.Is(err, ErrOverflow) {
		t.Errorf("CheckedPercent(MaxInt64, 2): got err %v, want ErrOverflow", err)
	}
	if _, err := USD(math.MinInt64).CheckedPercent(-1); !errors.Is(err, ErrOverflow) {
		t.Errorf("CheckedPercent(MinInt64, -1): got err %v, want ErrOverflow", err)
	}
	// MaxInt64 * 1 fits, so one percent of it must succeed.
	got, err := USD(math.MaxInt64).CheckedPercent(1)
	if err != nil {
		t.Fatalf("CheckedPercent(MaxInt64, 1): unexpected error %v", err)
	}
	if want := int64(math.MaxInt64 / 100); got.Amount != want {
		t.Errorf("CheckedPercent(MaxInt64, 1): got %d, want %d", got.Amount, want)
	}
	if got, err := USD(math.MaxInt64).CheckedPercent(0); err != nil || !got.IsZero() {
		t.Errorf("CheckedPercent(MaxInt64, 0): got %v, %v, want zero and no error", got, err)
	}
}

// TestCheckedPercentAgreesWithPercent pins the checked form to the panicking
// one on every row TestMoneyPercent already covers.
func TestCheckedPercentAgreesWithPercent(t *testing.T) {
	tests := []struct {
		name string
		base Money
		pct  int
	}{
		{"ten percent of $49.00", USD(4900), 10},
		{"twenty-five percent of $49.00", USD(4900), 25},
		{"zero percent", USD(4900), 0},
		{"one hundred percent", USD(4900), 100},
		{"over one hundred percent", USD(4900), 150},
		{"truncates rather than rounds", USD(101), 10},
		{"negative percent negates", USD(4900), -10},
		{"negative percent truncates toward zero", USD(101), -10},
		{"negative amount truncates toward zero", USD(-101), 10},
		{"preserves currency", EUR(19900), 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.base.CheckedPercent(tt.pct)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := tt.base.Percent(tt.pct); !got.Equal(want) {
				t.Errorf("CheckedPercent(%d): got %v, want %v (what Percent returns)", tt.pct, got, want)
			}
		})
	}
}

// TestCheckedMultiplyMatchesBigInt cross-checks the overflow detection
// against arbitrary-precision arithmetic over every pair of boundary values.
func TestCheckedMultiplyMatchesBigInt(t *testing.T) {
	values := []int64{
		0, 1, -1, 2, -2, 3, -3, 10, -10, 100, -100,
		math.MaxInt64, math.MaxInt64 - 1, math.MaxInt64 / 2, math.MaxInt64/2 + 1, math.MaxInt64 / 3, math.MaxInt64/3 + 1,
		math.MinInt64, math.MinInt64 + 1, math.MinInt64 / 2, math.MinInt64/2 - 1, math.MinInt64 / 3, math.MinInt64/3 - 1,
		1 << 31, -(1 << 31), 1 << 32, -(1 << 32), 3_037_000_499, 3_037_000_500,
	}
	lo, hi := big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64)

	for _, a := range values {
		for _, b := range values {
			exact := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
			fits := exact.Cmp(lo) >= 0 && exact.Cmp(hi) <= 0

			got, err := USD(a).CheckedMultiply(b)
			switch {
			case fits && err != nil:
				t.Errorf("CheckedMultiply(%d, %d): unexpected error %v", a, b, err)
			case fits && got.Amount != exact.Int64():
				t.Errorf("CheckedMultiply(%d, %d): got %d, want %d", a, b, got.Amount, exact.Int64())
			case !fits && !errors.Is(err, ErrOverflow):
				t.Errorf("CheckedMultiply(%d, %d): got %v, %v, want ErrOverflow (exact product %s)", a, b, got, err, exact)
			}

			sum := new(big.Int).Add(big.NewInt(a), big.NewInt(b))
			sumFits := sum.Cmp(lo) >= 0 && sum.Cmp(hi) <= 0
			if _, err := USD(a).CheckedAdd(USD(b)); sumFits != (err == nil) {
				t.Errorf("CheckedAdd(%d, %d): err %v, but the exact sum %s fits=%v", a, b, err, sum, sumFits)
			}

			diff := new(big.Int).Sub(big.NewInt(a), big.NewInt(b))
			diffFits := diff.Cmp(lo) >= 0 && diff.Cmp(hi) <= 0
			if _, err := USD(a).CheckedSubtract(USD(b)); diffFits != (err == nil) {
				t.Errorf("CheckedSubtract(%d, %d): err %v, but the exact difference %s fits=%v", a, b, err, diff, diffFits)
			}
		}
	}
}
