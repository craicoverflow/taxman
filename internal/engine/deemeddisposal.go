package engine

import (
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/fx"
	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/taxrules"
)

// deemedDisposalCycleYears is the interval at which Irish tax law
// deems a fund lot disposed of (and taxed) even if it hasn't been
// sold: 8 years, and every 8 years thereafter for as long as the lot
// is held. TCA 1997 s.747E(6) — "a deemed disposal and reacquisition
// of a material interest in an offshore fund at the ending of each
// period of 8 years beginning with the acquisition of the interest and
// each subsequent 8-year period".
const deemedDisposalCycleYears = 8

// NextDeemedDisposalAnniversary returns the next date on or after
// asOf at which lot crosses an 8-year deemed-disposal boundary
// (acquisition + 8, +16, +24, ... years). If asOf is exactly on an
// anniversary, that anniversary is treated as already reached and the
// following cycle is returned.
//
// This is pure date arithmetic: it takes no Classifier, performs no
// internal/taxrules lookup, and returns no liability figure. What is
// actually owed at a reached anniversary is ComputeFundTax's job.
func NextDeemedDisposalAnniversary(lot Lot, asOf time.Time) time.Time {
	next := lot.AcquiredDate.AddDate(deemedDisposalCycleYears, 0, 0)
	for !next.After(asOf) {
		next = next.AddDate(deemedDisposalCycleYears, 0, 0)
	}
	return next
}

// deemedDisposalAnniversaries returns every 8-year anniversary of
// acquired that falls on or before asOf, earliest first. An
// anniversary landing exactly on asOf counts as reached, matching
// NextDeemedDisposalAnniversary's boundary handling.
func deemedDisposalAnniversaries(acquired, asOf time.Time) []time.Time {
	var out []time.Time
	for d := acquired.AddDate(deemedDisposalCycleYears, 0, 0); !d.After(asOf); d = d.AddDate(deemedDisposalCycleYears, 0, 0) {
		out = append(out, d)
	}
	return out
}

// Valuer supplies the user-entered market value of one unit of a
// holding on a given day. internal/valuations.Store satisfies it; the
// engine defines the interface rather than importing that package so
// its own tests stay free of a SQLite dependency, exactly as
// Classifier does (SPEC.md §4).
//
// There is deliberately no fallback source. internal/prices is a
// live-quote client kept out of internal/engine and internal/audit by
// SPEC.md §4 and §9, and it serves a last price, not the historical
// close an 8-year-old anniversary needs.
type Valuer interface {
	ValuePerUnit(instrument string, on time.Time) (value decimal.Decimal, currency string, ok bool, err error)
}

// MissingValuationError blocks one holding's computation because a lot
// reached a deemed-disposal anniversary with no market value on record
// for that date. It names the lot and the date so the fix is a single
// obvious data entry, and — like UnclassifiedError — it blocks that
// holding only, never the whole run.
type MissingValuationError struct {
	Instrument      string
	LotAcquired     time.Time
	AnniversaryDate time.Time
	Quantity        decimal.Decimal
}

func (e *MissingValuationError) Error() string {
	return fmt.Sprintf(
		"engine: %s: the %s units acquired on %s reached their 8-year deemed-disposal anniversary on %s, but no market value is on record for that date; enter the value per unit before computing tax on this holding",
		e.Instrument,
		e.Quantity,
		e.LotAcquired.Format("2006-01-02"),
		e.AnniversaryDate.Format("2006-01-02"),
	)
}

