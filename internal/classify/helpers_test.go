package classify

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// baseTransaction builds a minimal valid Transaction for the given
// instrument, for tests that only care about populating the ledger
// with distinct holdings.
func baseTransaction(t *testing.T, instrument string) ledger.Transaction {
	t.Helper()
	date, err := time.Parse("2006-01-02", "2024-03-15")
	if err != nil {
		t.Fatalf("parsing date: %v", err)
	}
	return ledger.Transaction{
		Platform:   ledger.PlatformDegiro,
		Type:       ledger.TypeBuy,
		Date:       date,
		Instrument: instrument,
		Quantity:   decimal.NewFromInt(10),
		Price:      decimal.NewFromFloat(85.32),
		Currency:   "EUR",
		SourceRef:  "test-ref",
	}
}
