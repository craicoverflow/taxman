package n26

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// savingsInstrument is the fixed instrument identifier used for every
// N26 interest credit. N26's savings account isn't a security with an
// ISIN — there's only one "holding" per user, so a constant
// identifier is enough for classify/engine to key off.
const savingsInstrument = "N26_SAVINGS"

const dateLayout = "2006-01-02"

// NewInterestCredit builds a ledger.Transaction for a single N26
// interest credit, entered manually rather than parsed from a file —
// see doc.go. date must be YYYY-MM-DD; amount must be a positive
// decimal string; currency must be non-empty (ISO 4217, e.g. "EUR").
// Quantity is fixed at 1 and the full credited amount is carried in
// Price, matching how internal/engine.ComputeDIRT reads an interest
// transaction's amount as Quantity*Price.
func NewInterestCredit(date, amount, currency string) (ledger.Transaction, error) {
	parsedDate, err := time.Parse(dateLayout, date)
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("n26: parsing date %q: %w", date, err)
	}

	parsedAmount, err := decimal.NewFromString(amount)
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("n26: parsing amount %q: %w", amount, err)
	}
	if !parsedAmount.IsPositive() {
		return ledger.Transaction{}, fmt.Errorf("n26: amount %q must be positive", amount)
	}

	if currency == "" {
		return ledger.Transaction{}, fmt.Errorf("n26: currency must not be empty")
	}

	return ledger.Transaction{
		Platform:   ledger.PlatformN26,
		Type:       ledger.TypeInterest,
		Date:       parsedDate.UTC(),
		Instrument: savingsInstrument,
		Quantity:   decimal.NewFromInt(1),
		Price:      parsedAmount,
		Currency:   currency,
	}, nil
}
