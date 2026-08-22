// Package money converts between PayMux's internal amounts and the decimal
// strings payment gateways exchange.
//
// PayMux stores every amount as an integer in the currency's minor unit —
// rupiah for IDR, cents for USD — so arithmetic on money is exact. Gateways
// speak decimal strings, and this package is the only place that translates
// between the two. Nothing here uses floating point: a float cannot represent
// most decimal fractions exactly, and a payment system must never round money
// by accident.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// exponents maps a currency to the number of decimal places it uses.
//
// The list covers the currencies Midtrans transacts in. An unknown currency
// is rejected rather than assumed, because guessing the exponent would be a
// hundredfold error in either direction.
var exponents = map[string]int{
	"IDR": 0,
	"JPY": 0,
	"KRW": 0,
	"VND": 0,
	"USD": 2,
	"SGD": 2,
	"MYR": 2,
	"PHP": 2,
	"THB": 2,
	"AUD": 2,
	"EUR": 2,
	"GBP": 2,
	"CNY": 2,
	"HKD": 2,
	"NZD": 2,
	"TWD": 2,
	"INR": 2,
	"CHF": 2,
	"CAD": 2,
	"AED": 2,
	"SAR": 2,
}

// ErrUnknownCurrency reports a currency PayMux has no exponent for.
var ErrUnknownCurrency = errors.New("money: unknown currency")

// Exponent returns the number of decimal places for a currency code.
func Exponent(currency string) (int, error) {
	exp, ok := exponents[NormalizeCurrency(currency)]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownCurrency, currency)
	}
	return exp, nil
}

// Supported reports whether a currency is known.
func Supported(currency string) bool {
	_, ok := exponents[NormalizeCurrency(currency)]
	return ok
}

// NormalizeCurrency upper-cases and trims a currency code.
func NormalizeCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}

// Format renders an amount in minor units as the decimal string a gateway
// expects: "150000" for 150000 IDR, "10.50" for 1050 USD cents.
func Format(minor int64, currency string) (string, error) {
	exp, err := Exponent(currency)
	if err != nil {
		return "", err
	}
	if exp == 0 {
		return strconv.FormatInt(minor, 10), nil
	}

	negative := minor < 0
	if negative {
		minor = -minor
	}
	divisor := pow10(exp)
	whole := minor / divisor
	frac := minor % divisor

	out := fmt.Sprintf("%d.%0*d", whole, exp, frac)
	if negative {
		out = "-" + out
	}
	return out, nil
}

// Parse converts a gateway's decimal string into minor units.
//
// It rejects any value carrying more precision than the currency has, rather
// than rounding it away: an unexpected fraction means PayMux and the gateway
// disagree about the amount, which is worth failing over.
func Parse(value, currency string) (int64, error) {
	exp, err := Exponent(currency)
	if err != nil {
		return 0, err
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("money: amount is empty")
	}
	negative := false
	switch value[0] {
	case '-':
		negative, value = true, value[1:]
	case '+':
		value = value[1:]
	}

	whole, frac, hasFrac := strings.Cut(value, ".")
	if whole == "" {
		whole = "0"
	}
	if !isDigits(whole) || (hasFrac && !isDigits(frac)) {
		return 0, fmt.Errorf("money: %q is not a valid decimal amount", value)
	}

	// A trailing run of zeros beyond the currency's precision is harmless:
	// gateways commonly send "150000.00" for a zero-decimal currency.
	if len(frac) > exp {
		if strings.Trim(frac[exp:], "0") != "" {
			return 0, fmt.Errorf("money: amount %q has more precision than %s supports", value, NormalizeCurrency(currency))
		}
		frac = frac[:exp]
	}
	for len(frac) < exp {
		frac += "0"
	}

	minorStr := whole + frac
	minor, err := strconv.ParseInt(minorStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money: amount %q is out of range", value)
	}
	if negative {
		minor = -minor
	}
	return minor, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func pow10(n int) int64 {
	out := int64(1)
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}