// DeemedDisposal is one 8-year chargeable event on one lot: the units
// still held on that anniversary, valued at the market value on the
// date, against their ORIGINAL cost of acquisition.
//
// CumulativeTax is the tax on the whole rise since acquisition, not
// since the previous anniversary: Revenue TDM Part 27-04-01 §4.1.4
// directs that "the original cost of acquisition should be used", and
// TCA s.739D(2A) puts the same rule on the investment-undertaking side
// ("a previous '8-year event' is disregarded in calculating the
// gain"). CreditUsed then removes the tax already charged on earlier
// anniversaries of these same units, so TaxDue is what this event adds.
//
// TaxDue is signed and at full precision. It is NEGATIVE when the
// units have fallen since the last anniversary — tax already paid
// exceeds the cumulative charge, and the excess is repayable (Revenue
// TDM Part 27-01A-02 §4.4.5: "Where an overpayment of exit tax arises
// after such offset, the excess is repaid"; TCA Notes for Guidance
// s.747E(2)–(4): "the overpaid amount is refundable/available for
// set-off").
type DeemedDisposal struct {
	AnniversaryDate time.Time
	LotAcquired     time.Time
	Quantity        decimal.Decimal
	ValuePerUnit    decimal.Decimal // EUR, restated at the anniversary date's ECB rate
	Value           decimal.Decimal // Quantity * ValuePerUnit
	CostBasis       decimal.Decimal // original cost of those units
	Gain            decimal.Decimal // Value - CostBasis; may be negative
	CumulativeTax   decimal.Decimal // tax on the whole gain since acquisition
	CreditUsed      decimal.Decimal // tax already charged on earlier anniversaries of these units
	TaxDue          decimal.Decimal // CumulativeTax - CreditUsed; negative means repayable

	RuleEffectiveFrom time.Time
	RuleRate          decimal.Decimal
}

// FundDisposal is an actual (real) disposal of fund units with the
// deemed-disposal credit applied. The embedded Disposal carries the
// gain computed against the units' ORIGINAL cost — not a
// deemed-reacquisition uplift. s.747E(6) does say the interest is
// deemed reacquired at market value, but Revenue TDM Part 27-04-01
// §4.1.4 is explicit that on the later actual disposal "the original
// cost of acquisition should be used", with the earlier charge handled
// as a credit instead: "The total tax liability (between deemed and
// actual disposals) should not exceed the tax liability that arises on
// the actual disposal." Treating the reacquisition as a base uplift
// would produce a different, wrong figure.
//
// TaxDue is signed and at full precision: negative when the credit for
// earlier deemed disposals exceeds the tax on this disposal, which is
// the refund/set-off case in s.747E(3)(b).
type FundDisposal struct {
	Disposal

	TaxBeforeCredit decimal.Decimal // tax on this disposal's own gain
	CreditUsed      decimal.Decimal // deemed-disposal tax already paid on these units
	TaxDue          decimal.Decimal // TaxBeforeCredit - CreditUsed; negative means repayable

	RuleEffectiveFrom time.Time
	RuleRate          decimal.Decimal
}

// FundTaxResult is the outcome of computing exit tax for one
// EXIT_TAX_FUND holding across both kinds of chargeable event: actual
// disposals and 8-year deemed disposals.
//
// TaxDue and RefundDue are reported separately rather than as one
// signed total because they are separate lines on a return: TaxDue is
// the sum of the events that charge tax, RefundDue the sum of the
// events that repay it. Both are rounded down to whole euro — down is
// in the taxpayer's favour for a charge, and for a repayment claim it
// is the conservative direction (claim the whole euro, not the cents).
// That rounding of a repayment is a taxman convention, not a cited
// Revenue rule.
type FundTaxResult struct {
	Instrument      string
	Disposals       []FundDisposal
	DeemedDisposals []DeemedDisposal

	TotalGain       decimal.Decimal // raw sum of every event's gain; may be negative
	TaxableGain     decimal.Decimal // sum of only the positive per-event gains
	TaxBeforeCredit decimal.Decimal // tax on TaxableGain, before deemed-disposal credits
	CreditUsed      decimal.Decimal // deemed-disposal tax credited against later events

	TaxDue    decimal.Decimal // whole euro, >= 0
	RefundDue decimal.Decimal // whole euro, >= 0
}

// fundLot is a Lot plus the running deemed-disposal tax already
// charged on each unit still held in it. Carrying it per unit rather
// than per lot is what makes a partial sale work: selling half the lot
// takes half the credit with it and leaves the rest attached to the
// units that remain.
type fundLot struct {
	lot *Lot
	// deemedTaxPerUnit is the cumulative deemed-disposal tax charged
	// so far on one unit of this lot. It falls back towards zero when
	// an anniversary values the units below a previous one — see
	// DeemedDisposal.TaxDue.
	deemedTaxPerUnit decimal.Decimal
}

