package engine

import (
	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// OpenPosition is what remains of one instrument's holding after FIFO
// disposals: the quantity not yet sold and the euro cost basis of the
// lots that survived matching. See SPEC.md §9. It carries no tax-rule
// logic — the portfolio page (internal/web) uses it to show cost
// basis against live market value.
type OpenPosition struct {
	Quantity  decimal.Decimal
	CostBasis decimal.Decimal
}

// OpenPositions runs the same FIFO lot matching as ComputeCGT over one
// instrument's buy/rsu_vest/sell transactions and reports what is left
// unsold: the summed remaining quantity and the summed remaining cost
// basis of the lots FIFO left untouched (oldest consumed first). The
// remaining cost basis of a partially consumed lot is its per-unit
// cost times the quantity still held, exactly as matchFIFO values the
// consumed side.
//
// Non-EUR transactions are restated in euro at their transaction-date
// ECB reference rate, exactly as ComputeCGT does. An oversell is the
// same explicit error matchFIFO already returns. A holding with no
// surviving lots yields a zero-valued OpenPosition, not an error.
func OpenPositions(txs []ledger.Transaction) (OpenPosition, error) {
	_, lots, err := matchDisposalsFIFO(txs, "OpenPositions")
	if err != nil {
		return OpenPosition{}, err
	}

	pos := OpenPosition{Quantity: decimal.Zero, CostBasis: decimal.Zero}
	for _, lot := range lots {
		if lot.Remaining.IsZero() {
			continue
		}
		pos.Quantity = pos.Quantity.Add(lot.Remaining)
		pos.CostBasis = pos.CostBasis.Add(lot.Remaining.Mul(lot.unitCost()))
	}
	return pos, nil
}
