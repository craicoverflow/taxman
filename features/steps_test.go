package features

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/engine"
	"github.com/craicoverflow/taxman/internal/fx"
	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/taxrules"
)

// InitializeScenario wires the step vocabulary. The phrasing is meant to
// read as plain English in the .feature files; each step maps to a
// direct call into internal/engine, internal/taxrules or internal/fx.
func InitializeScenario(ctx *godog.ScenarioContext) {
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		line := 0
		if sc.Location != nil {
			line = int(sc.Location.Line)
		}
		return context.WithValue(c, worldKey{}, newWorld(sc.Uri, line)), nil
	})

	// --- Given: build the ledger -----------------------------------
	ctx.Step(`^an? (CGT_ASSET|EXIT_TAX_FUND|UNCLASSIFIED) holding "([^"]+)"$`, stepHolding)
	ctx.Step(`^a buy of ([\d.]+) "([^"]+)" at €([\d.]+) on (\d{4}-\d{2}-\d{2})$`, stepBuyEUR)
	ctx.Step(`^a buy of ([\d.]+) "([^"]+)" at ([\d.]+) ([A-Z]{3}) on (\d{4}-\d{2}-\d{2})$`, stepBuyCcy)
	ctx.Step(`^a sale of ([\d.]+) "([^"]+)" at €([\d.]+) on (\d{4}-\d{2}-\d{2})$`, stepSellEUR)
	ctx.Step(`^a sale of ([\d.]+) "([^"]+)" at ([\d.]+) ([A-Z]{3}) on (\d{4}-\d{2}-\d{2})$`, stepSellCcy)
	ctx.Step(`^an RSU vest of ([\d.]+) "([^"]+)" at €([\d.]+) on (\d{4}-\d{2}-\d{2})$`, stepVestEUR)
	ctx.Step(`^an RSU vest of ([\d.]+) "([^"]+)" at ([\d.]+) ([A-Z]{3}) on (\d{4}-\d{2}-\d{2})$`, stepVestCcy)
	ctx.Step(`^an interest credit of €([\d.]+) on (\d{4}-\d{2}-\d{2})$`, stepInterest)

	// --- When: run a computation ---------------------------------
	ctx.Step(`^CGT is computed for the holding$`, stepComputeCGT)
	ctx.Step(`^CGT is aggregated for the (\d{4}) tax year$`, stepAggregateCGTYear)
	ctx.Step(`^exit tax is computed for the holding$`, stepComputeExitTax)
	ctx.Step(`^exit tax is computed for the (\d{4}) tax year$`, stepComputeExitTaxYear)
	ctx.Step(`^DIRT is computed$`, stepComputeDIRT)
	ctx.Step(`^DIRT is computed for the (\d{4}) tax year$`, stepComputeDIRTYear)
	ctx.Step(`^the lots are matched FIFO$`, stepMatchFIFO)
	ctx.Step(`^the ECB reference rate for ([A-Z]{3}) on (\d{4}-\d{2}-\d{2}) is looked up$`, stepLookupFX)
	ctx.Step(`^the (CGT|exit tax|DIRT) rate on (\d{4}-\d{2}-\d{2}) is looked up$`, stepLookupRate)
	ctx.Step(`^the next deemed-disposal date is computed for a lot acquired on (\d{4}-\d{2}-\d{2}), as of (\d{4}-\d{2}-\d{2})$`, stepDeemedDate)
	ctx.Step(`^tax is computed with the holding left unclassified$`, stepComputeUnclassified)

	// --- Then: check the pinned figure ---------------------------
	ctx.Step(`^the total gain is (\S+)$`, stepThenTotalGain)
	ctx.Step(`^the net chargeable gain is (\S+)$`, stepThenNetChargeableGain)
	ctx.Step(`^the loss brought forward is (\S+)$`, stepThenLossBroughtForward)
	ctx.Step(`^the loss brought forward used is (\S+)$`, stepThenLossBroughtForwardUsed)
	ctx.Step(`^the loss carried forward is (\S+)$`, stepThenLossCarriedForward)
	ctx.Step(`^the annual exemption applied is (\S+)$`, stepThenAnnualExemptionApplied)
	ctx.Step(`^the taxable gain is (\S+)$`, stepThenTaxableGain)
	ctx.Step(`^holding "([^"]+)" has a realised gain of (\S+)$`, stepThenHoldingRealisedGain)
	ctx.Step(`^the CGT due is (\S+)$`, stepThenCGTDue)
	ctx.Step(`^the exit tax due is (\S+)$`, stepThenExitTaxDue)
	ctx.Step(`^the total interest is (\S+)$`, stepThenTotalInterest)
	ctx.Step(`^the DIRT due is (\S+)$`, stepThenDIRTDue)
	ctx.Step(`^disposal (\d+) has proceeds of (\S+)$`, stepThenDisposalProceeds)
	ctx.Step(`^disposal (\d+) has a cost basis of (\S+)$`, stepThenDisposalCostBasis)
	ctx.Step(`^disposal (\d+) has a gain of (\S+)$`, stepThenDisposalGain)
	ctx.Step(`^disposal (\d+) has a quantity of (\S+)$`, stepThenDisposalQuantity)
	ctx.Step(`^the euro value of one ([A-Z]{3}) is (\S+)$`, stepThenFXRate)
	ctx.Step(`^the (CGT|exit tax|DIRT) rate is (\S+)$`, stepThenRatePercent)
	ctx.Step(`^the annual exemption is (\S+)$`, stepThenRateExemption)
	ctx.Step(`^the rule takes effect from (\S+)$`, stepThenRuleEffectiveFrom)
	ctx.Step(`^the (?:lookup|computation|match) is rejected$`, stepThenRejected)
	ctx.Step(`^the next deemed-disposal date is (\S+)$`, stepThenDeemedDate)
	ctx.Step(`^computation is blocked for "([^"]+)"$`, stepThenBlocked)
	ctx.Step(`^the reason given is (\S.*)$`, stepThenBlockReason)
}

