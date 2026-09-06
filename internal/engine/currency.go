package engine

import (
	"fmt"

	"github.com/craicoverflow/taxman/internal/fx"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// toEURPrices returns txs with every non-EUR transaction's Price
// restated in euro at the ECB reference rate for that transaction's
// own date (see internal/fx). This is SPEC.md §2's Lot rule: a lot's
// cost basis and a disposal's proceeds are each fixed in euro at the
// FX rate on their transaction date, not re-translated later.
//
// EUR transactions pass through untouched. A transaction whose rate
// can't be resolved (unknown currency, or a date the embedded ECB
// series doesn't cover) is a hard error — the same "don't guess"
// stance the engine took when it rejected non-EUR outright, except the
// fix is now to refresh internal/fx/eurofxref-hist.csv rather than to
// write conversion code.
func toEURPrices(txs []ledger.Transaction, callerName string) ([]ledger.Transaction, error) {
	out := make([]ledger.Transaction, len(txs))
	for i, tx := range txs {
		if tx.Currency == "" || tx.Currency == "EUR" {
			out[i] = tx
			continue
		}

		rate, err := fx.Rate(tx.Currency, tx.Date)
		if err != nil {
			return nil, fmt.Errorf("engine: %s: converting the %s transaction on %s to EUR: %w",
				callerName, tx.Currency, tx.Date.Format("2006-01-02"), err)
		}

		converted := tx
		converted.Price = tx.Price.Mul(rate)
		converted.Currency = "EUR"
		out[i] = converted
	}
	return out, nil
}
