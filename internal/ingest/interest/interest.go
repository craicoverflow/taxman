// Package interest builds hand-entered interest credits — the savings
// interest a user types into the dashboard's "Log interest payments"
// form because their bank has no clean CSV export (SPEC.md §7).
//
// There is no list of supported institutions, by design. DIRT is DIRT:
// engine.ComputeDIRT sums every interest-type transaction and applies
// the rate for the credit date, and no Irish rule turns on which bank
// paid it. So the account is named by the user in free text ("Rainy
// day", "Revolut", whatever they call it), and that name is all taxman
// keeps: it rides on the transaction's Instrument so two accounts stay
// separable rows on the dashboard, and it is what makes two credits on
// the same date for the same amount distinct rather than duplicates.
package interest

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

const dateLayout = "2006-01-02"

// MaxSourceLen bounds a source name. It exists to keep a pasted
// accident out of the ledger, not to constrain naming — no real
// account name comes close.
const MaxSourceLen = 100

// NormalizeSource trims a user-typed source name and collapses runs of
// internal whitespace, so "  Rainy  day " and "Rainy day" name the same
// account rather than two that merely look alike. Case is left alone:
// it's the user's name for their own account.
func NormalizeSource(source string) string {
	return strings.Join(strings.Fields(source), " ")
}

// NewCredit builds a ledger.Transaction for one hand-entered interest
// credit. source is the user's own name for the account it was paid on
// (required — an unnamed credit couldn't be told apart from another
// account's); date must be YYYY-MM-DD; amount must be a positive
// decimal string. Currency is fixed to EUR, which is what
// engine.ComputeDIRT requires.
//
// Quantity is fixed at 1 and the full credited amount is carried in
// Price, matching how engine.ComputeDIRT reads an interest
// transaction's amount as Quantity*Price.
func NewCredit(source, date, amount string) (ledger.Transaction, error) {
	name := NormalizeSource(source)
	if name == "" {
		return ledger.Transaction{}, fmt.Errorf("interest: source must not be empty")
	}
	if len(name) > MaxSourceLen {
		return ledger.Transaction{}, fmt.Errorf("interest: source %q is longer than %d characters", name, MaxSourceLen)
	}

	parsedDate, err := time.Parse(dateLayout, date)
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("interest: parsing date %q: %w", date, err)
	}

	parsedAmount, err := decimal.NewFromString(amount)
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("interest: parsing amount %q: %w", amount, err)
	}
	if !parsedAmount.IsPositive() {
		return ledger.Transaction{}, fmt.Errorf("interest: amount %q must be positive", amount)
	}

	return ledger.Transaction{
		Platform:   ledger.PlatformManual,
		Type:       ledger.TypeInterest,
		Date:       parsedDate.UTC(),
		Instrument: name,
		Quantity:   decimal.NewFromInt(1),
		Price:      parsedAmount,
		Currency:   "EUR",
	}, nil
}