// ---- Given -------------------------------------------------------------

var classNames = map[string]classify.Classification{
	"CGT_ASSET":     classify.CGTAsset,
	"EXIT_TAX_FUND": classify.ExitTaxFund,
	"UNCLASSIFIED":  classify.Unclassified,
}

func stepHolding(ctx context.Context, cls, name string) error {
	w := worldFrom(ctx)
	h := w.holdingNamed(name)
	h.classification = classNames[cls]
	return nil
}

func addTx(ctx context.Context, typ ledger.Type, qty, instrument, price, currency, date string) error {
	w := worldFrom(ctx)
	q, err := parseAmount(qty)
	if err != nil {
		return fmt.Errorf("quantity: %w", err)
	}
	p, err := parseAmount(price)
	if err != nil {
		return fmt.Errorf("price: %w", err)
	}
	d, err := parseDate(date)
	if err != nil {
		return fmt.Errorf("date: %w", err)
	}
	platform := ledger.PlatformDegiro
	if typ == ledger.TypeInterest {
		platform = ledger.PlatformManual // hand-entered; no bank is modelled
	}
	h := w.holdingNamed(instrument)
	h.txs = append(h.txs, ledger.Transaction{
		Platform:   platform,
		Type:       typ,
		Date:       d.UTC(),
		Instrument: instrument,
		Quantity:   q,
		Price:      p,
		Currency:   currency,
	})
	return nil
}

