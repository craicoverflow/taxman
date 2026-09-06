package n26

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// N26 does not currently have a confirmed CSV export path (see
// doc.go and tasks/plan.md task 6.3 — resolved with the user as
// "not sure / build manual entry as the safe default"). This package
// therefore builds ledger.Transaction values directly from
// caller-supplied fields rather than parsing a file format, and can
// gain a CSV Parse() alongside this manual path later if a clean
// export is confirmed.

func TestNewInterestCredit_BuildsValidTransaction(t *testing.T) {
	tx, err := NewInterestCredit("2024-06-01", "200.00", "EUR")
	if err != nil {
		t.Fatalf("NewInterestCredit: %v", err)
	}

	if tx.Platform != ledger.PlatformN26 {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformN26)
	}
	if tx.Type != ledger.TypeInterest {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeInterest)
	}
	wantDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	if !tx.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", tx.Date, wantDate)
	}
	// ComputeDIRT (task 4.4) reads Quantity*Price as the credited
	// amount; a manual interest entry has no natural "quantity", so
	// Quantity is fixed at 1 and the full amount lives in Price.
	if !tx.Quantity.Equal(decimal.NewFromInt(1)) {
		t.Errorf("Quantity = %s, want 1", tx.Quantity)
	}
	if !tx.Price.Equal(decimal.RequireFromString("200.00")) {
		t.Errorf("Price (interest amount) = %s, want 200.00", tx.Price)
	}
	if tx.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", tx.Currency)
	}
	if tx.Instrument != savingsInstrument {
		t.Errorf("Instrument = %q, want %q", tx.Instrument, savingsInstrument)
	}
}

func TestNewInterestCredit_MalformedDate_ReturnsExplicitError(t *testing.T) {
	_, err := NewInterestCredit("not-a-date", "200.00", "EUR")
	if err == nil {
		t.Fatal("expected an error for a malformed date, got nil")
	}
}

func TestNewInterestCredit_MalformedAmount_ReturnsExplicitError(t *testing.T) {
	_, err := NewInterestCredit("2024-06-01", "not-a-number", "EUR")
	if err == nil {
		t.Fatal("expected an error for a malformed amount, got nil")
	}
}

func TestNewInterestCredit_EmptyCurrency_ReturnsExplicitError(t *testing.T) {
	_, err := NewInterestCredit("2024-06-01", "200.00", "")
	if err == nil {
		t.Fatal("expected an error for an empty currency, got nil")
	}
}

func TestNewInterestCredit_NegativeAmount_ReturnsExplicitError(t *testing.T) {
	// A negative interest credit isn't meaningful for this entry
	// point — reject it rather than silently taking the absolute
	// value, since a negative amount more likely indicates a typo or
	// a misunderstanding of what's being entered.
	_, err := NewInterestCredit("2024-06-01", "-50.00", "EUR")
	if err == nil {
		t.Fatal("expected an error for a negative amount, got nil")
	}
}

func TestNewInterestCredit_SameInputsTwice_SameFingerprint(t *testing.T) {
	// Confirms manual entries dedupe the same way parser-produced
	// transactions do, if the same credit is (re-)entered twice.
	a, err := NewInterestCredit("2024-06-01", "200.00", "EUR")
	if err != nil {
		t.Fatalf("NewInterestCredit: %v", err)
	}
	b, err := NewInterestCredit("2024-06-01", "200.00", "EUR")
	if err != nil {
		t.Fatalf("NewInterestCredit: %v", err)
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("expected identical manual entries to fingerprint the same, got %q and %q", a.Fingerprint(), b.Fingerprint())
	}
}
