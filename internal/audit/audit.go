package audit

import (
	"time"

	"github.com/shopspring/decimal"
)

// Kind identifies which tax regime a Record's liability figure was
// computed under.
type Kind string

const (
	KindCGT     Kind = "cgt"
	KindExitTax Kind = "exit_tax"
	KindDIRT    Kind = "dirt"
)

// Record links one computed liability figure back to the source
// transactions, lots, and tax rule version that produced it — see
// SPEC.md §2. Every liability figure the engine produces must have a
// corresponding Record; see internal/engine's Compute* functions and
// FromDisposals below, which is how that link gets built.
type Record struct {
	Instrument         string
	Kind               Kind
	LiabilityAmount    decimal.Decimal
	SourceTransactions []string // ledger.Transaction Fingerprints
	LotIDs             []string
	DisposalDate       time.Time
	RuleEffectiveFrom  time.Time // the taxrules.Rate's EffectiveFrom that was applied
	RuleRate           decimal.Decimal
}

// DisposalLike is the minimal shape of an internal/engine.Disposal
// this package needs. Defined locally (rather than importing
// internal/engine directly) to avoid an import cycle: internal/engine
// will depend on internal/audit to record its results, not the
// reverse.
type DisposalLike struct {
	Date      time.Time
	Quantity  decimal.Decimal
	Proceeds  decimal.Decimal
	CostBasis decimal.Decimal
	Gain      decimal.Decimal
}

// RateInfo is the minimal shape of an internal/taxrules.Rate this
// package needs, for the same import-cycle reason as DisposalLike.
type RateInfo struct {
	EffectiveFrom time.Time
	Rate          decimal.Decimal
}

// FromDisposals builds one Record per disposal, each recording the
// per-disposal liability at rate.Rate (no annual exemption or other
// aggregate adjustment is applied here — see the note below) alongside
// the disposal's own date and the rate version used.
//
// NOTE: for regimes with an aggregate adjustment across all
// disposals in a run (CGT's annual exemption, in particular),
// LiabilityAmount here is the disposal's gain at rate.Rate BEFORE
// that adjustment — it will not sum to the aggregate TaxDue a
// CGTResult/ExitTaxResult reports. Reconciling per-disposal records
// against an aggregate-adjusted total is left for a future task; for
// now every Record still traces its own disposal, lots, and rule
// version faithfully, which is what SPEC.md §2's audit trail
// requires — it does not yet claim to net against the exemption.
func FromDisposals(instrument string, kind Kind, disposals []DisposalLike, rate RateInfo) ([]Record, error) {
	records := make([]Record, 0, len(disposals))
	for _, d := range disposals {
		// A loss (on any regime, including exit tax's no-loss-relief
		// rule) still gets recorded faithfully as a Record with zero
		// liability, not skipped — the audit trail should show the
		// disposal happened and what rule applied, even when no tax
		// was due on it.
		liability := d.Gain.Mul(rate.Rate)
		if liability.IsNegative() {
			liability = decimal.Zero
		}

		records = append(records, Record{
			Instrument:        instrument,
			Kind:              kind,
			LiabilityAmount:   liability,
			DisposalDate:      d.Date,
			RuleEffectiveFrom: rate.EffectiveFrom,
			RuleRate:          rate.Rate,
		})
	}
	return records, nil
}
