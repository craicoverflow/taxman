package traderepublic

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// savingsInstrument is the fixed instrument identifier used for every
// Trade Republic interest credit. The uninvested-cash balance that
// earns interest isn't a security with an ISIN — there's one such
// "holding" per user — so a constant identifier is enough for
// classify/engine to key off, and it is deliberately distinct from
// n26's N26_SAVINGS so the two sources stay separable on the dashboard.
const savingsInstrument = "TRADE_REPUBLIC_SAVINGS"

const dateLayout = "2006-01-02"

// NewInterestCredit builds a ledger.Transaction for a single Trade
// Republic interest credit, entered manually rather than parsed from a
// file — see doc.go. date must be YYYY-MM-DD; amount must be a positive
// decimal string; currency must be non-empty (ISO 4217, e.g. "EUR").
// Quantity is fixed at 1 and the full credited amount is carried in
// Price, matching how internal/engine.ComputeDIRT reads an interest
// transaction's amount as Quantity*Price.
func NewInterestCredit(date, amount, currency string) (ledger.Transaction, error) {
	parsedDate, err := time.Parse(dateLayout, date)
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("traderepublic: parsing date %q: %w", date, err)
	}

	parsedAmount, err := decimal.NewFromString(amount)
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("traderepublic: parsing amount %q: %w", amount, err)
	}
	if !parsedAmount.IsPositive() {
		return ledger.Transaction{}, fmt.Errorf("traderepublic: amount %q must be positive", amount)
	}

	if currency == "" {
		return ledger.Transaction{}, fmt.Errorf("traderepublic: currency must not be empty")
	}

	return ledger.Transaction{
		Platform:   ledger.PlatformTradeRepublic,
		Type:       ledger.TypeInterest,
		Date:       parsedDate.UTC(),
		Instrument: savingsInstrument,
		Quantity:   decimal.NewFromInt(1),
		Price:      parsedAmount,
		Currency:   currency,
	}, nil
}
