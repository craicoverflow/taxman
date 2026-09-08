package engine

import (
	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/audit"
)

// AuditRecords links every chargeable event in this result — deemed
// and actual — back to the rule version that taxed it, satisfying
// SPEC.md §2's requirement that every computed liability figure
// resolves to an audit.Record.
//
// Unlike audit.FromDisposals, which applies one rate to a whole run,
// each record here carries the rate resolved at its OWN event date:
// a lot's 8-year anniversary and its eventual sale can fall either
// side of a rate change, and recording both at one rate would lose
// exactly the fact the audit trail exists to preserve.
//
// LiabilityAmount is the event's net charge after any
// deemed-disposal credit, and is negative on an event that repays
// tax — a repayment is a computed figure too, and blanking it to zero
// would leave the largest surprise on a return with no trail.
func (r *FundTaxResult) AuditRecords() []audit.Record {
	records := make([]audit.Record, 0, len(r.DeemedDisposals)+len(r.Disposals))

	for _, d := range r.DeemedDisposals {
		records = append(records, audit.Record{
			Instrument:        r.Instrument,
			Kind:              audit.KindExitTax,
			LiabilityAmount:   d.TaxDue,
			DisposalDate:      d.AnniversaryDate,
			RuleEffectiveFrom: d.RuleEffectiveFrom,
			RuleRate:          d.RuleRate,
		})
	}

	for _, d := range r.Disposals {
		records = append(records, audit.Record{
			Instrument:        r.Instrument,
			Kind:              audit.KindExitTax,
			LiabilityAmount:   d.TaxDue,
			DisposalDate:      d.Date,
			RuleEffectiveFrom: d.RuleEffectiveFrom,
			RuleRate:          d.RuleRate,
		})
	}

	return records
}

// TotalLiability is the signed net of everything this result charges
// and repays, at full precision — what the holding adds to (or takes
// off) the return once the deemed-disposal credits are applied. The
// rounded TaxDue and RefundDue fields are the figures to file; this is
// the one to reconcile the audit records against.
func (r *FundTaxResult) TotalLiability() decimal.Decimal {
	total := decimal.Zero
	for _, d := range r.DeemedDisposals {
		total = total.Add(d.TaxDue)
	}
	for _, d := range r.Disposals {
		total = total.Add(d.TaxDue)
	}
	return total
}
