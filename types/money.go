// Package types provides common types used across Ledger.
package types

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Money represents a monetary value in the smallest currency unit.
// All arithmetic is integer-only — no floating point.
//
// Examples:
//   - USD(4900) = $49.00 (4900 cents)
//   - EUR(19900) = €199.00 (19900 cents)
//   - GBP(9900) = £99.00 (9900 pence)
type Money struct {
	Amount   int64  `json:"amount"`   // Smallest unit (cents, pence, etc)
	Currency string `json:"currency"` // ISO 4217 lowercase: "usd", "eur", "gbp"
}

// Common currency constructors

// USD creates a Money value in US Dollars (cents).
func USD(cents int64) Money { return Money{Amount: cents, Currency: "usd"} }

// EUR creates a Money value in Euros (cents).
func EUR(cents int64) Money { return Money{Amount: cents, Currency: "eur"} }

// GBP creates a Money value in British Pounds (pence).
func GBP(pence int64) Money { return Money{Amount: pence, Currency: "gbp"} }

// JPY creates a Money value in Japanese Yen (no decimal).
func JPY(yen int64) Money { return Money{Amount: yen, Currency: "jpy"} }

// CAD creates a Money value in Canadian Dollars (cents).
func CAD(cents int64) Money { return Money{Amount: cents, Currency: "cad"} }

// AUD creates a Money value in Australian Dollars (cents).
func AUD(cents int64) Money { return Money{Amount: cents, Currency: "aud"} }

// Zero returns a zero Money value in the specified currency.
func Zero(currency string) Money { return Money{Amount: 0, Currency: strings.ToLower(currency)} }

// Arithmetic operations

// Add adds two Money values. Panics if currencies don't match.
func (m Money) Add(other Money) Money {
	m.assertSameCurrency(other)
	return Money{Amount: m.Amount + other.Amount, Currency: m.Currency}
}

// Subtract subtracts another Money value. Panics if currencies don't match.
func (m Money) Subtract(other Money) Money {
	m.assertSameCurrency(other)
	return Money{Amount: m.Amount - other.Amount, Currency: m.Currency}
}

// Multiply multiplies the Money by a quantity.
func (m Money) Multiply(qty int64) Money {
	return Money{Amount: m.Amount * qty, Currency: m.Currency}
}

// Divide divides the Money by a divisor. Uses integer division.
func (m Money) Divide(divisor int64) Money {
	if divisor == 0 {
		panic("money: division by zero")
	}
	return Money{Amount: m.Amount / divisor, Currency: m.Currency}
}

// Negate returns the negative of the Money value.
func (m Money) Negate() Money {
	return Money{Amount: -m.Amount, Currency: m.Currency}
}

// Abs returns the absolute value.
func (m Money) Abs() Money {
	if m.Amount < 0 {
		return Money{Amount: -m.Amount, Currency: m.Currency}
	}
	return m
}

// Comparison methods

// IsZero returns true if the amount is zero.
func (m Money) IsZero() bool { return m.Amount == 0 }

// IsPositive returns true if the amount is greater than zero.
func (m Money) IsPositive() bool { return m.Amount > 0 }

// IsNegative returns true if the amount is less than zero.
func (m Money) IsNegative() bool { return m.Amount < 0 }

// Equal returns true if both Money values are equal (same amount and currency).
func (m Money) Equal(other Money) bool {
	return m.Amount == other.Amount && m.Currency == other.Currency
}

// LessThan returns true if this Money is less than other. Panics if currencies don't match.
func (m Money) LessThan(other Money) bool {
	m.assertSameCurrency(other)
	return m.Amount < other.Amount
}

// GreaterThan returns true if this Money is greater than other. Panics if currencies don't match.
func (m Money) GreaterThan(other Money) bool {
	m.assertSameCurrency(other)
	return m.Amount > other.Amount
}

// Min returns the smaller of two Money values. Panics if currencies don't match.
func (m Money) Min(other Money) Money {
	m.assertSameCurrency(other)
	if m.Amount < other.Amount {
		return m
	}
	return other
}

// Max returns the larger of two Money values. Panics if currencies don't match.
func (m Money) Max(other Money) Money {
	m.assertSameCurrency(other)
	if m.Amount > other.Amount {
		return m
	}
	return other
}

// Formatting methods

// FormatMajor returns the major unit string without currency symbol.
// For currencies with 2 decimal places: "49.00" for USD(4900).
// For currencies with 0 decimal places (JPY): "100" for JPY(100).
func (m Money) FormatMajor() string {
	decimals := currencyDecimals(m.Currency)
	if decimals == 0 {
		return fmt.Sprintf("%d", m.Amount)
	}

	divisor := int64(1)
	for i := 0; i < decimals; i++ {
		divisor *= 10
	}

	// Handle sign separately
	isNegative := m.Amount < 0
	absAmount := m.Amount
	if isNegative {
		absAmount = -absAmount
	}

	major := absAmount / divisor
	minor := absAmount % divisor

	format := fmt.Sprintf("%%d.%%0%dd", decimals)
	result := fmt.Sprintf(format, major, minor)

	if isNegative {
		return "-" + result
	}
	return result
}

// String returns a human-readable string with currency symbol.
// Examples: "$49.00", "€199.00", "£99.00", "¥100"
func (m Money) String() string {
	symbol := currencySymbol(m.Currency)
	return symbol + m.FormatMajor()
}

// MarshalJSON implements json.Marshaler.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Display  string `json:"display"`
	}{
		Amount:   m.Amount,
		Currency: m.Currency,
		Display:  m.String(),
	})
}

// Helper functions

// assertSameCurrency panics if currencies don't match.
func (m Money) assertSameCurrency(other Money) {
	if m.Currency != other.Currency {
		panic(fmt.Sprintf("money: currency mismatch: %s != %s", m.Currency, other.Currency))
	}
}

// currencySymbol returns the symbol for a currency code.
func currencySymbol(currency string) string {
	symbols := map[string]string{
		"usd": "$",
		"eur": "€",
		"gbp": "£",
		"jpy": "¥",
		"cad": "C$",
		"aud": "A$",
		"chf": "CHF ",
		"cny": "¥",
		"sek": "kr ",
		"nzd": "NZ$",
	}
	if sym, ok := symbols[strings.ToLower(currency)]; ok {
		return sym
	}
	return strings.ToUpper(currency) + " "
}

// currencyDecimals returns the number of decimal places for a currency.
func currencyDecimals(currency string) int {
	// Currencies with 0 decimal places
	zeroDecimal := map[string]bool{
		"jpy": true, // Japanese Yen
		"krw": true, // Korean Won
		"vnd": true, // Vietnamese Dong
		"clp": true, // Chilean Peso
		"pyg": true, // Paraguayan Guarani
		"idr": true, // Indonesian Rupiah
	}
	if zeroDecimal[strings.ToLower(currency)] {
		return 0
	}
	// Most currencies have 2 decimal places
	return 2
}

// Sum calculates the sum of multiple Money values. All must have the same currency.
func Sum(values ...Money) Money {
	if len(values) == 0 {
		return Zero("usd")
	}

	result := values[0]
	for i := 1; i < len(values); i++ {
		result = result.Add(values[i])
	}
	return result
}
