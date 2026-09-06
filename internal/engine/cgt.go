package engine

import (
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/taxrules"
)

// Disposal is one sell event matched against one or more Lots via
// FIFO. See SPEC.md §2.
type Disposal struct {
	Date      time.Time
	Quantity  decimal.Decimal
	Proceeds  decimal.Decimal
	CostBasis decimal.Decimal
	Gain      decimal.Decimal // Proceeds - CostBasis; may be negative (a loss)
}

// CGTResult is the outcome of computing CGT for a single holding's
// buy/sell transactions.
type CGTResult struct {
	Disposals   []Disposal
	TotalGain   decimal.Decimal // sum of all Disposal.Gain, before the annual exemption
	TaxableGain decimal.Decimal // TotalGain less the annual exemption, floored at zero
	TaxDue      decimal.Decimal
}

// ComputeCGT computes CGT liability for a CGT_ASSET holding's
// transactions using FIFO lot matching. A non-EUR transaction is
// restated in euro at the ECB reference rate for its own date (see
// toEURPrices and internal/fx) before matching; a rate that can't be
// resolved is an explicit error rather than a silent guess.
// txs may be a mix of buy, rsu_vest, and sell events for one
// instrument, in any order; ComputeCGT sorts by date before matching.
// An rsu_vest creates a lot exactly like a buy, using its vest-date
// FMV as cost basis.
//
// NOTE: this is plain FIFO. It does NOT yet implement TCA s.581's
// four-week same-share reacquisition rule — see task 4.8 in
// tasks/plan.md, blocked pending a Revenue TDM citation. A sell
// followed by a repurchase within four weeks will currently be
// computed via plain FIFO, which may be wrong; see engine.doc.go and
// SPEC.md §6 for the "ask first" boundary this represents.
func ComputeCGT(txs []ledger.Transaction) (*CGTResult, error) {
	disposals, _, err := matchDisposalsFIFO(txs, "ComputeCGT")
	if err != nil {
		return nil, err
	}
	return summarizeCGT(disposals, rateDateFor(disposals))
}

// Per-tax-year CGT is NOT computed one holding at a time: the €1,270
// annual exemption is personal and applies once across the year, and
// gains and losses net across every holding (TCA 1997 s.31). That
// aggregate lives in AggregateCGTYear (see cgtyear.go); cmd/taxman/
// report.go and the dashboard's year selector call CGTDisposals per
// holding and then AggregateCGTYear once. ComputeCGT here remains for
// the single-holding disposal mechanics the golden fixtures exercise.

// matchDisposalsFIFO is the FIFO lot-building/matching loop shared by
// ComputeCGT and ComputeExitTax: both regimes match disposals against
// lots identically (build a lot on buy/rsu_vest, consume oldest-first
// on sell) and differ only in how the resulting gains are taxed —
// see summarizeCGT vs. summarizeExitTax. It also applies the shared
// EUR conversion (toEURPrices) up front, so both regimes match on
// euro figures. callerName is used only to namespace error messages
// to whichever Compute* function is calling.
//
// It also returns the lots it built, oldest first, with each lot's
// Remaining reflecting FIFO consumption — OpenPositions sums the
// unconsumed remainder for the portfolio view (SPEC.md §9); the
// Compute* tax paths ignore it.
func matchDisposalsFIFO(txs []ledger.Transaction, callerName string) ([]Disposal, []*Lot, error) {
	eurTxs, err := toEURPrices(txs, callerName)
	if err != nil {
		return nil, nil, err
	}

	sorted := make([]ledger.Transaction, len(eurTxs))
	copy(sorted, eurTxs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date.Before(sorted[j].Date) })

	var lots []*Lot
	var disposals []Disposal

	for _, tx := range sorted {
		// tx.Currency is "EUR" here — toEURPrices restated any other
		// currency at its transaction-date ECB reference rate.
		switch tx.Type {
		case ledger.TypeBuy, ledger.TypeRSUVest:
			// A buy and an RSU vest both create a lot the same way: a
			// vest's tx.Price carries the vest-date fair market value
			// (set by the ETRADE parser, task 6.2), not a purchase
			// price, and that FMV is the lot's cost basis — it must
			// never be treated as zero cost or default to the eventual
			// sale price.
			lots = append(lots, &Lot{
				AcquiredDate: tx.Date,
				Quantity:     tx.Quantity,
				Remaining:    tx.Quantity,
				CostBasis:    tx.Quantity.Mul(tx.Price),
			})

		case ledger.TypeSell:
			disposal, err := matchFIFO(lots, tx, callerName)
			if err != nil {
				return nil, nil, err
			}
			disposals = append(disposals, disposal)

		default:
			return nil, nil, fmt.Errorf("engine: %s: unsupported transaction type %q", callerName, tx.Type)
		}
	}

	return disposals, lots, nil
}