// fundEventKind orders events that land on the same date. A buy is
// processed before anything that could consume it; a deemed disposal
// comes before a same-day sale because s.747E(6) places the deemed
// disposal "immediately before the ending of the period". Revenue TDM
// Part 27-01A-02 §4.4.2 lets a FUND skip the deemed event for units
// sold in the same six-month valuation period, but that is an
// administrative easement for the fund's own withholding, not a rule
// for a self-assessing investor, so it is not applied here.
type fundEventKind int

const (
	eventBuy fundEventKind = iota
	eventDeemedDisposal
	eventSell
)

type fundEvent struct {
	date time.Time
	kind fundEventKind
	tx   ledger.Transaction // set for eventBuy and eventSell
	lot  int                // index into lots, set for eventDeemedDisposal
}

// ComputeFundTax computes exit-tax liability for one EXIT_TAX_FUND
// holding across both chargeable events Irish law recognises for a
// fund: actual disposals, and the 8-year deemed disposal in TCA 1997
// s.747E(6). Anniversaries falling on or before asOf are computed;
// later ones are not yet chargeable.
//
// It is a strict superset of ComputeExitTax: a holding no lot of which
// has yet reached an anniversary produces the same disposals and the
// same tax. The exit-tax rules ComputeExitTax applies still hold —
// no annual exemption, and no loss relief per chargeable event
// (s.747E(3) & (4), Revenue TDM Part 27-04-01 §4.1.6) — and each
// deemed disposal is its own chargeable event under the same rules.
//
// values must supply a market value for every anniversary reached, or
// computation for this holding is blocked with a *MissingValuationError
// naming the lot and date. Nothing is estimated: the anniversary value
// is user-entered data, like a holding's classification.
//
// Matching is FIFO, which for this regime is cited rather than
// assumed: Revenue TDM Part 27-04-01 §4.1.5 — "Where there has been a
// movement in units, the gain should be calculated on a FIFO basis."
func ComputeFundTax(instrument string, txs []ledger.Transaction, values Valuer, asOf time.Time) (*FundTaxResult, error) {
	if values == nil {
		return nil, fmt.Errorf("engine: ComputeFundTax: %s: a Valuer is required to compute deemed disposals", instrument)
	}

	eurTxs, err := toEURPrices(txs, "ComputeFundTax")
	if err != nil {
		return nil, err
	}

	events, lots, err := buildFundEvents(eurTxs, asOf)
	if err != nil {
		return nil, err
	}

	result := &FundTaxResult{Instrument: instrument}

	for _, ev := range events {
		switch ev.kind {
		case eventBuy:
			// Lots were built by buildFundEvents so that each
			// anniversary event could be generated up front; nothing
			// more to do as the buy comes round in date order.

		case eventDeemedDisposal:
			deemed, err := chargeDeemedDisposal(instrument, lots[ev.lot], ev.date, values)
			if err != nil {
				return nil, err
			}
			if deemed != nil {
				result.DeemedDisposals = append(result.DeemedDisposals, *deemed)
			}

		case eventSell:
			disposal, err := chargeFundDisposal(lots, ev.tx)
			if err != nil {
				return nil, err
			}
			result.Disposals = append(result.Disposals, *disposal)
		}
	}

	summarizeFundTax(result)
	return result, nil
}

// ComputeFundTaxForYear is ComputeFundTax restricted to chargeable
// events — actual and deemed — falling within the given calendar year.
//
// The whole history up to 31 December of that year is still walked, so
// that a disposal in the year is matched against its real acquisition
// lots and carries the credit for deemed disposals charged in earlier
// years. Only the reporting is scoped to the year.
func ComputeFundTaxForYear(instrument string, txs []ledger.Transaction, values Valuer, year int) (*FundTaxResult, error) {
	full, err := ComputeFundTax(instrument, txs, values, yearEndDate(year))
	if err != nil {
		return nil, err
	}

	scoped := &FundTaxResult{Instrument: instrument}
	for _, d := range full.Disposals {
		if d.Date.Year() == year {
			scoped.Disposals = append(scoped.Disposals, d)
		}
	}
	for _, d := range full.DeemedDisposals {
		if d.AnniversaryDate.Year() == year {
			scoped.DeemedDisposals = append(scoped.DeemedDisposals, d)
		}
	}

	summarizeFundTax(scoped)
	return scoped, nil
}