func stepBuyEUR(ctx context.Context, qty, instr, price, date string) error {
	return addTx(ctx, ledger.TypeBuy, qty, instr, price, "EUR", date)
}
func stepBuyCcy(ctx context.Context, qty, instr, price, ccy, date string) error {
	return addTx(ctx, ledger.TypeBuy, qty, instr, price, ccy, date)
}
func stepSellEUR(ctx context.Context, qty, instr, price, date string) error {
	return addTx(ctx, ledger.TypeSell, qty, instr, price, "EUR", date)
}
func stepSellCcy(ctx context.Context, qty, instr, price, ccy, date string) error {
	return addTx(ctx, ledger.TypeSell, qty, instr, price, ccy, date)
}
func stepVestEUR(ctx context.Context, qty, instr, price, date string) error {
	return addTx(ctx, ledger.TypeRSUVest, qty, instr, price, "EUR", date)
}
func stepVestCcy(ctx context.Context, qty, instr, price, ccy, date string) error {
	return addTx(ctx, ledger.TypeRSUVest, qty, instr, price, ccy, date)
}
func stepInterest(ctx context.Context, amount, date string) error {
	return addTx(ctx, ledger.TypeInterest, "1", "Savings account", amount, "EUR", date)
}

// ---- When -----------------------------------------------------------

// A When step never fails the scenario on an engine error: it stashes
// the error in w.whenErr and returns nil. A success-path Then step calls
// w.needOK() first (so an unexpected error still surfaces clearly); a
// failure-path scenario asserts with "Then the computation is rejected".

func stepComputeCGT(ctx context.Context) error {
	w := worldFrom(ctx)
	h, err := w.currentHolding()
	if err != nil {
		return err
	}
	w.cgt, w.whenErr = engine.ComputeCGT(h.txs)
	return nil
}

func stepAggregateCGTYear(ctx context.Context, year string) error {
	w := worldFrom(ctx)
	y, err := parseYear(year)
	if err != nil {
		return err
	}
	byHolding := map[string][]engine.Disposal{}
	for name, txs := range w.txnsByInstrument() {
		ds, dErr := engine.CGTDisposals(txs)
		if dErr != nil {
			w.whenErr = fmt.Errorf("FIFO-matching %s: %w", name, dErr)
			return nil
		}
		byHolding[name] = ds
	}
	w.cgtYear, w.whenErr = engine.AggregateCGTYear(byHolding, y)
	return nil
}

func stepComputeExitTax(ctx context.Context) error {
	w := worldFrom(ctx)
	h, err := w.currentHolding()
	if err != nil {
		return err
	}
	w.exitTax, w.whenErr = engine.ComputeExitTax(h.txs)
	return nil
}

func stepComputeExitTaxYear(ctx context.Context, year string) error {
	w := worldFrom(ctx)
	h, err := w.currentHolding()
	if err != nil {
		return err
	}
	y, err := parseYear(year)
	if err != nil {
		return err
	}
	w.exitTax, w.whenErr = engine.ComputeExitTaxForYear(h.txs, y)
	return nil
}

func stepComputeDIRT(ctx context.Context) error {
	w := worldFrom(ctx)
	w.dirt, w.whenErr = engine.ComputeDIRT(w.allTxns())
	return nil
}

func stepComputeDIRTYear(ctx context.Context, year string) error {
	w := worldFrom(ctx)
	y, err := parseYear(year)
	if err != nil {
		return err
	}
	w.dirt, w.whenErr = engine.ComputeDIRTForYear(w.allTxns(), y)
	return nil
}

func stepMatchFIFO(ctx context.Context) error {
	w := worldFrom(ctx)
	h, err := w.currentHolding()
	if err != nil {
		return err
	}
	w.disposals, w.whenErr = engine.CGTDisposals(h.txs)
	return nil
}

func stepLookupFX(ctx context.Context, ccy, date string) error {
	w := worldFrom(ctx)
	d, err := parseDate(date)
	if err != nil {
		return err
	}
	r, lookupErr := fx.Rate(ccy, d)
	w.whenErr = lookupErr
	if lookupErr == nil {
		w.fxRate = &r
	}
	return nil
}

var rateKinds = map[string]taxrules.Kind{
	"CGT":      taxrules.KindCGT,
	"exit tax": taxrules.KindExitTax,
	"DIRT":     taxrules.KindDIRT,
}