// matchFIFO consumes lots (oldest first, mutating their Remaining) to
// satisfy sell's quantity, and returns the resulting Disposal.
func matchFIFO(lots []*Lot, sell ledger.Transaction, callerName string) (Disposal, error) {
	remaining := sell.Quantity
	costBasis := decimal.Zero

	for _, lot := range lots {
		if remaining.IsZero() {
			break
		}
		if lot.Remaining.IsZero() {
			continue
		}

		take := decimal.Min(remaining, lot.Remaining)
		costBasis = costBasis.Add(take.Mul(lot.unitCost()))
		lot.Remaining = lot.Remaining.Sub(take)
		remaining = remaining.Sub(take)
	}

	if !remaining.IsZero() {
		return Disposal{}, fmt.Errorf("engine: %s: sell of %s on %s exceeds available lot quantity by %s", callerName, sell.Quantity, sell.Date.Format("2006-01-02"), remaining)
	}

	proceeds := sell.Quantity.Mul(sell.Price)
	return Disposal{
		Date:      sell.Date,
		Quantity:  sell.Quantity,
		Proceeds:  proceeds,
		CostBasis: costBasis,
		Gain:      proceeds.Sub(costBasis),
	}, nil
}

// summarizeCGT applies the CGT rate and annual exemption — looked up
// at rateDate — to the accumulated disposals. Callers pass the most
// recent disposal's date (ComputeCGT, whole history) or the tax
// year's 31 December (ComputeCGTForYear); a given tax year has one
// rate/exemption in practice, so mixing disposals from more than one
// year into a single call still resolves a single rate and is only
// meaningful when they share one. rateDate is ignored when disposals
// is empty.
func summarizeCGT(disposals []Disposal, rateDate time.Time) (*CGTResult, error) {
	totalGain := decimal.Zero
	for _, d := range disposals {
		totalGain = totalGain.Add(d.Gain)
	}

	result := &CGTResult{
		Disposals: disposals,
		TotalGain: totalGain,
	}

	if len(disposals) == 0 {
		result.TaxableGain = decimal.Zero
		result.TaxDue = decimal.Zero
		return result, nil
	}

	rate, err := taxrules.Lookup(taxrules.KindCGT, rateDate)
	if err != nil {
		return nil, fmt.Errorf("engine: ComputeCGT: %w", err)
	}

	taxable := totalGain.Sub(rate.AnnualExemption)
	if taxable.IsNegative() {
		taxable = decimal.Zero
	}
	result.TaxableGain = taxable
	result.TaxDue = roundTaxToWholeEuro(taxable.Mul(rate.Rate))

	return result, nil
}

// latestDisposalDate returns the most recent Date across disposals.
// Callers must ensure disposals is non-empty.
func latestDisposalDate(disposals []Disposal) time.Time {
	latest := disposals[0].Date
	for _, d := range disposals[1:] {
		if d.Date.After(latest) {
			latest = d.Date
		}
	}
	return latest
}

// rateDateFor is the date a whole-history summary looks its rate up
// at: the most recent disposal's, or the zero time when there are
// none (summarize* ignore the date in that case).
func rateDateFor(disposals []Disposal) time.Time {
	if len(disposals) == 0 {
		return time.Time{}
	}
	return latestDisposalDate(disposals)
}

// disposalsInYear returns the disposals whose Date falls in the given
// calendar year, preserving order.
func disposalsInYear(disposals []Disposal, year int) []Disposal {
	var out []Disposal
	for _, d := range disposals {
		if d.Date.Year() == year {
			out = append(out, d)
		}
	}
	return out
}

// yearEndDate is 31 December of year, the point a tax year's rate and
// exemption are resolved at (mirrors cmd/taxman/report.go's own
// year-boundary lookup).
func yearEndDate(year int) time.Time {
	return time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
}
