package engine

import (
	"time"

	"github.com/shopspring/decimal"
)

// Lot is a specific acquisition of a holding: a quantity acquired on a
// date at a given cost basis, tracked so disposals can be matched
// against it. See SPEC.md §2. Remaining shrinks as disposals consume
// the lot; a Lot with Remaining == 0 is fully consumed.
type Lot struct {
	AcquiredDate time.Time
	Quantity     decimal.Decimal // original quantity acquired
	Remaining    decimal.Decimal // quantity not yet matched to a disposal
	CostBasis    decimal.Decimal // total cost for Quantity (not per-unit)
}

// unitCost returns this lot's cost basis per unit.
func (l Lot) unitCost() decimal.Decimal {
	if l.Quantity.IsZero() {
		return decimal.Zero
	}
	return l.CostBasis.Div(l.Quantity)
}