func stepLookupRate(ctx context.Context, kind, date string) error {
	w := worldFrom(ctx)
	d, err := parseDate(date)
	if err != nil {
		return err
	}
	r, lookupErr := taxrules.Lookup(rateKinds[kind], d)
	w.whenErr = lookupErr
	if lookupErr == nil {
		w.rate = &r
	}
	return nil
}

func stepDeemedDate(ctx context.Context, acquired, asOf string) error {
	w := worldFrom(ctx)
	acq, err := parseDate(acquired)
	if err != nil {
		return err
	}
	as, err := parseDate(asOf)
	if err != nil {
		return err
	}
	next := engine.NextDeemedDisposalAnniversary(engine.Lot{AcquiredDate: acq.UTC()}, as.UTC())
	w.deemedDate = &next
	return nil
}

func stepComputeUnclassified(ctx context.Context) error {
	w := worldFrom(ctx)
	h, err := w.currentHolding()
	if err != nil {
		return err
	}
	w.blocked, w.whenErr = engine.Compute(h.txs, fixedClassifier{h.name: classify.Unclassified})
	return w.whenErr
}

// ---- Then ----------------------------------------------------------

func stepThenTotalGain(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	switch {
	case w.cgt != nil:
		return assertDecimal(w, "the total gain is "+want, want, w.cgt.TotalGain, formatEUR)
	case w.exitTax != nil:
		return assertDecimal(w, "the total gain is "+want, want, w.exitTax.TotalGain, formatEUR)
	default:
		return fmt.Errorf(`"the total gain is" needs a "When CGT/exit tax is computed for the holding" step first`)
	}
}

func stepThenNetChargeableGain(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.cgtYear == nil {
		return fmt.Errorf(`"the net chargeable gain is" needs a "When CGT is aggregated for the YYYY tax year" step`)
	}
	return assertDecimal(w, "the net chargeable gain is "+want, want, w.cgtYear.NetChargeableGain, formatEUR)
}

func stepThenLossBroughtForward(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.cgtYear == nil {
		return errNeedCGTYear("the loss brought forward is")
	}
	return assertDecimal(w, "the loss brought forward is "+want, want, w.cgtYear.LossBroughtForward, formatEUR)
}

func stepThenLossBroughtForwardUsed(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.cgtYear == nil {
		return errNeedCGTYear("the loss brought forward used is")
	}
	return assertDecimal(w, "the loss brought forward used is "+want, want, w.cgtYear.LossBroughtForwardUsed, formatEUR)
}

func stepThenLossCarriedForward(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.cgtYear == nil {
		return errNeedCGTYear("the loss carried forward is")
	}
	return assertDecimal(w, "the loss carried forward is "+want, want, w.cgtYear.LossCarriedForward, formatEUR)
}

func stepThenAnnualExemptionApplied(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.cgtYear == nil {
		return errNeedCGTYear("the annual exemption applied is")
	}
	return assertDecimal(w, "the annual exemption applied is "+want, want, w.cgtYear.AnnualExemptionUsed, formatEUR)
}

func stepThenTaxableGain(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	stepText := "the taxable gain is " + want
	switch {
	case w.cgtYear != nil:
		return assertDecimal(w, stepText, want, w.cgtYear.TaxableGain, formatEUR)
	case w.cgt != nil:
		return assertDecimal(w, stepText, want, w.cgt.TaxableGain, formatEUR)
	case w.exitTax != nil:
		return assertDecimal(w, stepText, want, w.exitTax.TaxableGain, formatEUR)
	default:
		return fmt.Errorf(`"the taxable gain is" needs a CGT or exit-tax When step first`)
	}
}

func stepThenHoldingRealisedGain(ctx context.Context, instrument, want string) error {
	w := worldFrom(ctx)
	if w.cgtYear == nil {
		return errNeedCGTYear("holding ... has a realised gain of")
	}
	for _, h := range w.cgtYear.Holdings {
		if h.Instrument == instrument {
			return assertDecimal(w, fmt.Sprintf("holding %q has a realised gain of %s", instrument, want), want, h.RealisedGain, formatEUR)
		}
	}
	return fmt.Errorf("no in-year disposals recorded for holding %q", instrument)
}

