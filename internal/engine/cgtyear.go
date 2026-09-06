package engine

import (
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/taxrules"
)

// CGTDisposals FIFO-matches one holding's full transaction history and
// returns the resulting disposals (each already restated in euro at its
// transaction-date ECB rate), oldest first.
//
// It is the per-holding building block that AggregateCGTYear consumes:
// a caller that needs a tax year's CGT charge calls this once per
// CGT_ASSET holding — handling a per-holding match failure (an
// unresolvable FX rate, an oversell) however it needs to, e.g. the
// dashboard surfaces it on that holding's row and still renders the
// rest of the page — then hands the successful holdings to
// AggregateCGTYear, which does the year-level netting, loss relief and
// exemption that CANNOT be done one holding at a time.
func CGTDisposals(txs []ledger.Transaction) ([]Disposal, error) {
	disposals, _, err := matchDisposalsFIFO(txs, "CGTDisposals")
	return disposals, err
}

// CGTYearHolding is one CGT_ASSET holding's contribution to a tax
// year's aggregate CGT position: its in-year disposals and their net
// realised gain (which may be negative). It deliberately carries no
// per-holding tax figure — the annual exemption and loss relief are
// personal and assessed at the year level, so they cannot be
// attributed to a single holding.
type CGTYearHolding struct {
	Instrument   string
	Disposals    []Disposal
	RealisedGain decimal.Decimal // sum of this holding's in-year Disposal.Gain
}

// CGTYearResult is the aggregate CGT position for one Irish tax year
// across every CGT_ASSET holding, computed the way an individual's CGT
// is actually assessed:
//
//   - every disposal in the year is FIFO-matched against its holding's
//     full acquisition history, then all the gains and losses are
//     netted TOGETHER across every holding (TCA 1997 s.31) — a loss on
//     one holding reduces a gain on another;
//   - unused capital losses from earlier years are brought forward and
//     set against the net gain, but only far enough to leave the annual
//     exempt amount un-wasted (s.601(4));
//   - the single €1,270 personal annual exemption is then deducted
//     (s.601) — once for the year, never once per holding;
//   - the year's CGT rate is applied to what remains and the tax is
//     rounded down to whole euro.
//
// A net loss for the year (after netting) is carried forward in
// LossCarriedForward. Capital losses realised before the ledger's
// earliest transaction are unknowable and are NOT included — see the
// note on AggregateCGTYear.
type CGTYearResult struct {
	Year int

	Holdings []CGTYearHolding // per-holding breakdown, sorted by instrument, in-year disposals only

	// NetChargeableGain is the sum of every holding's in-year
	// RealisedGain: gains and losses netted together, before any
	// brought-forward loss or the annual exemption. May be negative.
	NetChargeableGain decimal.Decimal

	LossBroughtForward     decimal.Decimal // unused losses carried in from earlier years
	LossBroughtForwardUsed decimal.Decimal // how much of the above was set against this year's gain
	LossCarriedForward     decimal.Decimal // unused losses carried out to later years

	AnnualExemptionUsed decimal.Decimal // 0 .. the year's annual exemption
	TaxableGain         decimal.Decimal // amount the rate is applied to; always >= 0
	TaxDue             decimal.Decimal

	RuleEffectiveFrom time.Time
	Rate              decimal.Decimal
}

