package engine

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/taxrules"
)

// DIRTResult is the outcome of computing DIRT liability across a set
// of interest-credit transactions.
type DIRTResult struct {
	TotalInterest decimal.Decimal
	TaxDue        decimal.Decimal
}

// ComputeDIRT sums interest credits (ledger.TypeInterest) and applies
// the DIRT rate looked up by the latest credit's date — no lot
// matching is involved, per SPEC.md §5. Non-interest transactions in
// txs are ignored, not an error: callers may pass a holding's full
// transaction history without pre-filtering. All interest
// transactions must be EUR-denominated for the same reason as
// ComputeCGT — no FX rate source exists yet.
func ComputeDIRT(txs []ledger.Transaction) (*DIRTResult, error) {
	total := decimal.Zero
	var latestDate *ledger.Transaction

	for i := range txs {
		tx := txs[i]
		if tx.Type != ledger.TypeInterest {
			continue
		}
		if tx.Currency != "EUR" {
			return nil, fmt.Errorf("engine: ComputeDIRT: interest credit on %s in %s is not EUR; FX conversion is not yet implemented", tx.Date.Format("2006-01-02"), tx.Currency)
		}

		amount := tx.Quantity.Mul(tx.Price)
		total = total.Add(amount)

		if latestDate == nil || tx.Date.After(latestDate.Date) {
			latestDate = &tx
		}
	}

	result := &DIRTResult{TotalInterest: total}

	if latestDate == nil {
		result.TaxDue = decimal.Zero
		return result, nil
	}

	rate, err := taxrules.Lookup(taxrules.KindDIRT, latestDate.Date)
	if err != nil {
		return nil, fmt.Errorf("engine: ComputeDIRT: %w", err)
	}

	result.TaxDue = roundTaxToWholeEuro(total.Mul(rate.Rate))
	return result, nil
}

// ComputeDIRTForYear is ComputeDIRT restricted to interest credits
// dated within the given calendar year — the per-year view the
// dashboard's year selector and report.go need. Unlike CGT/exit tax,
// no lot history is involved, so this is a plain date filter followed
// by ComputeDIRT; the rate resolves to that year's, since every
// remaining credit falls inside it.
func ComputeDIRTForYear(txs []ledger.Transaction, year int) (*DIRTResult, error) {
	var inYear []ledger.Transaction
	for _, tx := range txs {
		if tx.Type == ledger.TypeInterest && tx.Date.Year() == year {
			inYear = append(inYear, tx)
		}
	}
	return ComputeDIRT(inYear)
}