func stepThenCGTDue(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	stepText := "the CGT due is " + want
	switch {
	case w.cgtYear != nil:
		return assertDecimal(w, stepText, want, w.cgtYear.TaxDue, formatEUR)
	case w.cgt != nil:
		return assertDecimal(w, stepText, want, w.cgt.TaxDue, formatEUR)
	default:
		return fmt.Errorf(`"the CGT due is" needs a CGT When step first`)
	}
}

func stepThenExitTaxDue(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.exitTax == nil {
		return fmt.Errorf(`"the exit tax due is" needs an exit-tax When step first`)
	}
	return assertDecimal(w, "the exit tax due is "+want, want, w.exitTax.TaxDue, formatEUR)
}

func stepThenTotalInterest(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.dirt == nil {
		return errNeedDIRT("the total interest is")
	}
	return assertDecimal(w, "the total interest is "+want, want, w.dirt.TotalInterest, formatEUR)
}

func stepThenDIRTDue(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.dirt == nil {
		return errNeedDIRT("the DIRT due is")
	}
	return assertDecimal(w, "the DIRT due is "+want, want, w.dirt.TaxDue, formatEUR)
}

func (w *world) disposalN(n string) (engine.Disposal, error) {
	if err := w.needOK(); err != nil {
		return engine.Disposal{}, err
	}
	ds := w.disposals
	if ds == nil && w.cgt != nil {
		ds = w.cgt.Disposals
	}
	if ds == nil && w.exitTax != nil {
		ds = w.exitTax.Disposals
	}
	idx, err := parseInt(n)
	if err != nil || idx < 1 || idx > len(ds) {
		return engine.Disposal{}, fmt.Errorf("no disposal %s (there are %d)", n, len(ds))
	}
	return ds[idx-1], nil
}

func stepThenDisposalProceeds(ctx context.Context, n, want string) error {
	w := worldFrom(ctx)
	d, err := w.disposalN(n)
	if err != nil {
		return err
	}
	return assertDecimal(w, fmt.Sprintf("disposal %s has proceeds of %s", n, want), want, d.Proceeds, formatEUR)
}

func stepThenDisposalCostBasis(ctx context.Context, n, want string) error {
	w := worldFrom(ctx)
	d, err := w.disposalN(n)
	if err != nil {
		return err
	}
	return assertDecimal(w, fmt.Sprintf("disposal %s has a cost basis of %s", n, want), want, d.CostBasis, formatEUR)
}

func stepThenDisposalGain(ctx context.Context, n, want string) error {
	w := worldFrom(ctx)
	d, err := w.disposalN(n)
	if err != nil {
		return err
	}
	return assertDecimal(w, fmt.Sprintf("disposal %s has a gain of %s", n, want), want, d.Gain, formatEUR)
}

func stepThenDisposalQuantity(ctx context.Context, n, want string) error {
	w := worldFrom(ctx)
	d, err := w.disposalN(n)
	if err != nil {
		return err
	}
	return assertDecimal(w, fmt.Sprintf("disposal %s has a quantity of %s", n, want), want, d.Quantity, decimalPlain)
}

func decimalPlain(x decimal.Decimal) string { return x.String() }

func stepThenFXRate(ctx context.Context, ccy, want string) error {
	w := worldFrom(ctx)
	if w.fxRate == nil {
		return fmt.Errorf(`"the euro value of one %s is ..." needs a "When the ECB reference rate ... is looked up" step`, ccy)
	}
	return assertDecimal(w, fmt.Sprintf("the euro value of one %s is %s", ccy, want), want, *w.fxRate, formatRate)
}

