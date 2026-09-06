package traderepublic

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// Trade Republic has no confirmed machine-readable interest export
// (see doc.go), so this package builds ledger.Transaction values from
// caller-supplied fields, mirroring internal/ingest/n26.

func TestNewInterestCredit_BuildsValidTransaction(t *testing.T) {
	tx, err := NewInterestCredit("2024-06-01", "37.50", "EUR")
	if err != nil {
		t.Fatalf("NewInterestCredit: %v", err)
	}

	if tx.Platform != ledger.PlatformTradeRepublic {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformTradeRepublic)
	}
	if tx.Type != ledger.TypeInterest {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeInterest)
	}
	wantDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	if !tx.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", tx.Date, wantDate)
	}
	if !tx.Quantity.Equal(decimal.NewFromInt(1)) {
		t.Errorf("Quantity = %s, want 1", tx.Quantity)
	}
	if !tx.Price.Equal(decimal.RequireFromString("37.50")) {
		t.Errorf("Price (interest amount) = %s, want 37.50", tx.Price)
	}
	if tx.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", tx.Currency)
	}
	if tx.Instrument != savingsInstrument {
		t.Errorf("Instrument = %q, want %q", tx.Instrument, savingsInstrument)
	}
}

func TestNewInterestCredit_InstrumentIsDistinctFromN26(t *testing.T) {
	// The two manual-interest sources must not collapse into one
	// holding on the dashboard.
	if savingsInstrument == "N26_SAVINGS" {
		t.Fatalf("Trade Republic instrument must differ from N26_SAVINGS, got %q", savingsInstrument)
	}
}

func TestNewInterestCredit_MalformedDate_ReturnsExplicitError(t *testing.T) {
	if _, err := NewInterestCredit("not-a-date", "10.00", "EUR"); err == nil {
		t.Fatal("expected an error for a malformed date, got nil")
	}
}

func TestNewInterestCredit_MalformedAmount_ReturnsExplicitError(t *testing.T) {
	if _, err := NewInterestCredit("2024-06-01", "not-a-number", "EUR"); err == nil {
		t.Fatal("expected an error for a malformed amount, got nil")
	}
}

func TestNewInterestCredit_NegativeAmount_ReturnsExplicitError(t *testing.T) {
	if _, err := NewInterestCredit("2024-06-01", "-1.00", "EUR"); err == nil {
		t.Fatal("expected an error for a negative amount, got nil")
	}
}

func TestNewInterestCredit_EmptyCurrency_ReturnsExplicitError(t *testing.T) {
	if _, err := NewInterestCredit("2024-06-01", "10.00", ""); err == nil {
		t.Fatal("expected an error for an empty currency, got nil")
	}
}

func TestNewInterestCredit_SameInputsTwice_SameFingerprint(t *testing.T) {
	a, err := NewInterestCredit("2024-06-01", "37.50", "EUR")
	if err != nil {
		t.Fatalf("NewInterestCredit: %v", err)
	}
	b, err := NewInterestCredit("2024-06-01", "37.50", "EUR")
	if err != nil {
		t.Fatalf("NewInterestCredit: %v", err)
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("expected identical manual entries to fingerprint the same, got %q and %q", a.Fingerprint(), b.Fingerprint())
	}
}