// buildFundEvents turns a holding's transactions into the full,
// date-ordered sequence of chargeable events, with the lots each
// deemed disposal attaches to. Anniversaries can be generated up front
// because a lot's acquisition date is its buy's date — no simulation
// is needed to know when its 8-year boundaries fall.
func buildFundEvents(txs []ledger.Transaction, asOf time.Time) ([]fundEvent, []*fundLot, error) {
	sorted := make([]ledger.Transaction, len(txs))
	copy(sorted, txs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date.Before(sorted[j].Date) })

	var events []fundEvent
	var lots []*fundLot

	for _, tx := range sorted {
		switch tx.Type {
		case ledger.TypeBuy, ledger.TypeRSUVest:
			lots = append(lots, &fundLot{
				lot: &Lot{
					AcquiredDate: tx.Date,
					Quantity:     tx.Quantity,
					Remaining:    tx.Quantity,
					CostBasis:    tx.Quantity.Mul(tx.Price),
				},
				deemedTaxPerUnit: decimal.Zero,
			})
			index := len(lots) - 1

			events = append(events, fundEvent{date: tx.Date, kind: eventBuy, tx: tx, lot: index})
			for _, anniversary := range deemedDisposalAnniversaries(tx.Date, asOf) {
				events = append(events, fundEvent{date: anniversary, kind: eventDeemedDisposal, lot: index})
			}

		case ledger.TypeSell:
			events = append(events, fundEvent{date: tx.Date, kind: eventSell, tx: tx})

		default:
			return nil, nil, fmt.Errorf("engine: ComputeFundTax: unsupported transaction type %q", tx.Type)
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].date.Equal(events[j].date) {
			return events[i].date.Before(events[j].date)
		}
		return events[i].kind < events[j].kind
	})

	return events, lots, nil
}

// chargeDeemedDisposal computes the 8-year charge on the units of fl
// still held at anniversary. It returns nil (no event) when the lot has
// already been fully sold: those units had a real disposal instead,
// and nothing remains to deem disposed.
func chargeDeemedDisposal(instrument string, fl *fundLot, anniversary time.Time, values Valuer) (*DeemedDisposal, error) {
	quantity := fl.lot.Remaining
	if !quantity.IsPositive() {
		return nil, nil
	}

	valuePerUnit, err := anniversaryValueEUR(instrument, fl, anniversary, values)
	if err != nil {
		return nil, err
	}

	rate, err := taxrules.Lookup(taxrules.KindExitTax, anniversary)
	if err != nil {
		return nil, fmt.Errorf("engine: ComputeFundTax: %s: deemed disposal on %s: %w", instrument, anniversary.Format("2006-01-02"), err)
	}

	value := quantity.Mul(valuePerUnit)
	costBasis := quantity.Mul(fl.lot.unitCost())
	gain := value.Sub(costBasis)

	// No loss relief, and the amount charged "cannot be of a negative
	// amount" (s.747E(3) & (4)): a fall below cost charges nothing, it
	// does not create relief against anything else.
	taxable := gain
	if taxable.IsNegative() {
		taxable = decimal.Zero
	}

	cumulativeTax := taxable.Mul(rate.Rate)
	creditUsed := quantity.Mul(fl.deemedTaxPerUnit)

	// The units carry forward the cumulative tax charged on them, so
	// the next chargeable event — deemed or actual — credits it.
	fl.deemedTaxPerUnit = cumulativeTax.Div(quantity)

	return &DeemedDisposal{
		AnniversaryDate:   anniversary,
		LotAcquired:       fl.lot.AcquiredDate,
		Quantity:          quantity,
		ValuePerUnit:      valuePerUnit,
		Value:             value,
		CostBasis:         costBasis,
		Gain:              gain,
		CumulativeTax:     cumulativeTax,
		CreditUsed:        creditUsed,
		TaxDue:            cumulativeTax.Sub(creditUsed),
		RuleEffectiveFrom: rate.EffectiveFrom,
		RuleRate:          rate.Rate,
	}, nil
}

