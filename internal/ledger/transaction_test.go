package ledger

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

func baseTransaction(t *testing.T) Transaction {
	t.Helper()
	return Transaction{
		Platform:    PlatformDegiro,
		Type:        TypeBuy,
		Date:        mustDate(t, "2024-03-15"),
		Instrument:  "IE00B4L5Y983", // an ISIN
		Quantity:    decimal.NewFromInt(10),
		Price:       decimal.NewFromFloat(85.32),
		Currency:    "EUR",
		SourceRef:   "degiro-order-12345",
		Description: "ISHARES CORE MSCI WORLD",
	}
}

func TestFingerprint_IdenticalTransactions_SameFingerprint(t *testing.T) {
	a := baseTransaction(t)
	b := baseTransaction(t)

	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("expected identical transactions to have the same fingerprint, got %q and %q", a.Fingerprint(), b.Fingerprint())
	}
}

func TestFingerprint_DiffersOnlyBySourceRef_SameFingerprint(t *testing.T) {
	a := baseTransaction(t)
	b := baseTransaction(t)
	b.SourceRef = "a-completely-different-broker-note-id"

	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("expected transactions differing only by SourceRef (non-identifying) to share a fingerprint, got %q and %q", a.Fingerprint(), b.Fingerprint())
	}
}

func TestFingerprint_DiffersOnlyByDescription_SameFingerprint(t *testing.T) {
	a := baseTransaction(t)
	a.Description = "ISHARES CORE MSCI WORLD"
	b := baseTransaction(t)
	b.Description = "A totally different display name"

	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("expected transactions differing only by Description (non-identifying) to share a fingerprint, got %q and %q", a.Fingerprint(), b.Fingerprint())
	}
}

func TestFingerprint_DiffersByIdentifyingField_DifferentFingerprint(t *testing.T) {
	tests := []struct {
		name   string
		modify func(tx *Transaction)
	}{
		{"platform", func(tx *Transaction) { tx.Platform = PlatformIBKR }},
		{"type", func(tx *Transaction) { tx.Type = TypeSell }},
		{"date", func(tx *Transaction) { tx.Date = mustDate(t, "2024-03-16") }},
		{"instrument", func(tx *Transaction) { tx.Instrument = "IE00FIXTURE04" }},
		{"quantity", func(tx *Transaction) { tx.Quantity = decimal.NewFromInt(11) }},
		{"price", func(tx *Transaction) { tx.Price = decimal.NewFromFloat(85.33) }},
		{"currency", func(tx *Transaction) { tx.Currency = "USD" }},
	}

	base := baseTransaction(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modified := baseTransaction(t)
			tt.modify(&modified)
			if base.Fingerprint() == modified.Fingerprint() {
				t.Errorf("expected changing %s to change the fingerprint, but it stayed %q", tt.name, base.Fingerprint())
			}
		})
	}
}

func TestFingerprint_Deterministic_AcrossCalls(t *testing.T) {
	tx := baseTransaction(t)
	f1 := tx.Fingerprint()
	f2 := tx.Fingerprint()
	if f1 != f2 {
		t.Errorf("expected repeated calls to Fingerprint() to be stable, got %q then %q", f1, f2)
	}
}
