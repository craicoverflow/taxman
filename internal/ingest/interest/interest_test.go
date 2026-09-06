package interest

import (
	"testing"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func TestNewCredit_BuildsEURManualCreditNamedByTheUser(t *testing.T) {
	tx, err := NewCredit("Rainy day", "2024-05-01", "12.34")
	if err != nil {
		t.Fatalf("NewCredit: %v", err)
	}
	if tx.Platform != ledger.PlatformManual {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformManual)
	}
	if tx.Type != ledger.TypeInterest {
		t.Errorf("Type = %q, want interest", tx.Type)
	}
	if tx.Instrument != "Rainy day" {
		t.Errorf("Instrument = %q, want the source name verbatim", tx.Instrument)
	}
	if tx.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", tx.Currency)
	}
	if tx.Quantity.String() != "1" {
		t.Errorf("Quantity = %s, want 1", tx.Quantity)
	}
	if tx.Price.String() != "12.34" {
		t.Errorf("Price = %s, want the full credited amount", tx.Price)
	}
	if tx.Date.Format("2006-01-02") != "2024-05-01" {
		t.Errorf("Date = %s, want 2024-05-01", tx.Date)
	}
}

func TestNewCredit_AnySourceNameIsAccepted(t *testing.T) {
	// There is no registry of banks: whatever the user calls the
	// account is the account.
	for _, name := range []string{"Revolut", "An Post", "credit union #2", "Mam's account"} {
		tx, err := NewCredit(name, "2024-05-01", "1.00")
		if err != nil {
			t.Errorf("NewCredit(%q): %v", name, err)
			continue
		}
		if tx.Instrument != name {
			t.Errorf("NewCredit(%q): Instrument = %q", name, tx.Instrument)
		}
	}
}

func TestNewCredit_SourceIsNormalizedSoLookalikesAreOneAccount(t *testing.T) {
	spaced, err := NewCredit("  Rainy   day ", "2024-05-01", "12.34")
	if err != nil {
		t.Fatalf("NewCredit: %v", err)
	}
	tidy, err := NewCredit("Rainy day", "2024-05-01", "12.34")
	if err != nil {
		t.Fatalf("NewCredit: %v", err)
	}
	if spaced.Instrument != tidy.Instrument {
		t.Errorf("Instrument = %q and %q, want one normalized name", spaced.Instrument, tidy.Instrument)
	}
	if spaced.Fingerprint() != tidy.Fingerprint() {
		t.Error("the same credit typed with stray spaces must not become a second ledger row")
	}
}

func TestNewCredit_DifferentSourcesAreDistinctCredits(t *testing.T) {
	// Same date, same amount, different account — two real credits,
	// not one duplicate.
	a, err := NewCredit("Revolut", "2024-05-01", "12.34")
	if err != nil {
		t.Fatalf("NewCredit: %v", err)
	}
	b, err := NewCredit("An Post", "2024-05-01", "12.34")
	if err != nil {
		t.Fatalf("NewCredit: %v", err)
	}
	if a.Fingerprint() == b.Fingerprint() {
		t.Error("credits from two different accounts share a fingerprint")
	}
}

func TestNewCredit_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name             string
		source, date, am string
	}{
		{"empty source", "", "2024-05-01", "12.34"},
		{"whitespace-only source", "   ", "2024-05-01", "12.34"},
		{"malformed date", "Revolut", "not-a-date", "12.34"},
		{"non-positive amount", "Revolut", "2024-05-01", "-1"},
		{"zero amount", "Revolut", "2024-05-01", "0"},
		{"malformed amount", "Revolut", "2024-05-01", "twelve"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewCredit(c.source, c.date, c.am); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestNewCredit_RejectsAnOverlongSource(t *testing.T) {
	long := make([]byte, MaxSourceLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := NewCredit(string(long), "2024-05-01", "12.34"); err == nil {
		t.Error("expected an over-length source to be rejected")
	}
}