// anniversaryValueEUR looks up the user-entered value for this
// anniversary and restates it in euro at that date's ECB reference
// rate — the same treatment every other amount gets (SPEC.md §2).
func anniversaryValueEUR(instrument string, fl *fundLot, anniversary time.Time, values Valuer) (decimal.Decimal, error) {
	value, currency, ok, err := values.ValuePerUnit(instrument, anniversary)
	if err != nil {
		return decimal.Zero, fmt.Errorf("engine: ComputeFundTax: %s: looking up the value on %s: %w", instrument, anniversary.Format("2006-01-02"), err)
	}
	if !ok {
		return decimal.Zero, &MissingValuationError{
			Instrument:      instrument,
			LotAcquired:     fl.lot.AcquiredDate,
			AnniversaryDate: anniversary,
			Quantity:        fl.lot.Remaining,
		}
	}

	if currency == "" || currency == "EUR" {
		return value, nil
	}

	rate, err := fx.Rate(currency, anniversary)
	if err != nil {
		return decimal.Zero, fmt.Errorf("engine: ComputeFundTax: %s: converting the %s value on %s to EUR: %w", instrument, currency, anniversary.Format("2006-01-02"), err)
	}
	return value.Mul(rate), nil
}

// chargeFundDisposal matches a sell against lots FIFO and applies the
// credit for deemed-disposal tax already paid on the units it consumes.
// The gain is computed against the units' original cost — see
// FundDisposal.
func chargeFundDisposal(lots []*fundLot, sell ledger.Transaction) (*FundDisposal, error) {
	remaining := sell.Quantity
	costBasis := decimal.Zero
	creditUsed := decimal.Zero

	for _, fl := range lots {
		if remaining.IsZero() {
			break
		}
		if fl.lot.Remaining.IsZero() {
			continue
		}

		take := decimal.Min(remaining, fl.lot.Remaining)
		costBasis = costBasis.Add(take.Mul(fl.lot.unitCost()))
		creditUsed = creditUsed.Add(take.Mul(fl.deemedTaxPerUnit))
		fl.lot.Remaining = fl.lot.Remaining.Sub(take)
		remaining = remaining.Sub(take)
	}

	if !remaining.IsZero() {
		return nil, fmt.Errorf("engine: ComputeFundTax: sell of %s on %s exceeds available lot quantity by %s", sell.Quantity, sell.Date.Format("2006-01-02"), remaining)
	}

	rate, err := taxrules.Lookup(taxrules.KindExitTax, sell.Date)
	if err != nil {
		return nil, fmt.Errorf("engine: ComputeFundTax: disposal on %s: %w", sell.Date.Format("2006-01-02"), err)
	}

	proceeds := sell.Quantity.Mul(sell.Price)
	gain := proceeds.Sub(costBasis)

	taxable := gain
	if taxable.IsNegative() {
		taxable = decimal.Zero
	}
	taxBeforeCredit := taxable.Mul(rate.Rate)

	return &FundDisposal{
		Disposal: Disposal{
			Date:      sell.Date,
			Quantity:  sell.Quantity,
			Proceeds:  proceeds,
			CostBasis: costBasis,
			Gain:      gain,
		},
		TaxBeforeCredit:   taxBeforeCredit,
		CreditUsed:        creditUsed,
		TaxDue:            taxBeforeCredit.Sub(creditUsed),
		RuleEffectiveFrom: rate.EffectiveFrom,
		RuleRate:          rate.Rate,
	}, nil
}