func stepThenRatePercent(ctx context.Context, kind, want string) error {
	w := worldFrom(ctx)
	stepText := fmt.Sprintf("the %s rate is %s", kind, want)
	switch {
	case w.rate != nil:
		return assertPercent(w, stepText, want, w.rate.Rate)
	case kind == "CGT" && w.cgtYear != nil:
		// The year-level aggregate resolves and reports its own rate
		// (at 31 December of the year); no separate lookup step needed.
		return assertPercent(w, stepText, want, w.cgtYear.Rate)
	default:
		return fmt.Errorf(`"the %s rate is ..." needs a rate-lookup When step, or (for CGT) a "When CGT is aggregated ..." step`, kind)
	}
}

func stepThenRateExemption(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.rate == nil {
		return fmt.Errorf(`"the annual exemption is ..." needs a "When the CGT rate on DATE is looked up" step`)
	}
	return assertDecimal(w, "the annual exemption is "+want, want, w.rate.AnnualExemption, formatEUR)
}

func stepThenRuleEffectiveFrom(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.rate == nil {
		return fmt.Errorf(`"the rule takes effect from ..." needs a "When the ... rate on DATE is looked up" step`)
	}
	return assertString(w, "the rule takes effect from "+want, want, w.rate.EffectiveFrom.Format(dateLayout))
}

func stepThenRejected(ctx context.Context) error {
	w := worldFrom(ctx)
	if regenMode {
		return nil
	}
	if w.whenErr == nil {
		return fmt.Errorf("expected an explicit error, but the computation returned a value")
	}
	return nil
}

func stepThenDeemedDate(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.deemedDate == nil {
		return fmt.Errorf(`"the next deemed-disposal date is ..." needs a "When the next deemed-disposal date is computed ..." step`)
	}
	return assertString(w, "the next deemed-disposal date is "+want, want, w.deemedDate.Format(dateLayout))
}

func stepThenBlocked(ctx context.Context, instrument string) error {
	w := worldFrom(ctx)
	if regenMode {
		return nil
	}
	if w.blocked == nil {
		return fmt.Errorf(`"computation is blocked for ..." needs a "When tax is computed with the holding left unclassified" step`)
	}
	hr, ok := w.blocked.Holdings[instrument]
	if !ok {
		return fmt.Errorf("no result for holding %q", instrument)
	}
	var unclassified *engine.UnclassifiedError
	if !errors.As(hr.Err, &unclassified) {
		return fmt.Errorf("holding %q was not blocked (Err = %v)", instrument, hr.Err)
	}
	return nil
}

func stepThenBlockReason(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if regenMode {
		return nil
	}
	if w.blocked == nil {
		return fmt.Errorf(`"the reason given is ..." needs a "When tax is computed with the holding left unclassified" step`)
	}
	want = trimQuotes(want)
	for _, hr := range w.blocked.Holdings {
		if hr.Err != nil && strings.Contains(hr.Err.Error(), want) {
			return nil
		}
	}
	return fmt.Errorf("no blocked holding's reason contained %q", want)
}

// ---- small shared helpers ----------------------------------------

func parseYear(s string) (int, error) { return parseInt(s) }

func parseInt(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil {
		return 0, fmt.Errorf("not an integer: %q", s)
	}
	return n, nil
}

func errNeedCGTYear(step string) error {
	return fmt.Errorf("%q needs a \"When CGT is aggregated for the YYYY tax year\" step", step)
}

func errNeedDIRT(step string) error {
	return fmt.Errorf("%q needs a \"When DIRT is computed\" step", step)
}

func assertPercent(w *world, stepText, want string, gotFraction decimal.Decimal) error {
	if err := w.needOK(); err != nil {
		return err
	}
	rendered := formatPercent(gotFraction)
	if regenMode {
		recordPinned(w.scenarioURI, w.scenarioLine, stepText, rendered)
		return nil
	}
	if want != rendered {
		return fmt.Errorf("engine resolves the rate to %s, feature file pins %s", rendered, want)
	}
	return nil
}

type fixedClassifier map[string]classify.Classification

func (f fixedClassifier) Classify(instrument string) (classify.Classification, error) {
	if c, ok := f[instrument]; ok {
		return c, nil
	}
	return classify.Unclassified, nil
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