// AggregateCGTYear computes the aggregate CGT position for tax year
// `year` from every CGT_ASSET holding's FIFO-matched disposals (keyed
// by instrument; get each value from CGTDisposals). The caller is
// responsible for passing ONLY holdings it has classified CGT_ASSET.
//
// Loss carry-forward is derived statelessly: the function replays every
// tax year from the earliest disposal up to `year`, netting each year's
// gains and losses across all holdings and rolling any unused net loss
// forward under the s.601(4) restriction (brought-forward losses are
// used only down to the annual exemption, never wasting it). It
// therefore needs each holding's disposals across ALL years, not just
// `year`. Capital losses realised before the ledger's earliest
// transaction cannot be seen and are not accounted for.
func AggregateCGTYear(disposalsByHolding map[string][]Disposal, year int) (*CGTYearResult, error) {
	rate, err := taxrules.Lookup(taxrules.KindCGT, yearEndDate(year))
	if err != nil {
		return nil, fmt.Errorf("engine: AggregateCGTYear: %w", err)
	}

	result := &CGTYearResult{
		Year:              year,
		RuleEffectiveFrom: rate.EffectiveFrom,
		Rate:              rate.Rate,
	}

	instruments := make([]string, 0, len(disposalsByHolding))
	for instr := range disposalsByHolding {
		instruments = append(instruments, instr)
	}
	sort.Strings(instruments)

	earliestYear := 0
	for _, instr := range instruments {
		for _, d := range disposalsByHolding[instr] {
			if earliestYear == 0 || d.Date.Year() < earliestYear {
				earliestYear = d.Date.Year()
			}
		}
	}

	// No disposals anywhere, or none on or before `year`: a zero result
	// (a valid outcome, not an error — a year in which nothing was sold).
	if earliestYear == 0 || earliestYear > year {
		return result, nil
	}

	// netGainForYear sums every holding's disposal gains (and losses)
	// dated in tax year y — the s.31 aggregation for that year.
	netGainForYear := func(y int) decimal.Decimal {
		net := decimal.Zero
		for _, instr := range instruments {
			for _, d := range disposalsByHolding[instr] {
				if d.Date.Year() == y {
					net = net.Add(d.Gain)
				}
			}
		}
		return net
	}

	// Replay earliestYear .. year, evolving the brought-forward loss
	// pool. Only the target year's figures are reported; the earlier
	// years exist purely to build up (and partially consume) the pool.
	lossPool := decimal.Zero
	var lossPoolIntoTargetYear, lossUsedInTargetYear decimal.Decimal
	for y := earliestYear; y <= year; y++ {
		netY := netGainForYear(y)
		if y == year {
			lossPoolIntoTargetYear = lossPool
			result.NetChargeableGain = netY
		}

		if !netY.IsPositive() {
			// Net loss (or exactly zero) for the year: nothing
			// chargeable; any loss joins the pool for later years.
			lossPool = lossPool.Add(netY.Abs())
			continue
		}

		// Net gain: bring forward earlier losses, but only enough to
		// reduce the net gain to the annual exemption (s.601(4) — the
		// exemption must not be wasted by over-using brought-forward
		// losses).
		usable := netY.Sub(rate.AnnualExemption)
		if usable.IsNegative() {
			usable = decimal.Zero
		}
		used := decimal.Min(lossPool, usable)
		lossPool = lossPool.Sub(used)
		if y == year {
			lossUsedInTargetYear = used
		}
	}

	result.LossBroughtForward = lossPoolIntoTargetYear
	result.LossBroughtForwardUsed = lossUsedInTargetYear
	result.LossCarriedForward = lossPool

	if afterLosses := result.NetChargeableGain.Sub(lossUsedInTargetYear); afterLosses.IsPositive() {
		exemptionUsed := decimal.Min(afterLosses, rate.AnnualExemption)
		taxable := afterLosses.Sub(exemptionUsed)
		if taxable.IsNegative() {
			taxable = decimal.Zero
		}
		result.AnnualExemptionUsed = exemptionUsed
		result.TaxableGain = taxable
		result.TaxDue = roundTaxToWholeEuro(taxable.Mul(rate.Rate))
	}

	// Per-holding breakdown: this year's disposals only, for display
	// and the audit trail.
	for _, instr := range instruments {
		var inYear []Disposal
		gain := decimal.Zero
		for _, d := range disposalsByHolding[instr] {
			if d.Date.Year() == year {
				inYear = append(inYear, d)
				gain = gain.Add(d.Gain)
			}
		}
		if len(inYear) == 0 {
			continue
		}
		sort.SliceStable(inYear, func(i, j int) bool { return inYear[i].Date.Before(inYear[j].Date) })
		result.Holdings = append(result.Holdings, CGTYearHolding{
			Instrument:   instr,
			Disposals:    inYear,
			RealisedGain: gain,
		})
	}

	return result, nil
}
