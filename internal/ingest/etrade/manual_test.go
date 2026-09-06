package etrade

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func TestNewRSUVest_BuildsValidTransaction(t *testing.T) {
	tx, err := NewRSUVest("ACME", "2024-03-15", "42", "187.50", "usd")
	if err != nil {
		t.Fatalf("NewRSUVest: %v", err)
	}

	if tx.Platform != ledger.PlatformETRADE {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformETRADE)
	}
	if tx.Type != ledger.TypeRSUVest {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeRSUVest)
	}
	if tx.Instrument != "ACME" {
		t.Errorf("Instrument = %q, want ACME", tx.Instrument)
	}
	wantDate := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	if !tx.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", tx.Date, wantDate)
	}
	if !tx.Quantity.Equal(decimal.RequireFromString("42")) {
		t.Errorf("Quantity = %s, want 42", tx.Quantity)
	}
	// Price carries the vest-date fair market value per share — the
	// engine reads it as the lot's cost basis.
	if !tx.Price.Equal(decimal.RequireFromString("187.50")) {
		t.Errorf("Price (FMV/share) = %s, want 187.50", tx.Price)
	}
	if tx.Currency != "USD" {
		t.Errorf("Currency = %q, want USD (upper-cased)", tx.Currency)
	}
}

func TestNewRSUVest_RejectsBadFields(t *testing.T) {
	cases := []struct {
		name                                   string
		symbol, date, quantity, fmv, currency string
	}{
		{"empty symbol", "", "2024-03-15", "42", "187.50", "USD"},
		{"malformed date", "ACME", "15-03-2024", "42", "187.50", "USD"},
		{"non-numeric quantity", "ACME", "2024-03-15", "lots", "187.50", "USD"},
		{"zero quantity", "ACME", "2024-03-15", "0", "187.50", "USD"},
		{"negative quantity", "ACME", "2024-03-15", "-1", "187.50", "USD"},
		{"non-numeric fmv", "ACME", "2024-03-15", "42", "market", "USD"},
		{"zero fmv", "ACME", "2024-03-15", "42", "0", "USD"},
		{"negative fmv", "ACME", "2024-03-15", "42", "-1.00", "USD"},
		{"empty currency", "ACME", "2024-03-15", "42", "187.50", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRSUVest(tc.symbol, tc.date, tc.quantity, tc.fmv, tc.currency); err == nil {
				t.Errorf("expected an error for %s, got nil", tc.name)
			}
		})
	}
}

func TestNewRSUVest_SameInputsTwice_SameFingerprint(t *testing.T) {
	a, err := NewRSUVest("ACME", "2024-03-15", "42", "187.50", "USD")
	if err != nil {
		t.Fatalf("NewRSUVest: %v", err)
	}
	b, err := NewRSUVest("ACME", "2024-03-15", "42", "187.50", "USD")
	if err != nil {
		t.Fatalf("NewRSUVest: %v", err)
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("expected identical manual vests to fingerprint the same, got %q and %q", a.Fingerprint(), b.Fingerprint())
	}
}
