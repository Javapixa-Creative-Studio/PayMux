package money

import (
	"errors"
	"testing"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		minor    int64
		currency string
		want     string
	}{
		{150000, "IDR", "150000"},
		{0, "IDR", "0"},
		{1, "IDR", "1"},
		{1050, "USD", "10.50"},
		{5, "USD", "0.05"},
		{0, "USD", "0.00"},
		{100000, "USD", "1000.00"},
		{-2550, "USD", "-25.50"},
		{999, "JPY", "999"},
	}
	for _, tc := range cases {
		got, err := Format(tc.minor, tc.currency)
		if err != nil {
			t.Errorf("Format(%d, %s) error: %v", tc.minor, tc.currency, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Format(%d, %s) = %q, want %q", tc.minor, tc.currency, got, tc.want)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		value    string
		currency string
		want     int64
	}{
		// Midtrans sends zero-decimal currencies with trailing ".00".
		{"150000.00", "IDR", 150000},
		{"150000", "IDR", 150000},
		{"0", "IDR", 0},
		{"10.50", "USD", 1050},
		{"10.5", "USD", 1050},
		{"10", "USD", 1000},
		{".50", "USD", 50},
		{"-25.50", "USD", -2550},
		{"+25.50", "USD", 2550},
		{" 150000.00 ", "IDR", 150000},
	}
	for _, tc := range cases {
		got, err := Parse(tc.value, tc.currency)
		if err != nil {
			t.Errorf("Parse(%q, %s) error: %v", tc.value, tc.currency, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Parse(%q, %s) = %d, want %d", tc.value, tc.currency, got, tc.want)
		}
	}
}

func TestParseRejectsExcessPrecision(t *testing.T) {
	// A rupiah has no subdivision: a fractional amount means PayMux and the
	// gateway disagree, which must not be silently rounded away.
	if _, err := Parse("150000.50", "IDR"); err == nil {
		t.Error("Parse accepted a fractional IDR amount")
	}
	if _, err := Parse("10.505", "USD"); err == nil {
		t.Error("Parse accepted sub-cent precision for USD")
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, value := range []string{"", "abc", "1,000", "1.2.3", "1e5", "--5", "0x10", "  "} {
		if _, err := Parse(value, "IDR"); err == nil {
			t.Errorf("Parse(%q) = nil error, want failure", value)
		}
	}
}

func TestUnknownCurrency(t *testing.T) {
	if _, err := Parse("100", "XYZ"); !errors.Is(err, ErrUnknownCurrency) {
		t.Errorf("Parse with unknown currency = %v, want ErrUnknownCurrency", err)
	}
	if _, err := Format(100, "XYZ"); !errors.Is(err, ErrUnknownCurrency) {
		t.Errorf("Format with unknown currency = %v, want ErrUnknownCurrency", err)
	}
	if Supported("XYZ") {
		t.Error("Supported reported an unknown currency as supported")
	}
	if !Supported("idr") {
		t.Error("Supported did not normalise the currency code")
	}
}

func TestRoundTrip(t *testing.T) {
	for _, currency := range []string{"IDR", "USD", "JPY", "SGD"} {
		for _, minor := range []int64{0, 1, 99, 100, 12345, 999999999} {
			formatted, err := Format(minor, currency)
			if err != nil {
				t.Fatalf("Format(%d, %s): %v", minor, currency, err)
			}
			got, err := Parse(formatted, currency)
			if err != nil {
				t.Fatalf("Parse(%q, %s): %v", formatted, currency, err)
			}
			if got != minor {
				t.Errorf("round trip %d %s -> %q -> %d", minor, currency, formatted, got)
			}
		}
	}
}