// summarizeFundTax totals a result's events. Charges and repayments
// are accumulated separately — an event that repays tax does not
// reduce the charge on an unrelated one, it is its own line — and each
// total is rounded down to whole euro once, at the end.
func summarizeFundTax(result *FundTaxResult) {
	totalGain := decimal.Zero
	taxableGain := decimal.Zero
	taxBeforeCredit := decimal.Zero
	creditUsed := decimal.Zero
	charged := decimal.Zero
	repayable := decimal.Zero

	accumulate := func(gain, before, credit, net decimal.Decimal) {
		totalGain = totalGain.Add(gain)
		if gain.IsPositive() {
			taxableGain = taxableGain.Add(gain)
		}
		taxBeforeCredit = taxBeforeCredit.Add(before)
		creditUsed = creditUsed.Add(credit)
		if net.IsNegative() {
			repayable = repayable.Add(net.Neg())
		} else {
			charged = charged.Add(net)
		}
	}

	for _, d := range result.DeemedDisposals {
		accumulate(d.Gain, d.CumulativeTax, d.CreditUsed, d.TaxDue)
	}
	for _, d := range result.Disposals {
		accumulate(d.Gain, d.TaxBeforeCredit, d.CreditUsed, d.TaxDue)
	}

	result.TotalGain = totalGain
	result.TaxableGain = taxableGain
	result.TaxBeforeCredit = taxBeforeCredit
	result.CreditUsed = creditUsed
	result.TaxDue = roundTaxToWholeEuro(charged)
	result.RefundDue = roundTaxToWholeEuro(repayable)
}

// DeemedDisposalDue is one 8-year anniversary of one lot: either one
// already reached (Reached, and so chargeable — it needs a market value
// before the holding can be computed) or the next one still ahead.
type DeemedDisposalDue struct {
	LotAcquired     time.Time
	AnniversaryDate time.Time
	Quantity        decimal.Decimal // units still held when the anniversary falls
	Reached         bool            // AnniversaryDate is on or before asOf
}

// DeemedDisposalSchedule lists, per lot, every 8-year anniversary
// already reached as of asOf plus the one next ahead, with the quantity
// still held at each. It answers the two questions the deemed-disposal
// clock exists to answer — what needs a market value now, and when the
// next charge falls due — without computing any liability, so it works
// on a holding whose anniversary values have not been entered yet.
//
// A lot fully sold before an anniversary never reaches it and is
// omitted: those units had a real disposal instead.
func DeemedDisposalSchedule(txs []ledger.Transaction, asOf time.Time) ([]DeemedDisposalDue, error) {
	events, lots, err := buildFundEvents(txs, asOf)
	if err != nil {
		return nil, err
	}

	var out []DeemedDisposalDue
	for _, ev := range events {
		switch ev.kind {
		case eventDeemedDisposal:
			fl := lots[ev.lot]
			if !fl.lot.Remaining.IsPositive() {
				continue
			}
			out = append(out, DeemedDisposalDue{
				LotAcquired:     fl.lot.AcquiredDate,
				AnniversaryDate: ev.date,
				Quantity:        fl.lot.Remaining,
				Reached:         true,
			})

		case eventSell:
			// Quantities only — no rate lookup, no gain, so this stays
			// usable when no anniversary value is on record.
			remaining := ev.tx.Quantity
			for _, fl := range lots {
				if !remaining.IsPositive() || fl.lot.Remaining.IsZero() {
					continue
				}
				take := decimal.Min(remaining, fl.lot.Remaining)
				fl.lot.Remaining = fl.lot.Remaining.Sub(take)
				remaining = remaining.Sub(take)
			}
			if remaining.IsPositive() {
				return nil, fmt.Errorf("engine: DeemedDisposalSchedule: sell of %s on %s exceeds available lot quantity by %s", ev.tx.Quantity, ev.tx.Date.Format("2006-01-02"), remaining)
			}
		}
	}

	for _, fl := range lots {
		if !fl.lot.Remaining.IsPositive() {
			continue
		}
		out = append(out, DeemedDisposalDue{
			LotAcquired:     fl.lot.AcquiredDate,
			AnniversaryDate: NextDeemedDisposalAnniversary(*fl.lot, asOf),
			Quantity:        fl.lot.Remaining,
			Reached:         false,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].AnniversaryDate.Before(out[j].AnniversaryDate) })
	return out, nil
}
