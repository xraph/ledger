package types

import (
	"encoding/json"
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
