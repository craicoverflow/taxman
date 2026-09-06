package engine

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/taxrules"
)

// ExitTaxResult is the outcome of computing exit tax for a single
// EXIT_TAX_FUND holding's transactions. Unlike CGTResult, there is no
// annual exemption and no loss relief.
//
// "No loss relief" is applied PER CHARGEABLE EVENT: each disposal is
// taxed on its own gain, and a disposal that makes a loss contributes
// zero — it never nets against a gain on another disposal, not even
// another disposal of the same fund in the same year. TotalGain is the
// raw arithmetic sum of every disposal's Gain (which can be negative
// and is shown for transparency); TaxableGain is the sum of only the
// positive per-disposal gains, and is what the rate is applied to.
type ExitTaxResult struct {
	Disposals   []Disposal
	TotalGain   decimal.Decimal // raw sum of every Disposal.Gain; may be negative
	TaxableGain decimal.Decimal // sum of the positive per-disposal gains only
	TaxDue      decimal.Decimal
}

// ComputeExitTax computes exit-tax liability for an EXIT_TAX_FUND
// holding's actual (not deemed) disposals, using FIFO lot matching —
// the same mechanics as ComputeCGT (see matchDisposalsFIFO), but with
// exit tax's own rules applied: no annual exemption (Ireland's
// exit-tax regime has none), and no loss relief — a loss on this
// holding cannot offset gains elsewhere, so TaxableGain floors at
// zero rather than going negative. Non-EUR transactions are restated
// in euro at transaction-date ECB rates by the shared matcher, the
// same as ComputeCGT.
//
// This function handles only actual disposals — it does not yet track
// or compute deemed disposal (the 8-year clock is task 4.7; deemed
// disposal's liability computation is task 4.9, blocked pending a
// Revenue TDM citation per SPEC.md §6).
//
// CAVEAT (flagged, not blocked, per tasks/plan.md task 4.5): this
// assumes plain FIFO matching order for exit-tax disposals, mirroring
// CGT. Whether Revenue's exit-tax rules actually match in FIFO order
// has not been separately verified — worth a light check before
// relying on this for a real filing.
func ComputeExitTax(txs []ledger.Transaction) (*ExitTaxResult, error) {
	disposals, _, err := matchDisposalsFIFO(txs, "ComputeExitTax")
	if err != nil {
		return nil, err
	}
	return summarizeExitTax(disposals, rateDateFor(disposals))
}

// ComputeExitTaxForYear is ComputeExitTax restricted to disposals
// dated within the given calendar year, matched against the holding's
// full acquisition history and taxed at that year's rate (resolved at
// 31 December). It mirrors ComputeCGTForYear — see that function — and
// is what the dashboard's year selector and report.go use for
// exit-tax holdings. No annual exemption, no loss relief, same as
// ComputeExitTax.
func ComputeExitTaxForYear(txs []ledger.Transaction, year int) (*ExitTaxResult, error) {
	disposals, _, err := matchDisposalsFIFO(txs, "ComputeExitTaxForYear")
	if err != nil {
		return nil, err
	}
	return summarizeExitTax(disposalsInYear(disposals, year), yearEndDate(year))
}

// summarizeExitTax applies the exit-tax rate — looked up at rateDate —
// to the accumulated disposals. rateDate is the latest disposal's
// date (ComputeExitTax) or the tax year's 31 December
// (ComputeExitTaxForYear), and is ignored when disposals is empty.
func summarizeExitTax(disposals []Disposal, rateDate time.Time) (*ExitTaxResult, error) {
	totalGain := decimal.Zero
	taxable := decimal.Zero
	for _, d := range disposals {
		totalGain = totalGain.Add(d.Gain)
		// No loss relief: a losing disposal contributes zero, it does
		// not reduce the charge on a gaining disposal elsewhere in the
		// same fund/year. Only the positive gains are taxable.
		if d.Gain.IsPositive() {
			taxable = taxable.Add(d.Gain)
		}
	}

	result := &ExitTaxResult{
		Disposals:   disposals,
		TotalGain:   totalGain,
		TaxableGain: taxable,
	}

	if len(disposals) == 0 {
		result.TaxDue = decimal.Zero
		return result, nil
	}

	rate, err := taxrules.Lookup(taxrules.KindExitTax, rateDate)
	if err != nil {
		return nil, fmt.Errorf("engine: ComputeExitTax: %w", err)
	}

	result.TaxDue = roundTaxToWholeEuro(taxable.Mul(rate.Rate))

	return result, nil
}
