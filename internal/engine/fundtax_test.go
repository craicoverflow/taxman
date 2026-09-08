package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// fakeValuer is a stand-in for internal/valuations.Store, keyed by
// "instrument@YYYY-MM-DD" so a test can state exactly which
// anniversary values are on record and which are missing.
type fakeValuer struct {
	values   map[string]string // key -> value per unit
	currency string            // "" means EUR
	err      error
}

func newValuer(values map[string]string) *fakeValuer {
	return &fakeValuer{values: values}
}

func (f *fakeValuer) ValuePerUnit(instrument string, on time.Time) (decimal.Decimal, string, bool, error) {
	if f.err != nil {
		return decimal.Zero, "", false, f.err
	}
	raw, ok := f.values[instrument+"@"+on.Format("2006-01-02")]
	if !ok {
		return decimal.Zero, "", false, nil
	}
	currency := f.currency
	if currency == "" {
		currency = "EUR"
	}
	return decimal.RequireFromString(raw), currency, true, nil
}

func eur(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	return decimal.RequireFromString(s)
}

func assertDecimal(t *testing.T, label string, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %s, want %s", label, got, want)
	}
}

func TestComputeFundTax_FirstAnniversary_ChargesGainAtAnniversaryValue(t *testing.T) {
	// Buy 100 units @ €10 (cost €1,000) on 2016-03-15. The 8-year
	// anniversary falls on 2024-03-15, when the units are worth €18
	// each (€1,800). Gain = 1800 - 1000 = 800; 2024 rate is 41%, so
	// 800 * 0.41 = €328.
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2016-03-15", 100, 10)}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	if len(result.DeemedDisposals) != 1 {
		t.Fatalf("expected 1 deemed disposal, got %d", len(result.DeemedDisposals))
	}
	d := result.DeemedDisposals[0]
	if !d.AnniversaryDate.Equal(mustDate(t, "2024-03-15")) {
		t.Errorf("AnniversaryDate = %s, want 2024-03-15", d.AnniversaryDate.Format("2006-01-02"))
	}
	assertDecimal(t, "Value", d.Value, eur(t, "1800"))
	assertDecimal(t, "CostBasis", d.CostBasis, eur(t, "1000"))
	assertDecimal(t, "Gain", d.Gain, eur(t, "800"))
	assertDecimal(t, "CreditUsed", d.CreditUsed, decimal.Zero)
	assertDecimal(t, "TaxDue", d.TaxDue, eur(t, "328"))
	assertDecimal(t, "result.TaxDue", result.TaxDue, eur(t, "328"))
	assertDecimal(t, "result.RefundDue", result.RefundDue, decimal.Zero)

	// The charge is not an actual disposal: the units are still held.
	if len(result.Disposals) != 0 {
		t.Errorf("expected no actual disposals, got %d", len(result.Disposals))
	}
}

func TestComputeFundTax_AnniversaryNotYetReached_NoCharge(t *testing.T) {
	// asOf is one day before the anniversary: nothing is chargeable
	// yet, and no valuation is needed.
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2016-03-15", 100, 10)}

	result, err := ComputeFundTax("IE_ETF", txs, newValuer(nil), mustDate(t, "2024-03-14"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}
	if len(result.DeemedDisposals) != 0 {
		t.Fatalf("expected no deemed disposal before the anniversary, got %d", len(result.DeemedDisposals))
	}
	assertDecimal(t, "TaxDue", result.TaxDue, decimal.Zero)
}

func TestComputeFundTax_MissingValuation_BlocksWithLotIdentifyingError(t *testing.T) {
	// The anniversary has been reached but no value is on record. The
	// engine must refuse to compute rather than guess a value or
	// silently skip the event.
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2016-03-15", 100, 10)}

	_, err := ComputeFundTax("IE_ETF", txs, newValuer(nil), mustDate(t, "2024-12-31"))
	if err == nil {
		t.Fatal("expected a MissingValuationError, got nil")
	}

	var missing *MissingValuationError
	if !errors.As(err, &missing) {
		t.Fatalf("expected *MissingValuationError, got %T: %v", err, err)
	}
	if missing.Instrument != "IE_ETF" {
		t.Errorf("Instrument = %q, want IE_ETF", missing.Instrument)
	}
	if !missing.AnniversaryDate.Equal(mustDate(t, "2024-03-15")) {
		t.Errorf("AnniversaryDate = %s, want 2024-03-15", missing.AnniversaryDate.Format("2006-01-02"))
	}
	if !missing.LotAcquired.Equal(mustDate(t, "2016-03-15")) {
		t.Errorf("LotAcquired = %s, want 2016-03-15", missing.LotAcquired.Format("2006-01-02"))
	}
	assertDecimal(t, "Quantity", missing.Quantity, eur(t, "100"))
}

func TestComputeFundTax_ActualDisposalAfterDeemed_CreditsTaxAlreadyPaid(t *testing.T) {
	// Buy 100 @ €10 in 2016. Deemed disposal 2024-03-15 at €18 charges
	// 800 * 0.41 = €328. Sell all 100 @ €20 later that year: the gain
	// is measured against the ORIGINAL €1,000 cost (not a €1,800
	// reacquisition uplift), so 2000 - 1000 = 1000, tax 1000 * 0.41 =
	// €410, less the €328 already paid = €82 to pay now.
	//
	// Total tax across both events = 328 + 82 = 410, exactly the tax
	// on the actual disposal. TDM Part 27-04-01 §4.1.4.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2024-09-01", 100, 20),
	}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	if len(result.DeemedDisposals) != 1 || len(result.Disposals) != 1 {
		t.Fatalf("expected 1 deemed + 1 actual disposal, got %d + %d", len(result.DeemedDisposals), len(result.Disposals))
	}

	sale := result.Disposals[0]
	assertDecimal(t, "sale.CostBasis", sale.CostBasis, eur(t, "1000"))
	assertDecimal(t, "sale.Gain", sale.Gain, eur(t, "1000"))
	assertDecimal(t, "sale.TaxBeforeCredit", sale.TaxBeforeCredit, eur(t, "410"))
	assertDecimal(t, "sale.CreditUsed", sale.CreditUsed, eur(t, "328"))
	assertDecimal(t, "sale.TaxDue", sale.TaxDue, eur(t, "82"))

	// 328 charged at the anniversary + 82 on the sale.
	assertDecimal(t, "result.TaxDue", result.TaxDue, eur(t, "410"))
	assertDecimal(t, "result.RefundDue", result.RefundDue, decimal.Zero)
}

func TestComputeFundTax_ValueFallsBeforeSale_ExcessIsRepayable(t *testing.T) {
	// Same start: deemed disposal at €18 charges €328. But the units
	// are then sold at €12, so the real gain is only 1200 - 1000 =
	// 200, taxed at 41% = €82. The €328 already paid exceeds that, and
	// the €246 excess is repayable — s.747E(3)(b) / TDM Part 27-01A-02
	// §4.4.5. Total tax must not exceed the €82 due on the actual
	// disposal.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2024-09-01", 100, 12),
	}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	sale := result.Disposals[0]
	assertDecimal(t, "sale.TaxBeforeCredit", sale.TaxBeforeCredit, eur(t, "82"))
	assertDecimal(t, "sale.CreditUsed", sale.CreditUsed, eur(t, "328"))
	assertDecimal(t, "sale.TaxDue", sale.TaxDue, eur(t, "-246"))

	assertDecimal(t, "result.TaxDue", result.TaxDue, eur(t, "328"))
	assertDecimal(t, "result.RefundDue", result.RefundDue, eur(t, "246"))

	// Net of the repayment, the taxpayer has borne exactly the tax on
	// the actual disposal.
	assertDecimal(t, "net", result.TaxDue.Sub(result.RefundDue), eur(t, "82"))
}

func TestComputeFundTax_SoldBelowCostAfterDeemed_WholeDeemedChargeRepayable(t *testing.T) {
	// Sold at €8, below the €10 cost: no gain arises on the actual
	// disposal, so the whole €328 deemed charge comes back. "Where a
	// gain is treated as nil and tax was chargeable in respect of an
	// earlier deemed disposal..., the overpaid amount is
	// refundable/available for set-off" — Notes for Guidance s.747E.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2024-09-01", 100, 8),
	}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	sale := result.Disposals[0]
	assertDecimal(t, "sale.Gain", sale.Gain, eur(t, "-200"))
	assertDecimal(t, "sale.TaxBeforeCredit", sale.TaxBeforeCredit, decimal.Zero)
	assertDecimal(t, "sale.TaxDue", sale.TaxDue, eur(t, "-328"))
	assertDecimal(t, "result.RefundDue", result.RefundDue, eur(t, "328"))

	// The loss itself gets no relief: it does not become a deduction
	// anywhere (s.747E(3) & (4)).
	assertDecimal(t, "result.TaxableGain", result.TaxableGain, eur(t, "800"))
}

func TestComputeFundTax_SecondAnniversary_UsesOriginalCostAndCreditsTheFirst(t *testing.T) {
	// Buy 100 @ €10 in 2008. First anniversary 2016-03-15 at €18:
	// gain 800, 2016 rate 41%, tax €328. Second anniversary
	// 2024-03-15 at €25: the gain is measured from the ORIGINAL cost
	// again (1500, not 700 — s.739D(2A) disregards the previous 8-year
	// event), cumulative tax 1500 * 0.41 = €615, less the €328 already
	// paid = €287 now.
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2008-03-15", 100, 10)}
	values := newValuer(map[string]string{
		"IE_ETF@2016-03-15": "18",
		"IE_ETF@2024-03-15": "25",
	})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	if len(result.DeemedDisposals) != 2 {
		t.Fatalf("expected 2 deemed disposals, got %d", len(result.DeemedDisposals))
	}

	second := result.DeemedDisposals[1]
	assertDecimal(t, "second.CostBasis", second.CostBasis, eur(t, "1000"))
	assertDecimal(t, "second.Gain", second.Gain, eur(t, "1500"))
	assertDecimal(t, "second.CumulativeTax", second.CumulativeTax, eur(t, "615"))
	assertDecimal(t, "second.CreditUsed", second.CreditUsed, eur(t, "328"))
	assertDecimal(t, "second.TaxDue", second.TaxDue, eur(t, "287"))

	assertDecimal(t, "result.TaxDue", result.TaxDue, eur(t, "615"))
}

func TestComputeFundTax_SecondAnniversaryLower_RepaysTheDifference(t *testing.T) {
	// First anniversary at €18 charges €328; by the second the units
	// are worth €12, so cumulative tax is only 200 * 0.41 = €82 and
	// €246 of the earlier charge is repayable.
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2008-03-15", 100, 10)}
	values := newValuer(map[string]string{
		"IE_ETF@2016-03-15": "18",
		"IE_ETF@2024-03-15": "12",
	})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	second := result.DeemedDisposals[1]
	assertDecimal(t, "second.CumulativeTax", second.CumulativeTax, eur(t, "82"))
	assertDecimal(t, "second.TaxDue", second.TaxDue, eur(t, "-246"))
	assertDecimal(t, "result.TaxDue", result.TaxDue, eur(t, "328"))
	assertDecimal(t, "result.RefundDue", result.RefundDue, eur(t, "246"))
}

func TestComputeFundTax_LotSoldBeforeAnniversary_NoDeemedDisposal(t *testing.T) {
	// The units were really disposed of in 2020, four years before the
	// anniversary would have fallen. Nothing remains to deem disposed,
	// and no valuation is needed for 2024.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2020-01-01", 100, 15),
	}

	result, err := ComputeFundTax("IE_ETF", txs, newValuer(nil), mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}
	if len(result.DeemedDisposals) != 0 {
		t.Fatalf("expected no deemed disposal for a fully sold lot, got %d", len(result.DeemedDisposals))
	}
	assertDecimal(t, "result.TaxDue", result.TaxDue, eur(t, "205")) // 500 gain * 0.41
}

func TestComputeFundTax_PartialSaleBeforeAnniversary_ChargesOnlyTheUnitsStillHeld(t *testing.T) {
	// 100 bought, 40 sold in 2020, so only the remaining 60 units
	// reach the 2024 anniversary. 60 * (18 - 10) = 480 gain,
	// 480 * 0.41 = €196.80.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2020-01-01", 40, 15),
	}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	d := result.DeemedDisposals[0]
	assertDecimal(t, "Quantity", d.Quantity, eur(t, "60"))
	assertDecimal(t, "Gain", d.Gain, eur(t, "480"))
	assertDecimal(t, "TaxDue", d.TaxDue, eur(t, "196.8"))
}

func TestComputeFundTax_PartialSaleAfterAnniversary_TakesItsShareOfTheCredit(t *testing.T) {
	// The full 100 units are charged at the 2024 anniversary (€328).
	// Selling 40 of them afterwards carries 40/100 of that credit —
	// €131.20 — against the €164 tax on its own gain (40 * (20-10) =
	// 400 * 0.41), leaving €32.80. The other 60 units keep their share
	// of the credit for a later event.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2024-09-01", 40, 20),
	}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	sale := result.Disposals[0]
	assertDecimal(t, "sale.TaxBeforeCredit", sale.TaxBeforeCredit, eur(t, "164"))
	assertDecimal(t, "sale.CreditUsed", sale.CreditUsed, eur(t, "131.2"))
	assertDecimal(t, "sale.TaxDue", sale.TaxDue, eur(t, "32.8"))
}

func TestComputeFundTax_AnniversaryInSameYearAsRateChange_UsesTheRateOnTheDate(t *testing.T) {
	// The anniversary falls on 2026-03-15, after Finance Act 2025 cut
	// the rate to 38%: 800 * 0.38 = €304, not 41%'s €328.
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2018-03-15", 100, 10)}
	values := newValuer(map[string]string{"IE_ETF@2026-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2026-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	d := result.DeemedDisposals[0]
	assertDecimal(t, "RuleRate", d.RuleRate, eur(t, "0.38"))
	assertDecimal(t, "TaxDue", d.TaxDue, eur(t, "304"))
}

func TestComputeFundTax_NoAnniversaryReached_MatchesComputeExitTax(t *testing.T) {
	// ComputeFundTax is a strict superset: where no lot has reached an
	// anniversary it must produce exactly what ComputeExitTax does.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2024-01-01", 100, 10),
		sellTx(t, "IE_ETF", "2024-06-01", 100, 15),
	}

	fund, err := ComputeFundTax("IE_ETF", txs, newValuer(nil), mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}
	exit, err := ComputeExitTax(txs)
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}

	assertDecimal(t, "TaxDue", fund.TaxDue, exit.TaxDue)
	assertDecimal(t, "TotalGain", fund.TotalGain, exit.TotalGain)
	assertDecimal(t, "TaxableGain", fund.TaxableGain, exit.TaxableGain)
	if len(fund.Disposals) != len(exit.Disposals) {
		t.Fatalf("disposal count %d != %d", len(fund.Disposals), len(exit.Disposals))
	}
}

func TestComputeFundTax_NoLossReliefBetweenEvents(t *testing.T) {
	// Two lots: one gains at its anniversary, the other loses. Exit
	// tax gives no loss relief per chargeable event, so the losing
	// anniversary contributes zero rather than reducing the other's
	// charge (s.747E(3) & (4)).
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		buyTx(t, "IE_ETF", "2016-06-15", 100, 30),
	}
	values := newValuer(map[string]string{
		"IE_ETF@2024-03-15": "18", // +800
		"IE_ETF@2024-06-15": "20", // -1000
	})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	assertDecimal(t, "TotalGain", result.TotalGain, eur(t, "-200"))
	assertDecimal(t, "TaxableGain", result.TaxableGain, eur(t, "800"))
	assertDecimal(t, "TaxDue", result.TaxDue, eur(t, "328"))
	assertDecimal(t, "RefundDue", result.RefundDue, decimal.Zero)
}

func TestComputeFundTax_RoundsChargeDownToWholeEuro(t *testing.T) {
	// 100 * (17.51 - 10) = 751 gain; 751 * 0.41 = 307.91 -> €307.
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2016-03-15", 100, 10)}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "17.51"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	assertDecimal(t, "DeemedDisposals[0].TaxDue", result.DeemedDisposals[0].TaxDue, eur(t, "307.91"))
	assertDecimal(t, "result.TaxDue", result.TaxDue, eur(t, "307"))
}

func TestComputeFundTax_NonEURValuation_RestatedAtAnniversaryDateRate(t *testing.T) {
	// A USD anniversary value is restated in euro at that date's ECB
	// reference rate, the same treatment every other amount gets.
	txs := []ledger.Transaction{buyTx(t, "US_ETF", "2016-03-15", 100, 10)}
	values := newValuer(map[string]string{"US_ETF@2024-03-15": "18"})
	values.currency = "USD"

	result, err := ComputeFundTax("US_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	d := result.DeemedDisposals[0]
	if d.ValuePerUnit.Equal(eur(t, "18")) {
		t.Error("expected the USD value to be converted to EUR, got it unchanged")
	}
	if !d.ValuePerUnit.IsPositive() {
		t.Errorf("ValuePerUnit = %s, want a positive EUR figure", d.ValuePerUnit)
	}
}

func TestComputeFundTaxForYear_ScopesEventsButKeepsEarlierCredits(t *testing.T) {
	// The deemed disposal falls in 2024 and the sale in 2025. Asked
	// for 2025, the result must show only the sale — but still net off
	// the credit for the 2024 charge, which happened outside the year.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2025-09-01", 100, 20),
	}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	y2025, err := ComputeFundTaxForYear("IE_ETF", txs, values, 2025)
	if err != nil {
		t.Fatalf("ComputeFundTaxForYear: %v", err)
	}
	if len(y2025.DeemedDisposals) != 0 {
		t.Errorf("expected the 2024 deemed disposal to be out of scope for 2025, got %d", len(y2025.DeemedDisposals))
	}
	if len(y2025.Disposals) != 1 {
		t.Fatalf("expected 1 disposal in 2025, got %d", len(y2025.Disposals))
	}
	assertDecimal(t, "2025 CreditUsed", y2025.Disposals[0].CreditUsed, eur(t, "328"))
	assertDecimal(t, "2025 TaxDue", y2025.TaxDue, eur(t, "82"))

	y2024, err := ComputeFundTaxForYear("IE_ETF", txs, values, 2024)
	if err != nil {
		t.Fatalf("ComputeFundTaxForYear: %v", err)
	}
	if len(y2024.DeemedDisposals) != 1 {
		t.Fatalf("expected the deemed disposal in 2024, got %d", len(y2024.DeemedDisposals))
	}
	assertDecimal(t, "2024 TaxDue", y2024.TaxDue, eur(t, "328"))
}

func TestComputeFundTax_SaleOnTheAnniversaryItself_DeemedDisposalComesFirst(t *testing.T) {
	// s.747E(6) places the deemed disposal immediately before the
	// ending of the 8-year period, so a same-day sale is the later
	// event and takes the credit rather than pre-empting the charge.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2024-03-15", 100, 20),
	}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}
	if len(result.DeemedDisposals) != 1 {
		t.Fatalf("expected the deemed disposal to be charged before the same-day sale, got %d", len(result.DeemedDisposals))
	}
	assertDecimal(t, "sale.CreditUsed", result.Disposals[0].CreditUsed, eur(t, "328"))
	assertDecimal(t, "result.TaxDue", result.TaxDue, eur(t, "410"))
}

func TestComputeFundTax_RequiresAValuer(t *testing.T) {
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2016-03-15", 100, 10)}
	if _, err := ComputeFundTax("IE_ETF", txs, nil, mustDate(t, "2024-12-31")); err == nil {
		t.Fatal("expected an error when no Valuer is supplied")
	}
}

func TestFundTaxResult_AuditRecords_OnePerChargeableEventAtItsOwnRate(t *testing.T) {
	// The anniversary falls in 2025 (41%) and the sale in 2026 (38%),
	// either side of the Finance Act 2025 rate cut. Each record must
	// carry the rate that actually applied to its own event.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2017-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2026-06-01", 100, 20),
	}
	values := newValuer(map[string]string{"IE_ETF@2025-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2026-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	records := result.AuditRecords()
	if len(records) != 2 {
		t.Fatalf("expected 1 record per chargeable event (2), got %d", len(records))
	}

	for _, r := range records {
		if r.Instrument != "IE_ETF" {
			t.Errorf("Instrument = %q, want IE_ETF", r.Instrument)
		}
		if r.RuleEffectiveFrom.IsZero() || r.RuleRate.IsZero() {
			t.Errorf("record for %s has no rule version attached", r.DisposalDate.Format("2006-01-02"))
		}
	}

	assertDecimal(t, "deemed record rate", records[0].RuleRate, eur(t, "0.41"))
	assertDecimal(t, "actual record rate", records[1].RuleRate, eur(t, "0.38"))

	// The records reconcile to the holding's net liability.
	total := decimal.Zero
	for _, r := range records {
		total = total.Add(r.LiabilityAmount)
	}
	assertDecimal(t, "records total", total, result.TotalLiability())
}

func TestFundTaxResult_AuditRecords_RepaymentIsRecordedNotBlanked(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2024-09-01", 100, 8),
	}
	values := newValuer(map[string]string{"IE_ETF@2024-03-15": "18"})

	result, err := ComputeFundTax("IE_ETF", txs, values, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("ComputeFundTax: %v", err)
	}

	records := result.AuditRecords()
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	assertDecimal(t, "repayment record", records[1].LiabilityAmount, eur(t, "-328"))
	assertDecimal(t, "net liability", result.TotalLiability(), decimal.Zero)
}

func TestDeemedDisposalSchedule_ListsReachedAndUpcoming(t *testing.T) {
	// Lot A (2016) has reached its 2024 anniversary; lot B (2022) has
	// not — its first falls in 2030. Both should appear, flagged.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		buyTx(t, "IE_ETF", "2022-06-01", 50, 20),
	}

	due, err := DeemedDisposalSchedule(txs, mustDate(t, "2026-09-07"))
	if err != nil {
		t.Fatalf("DeemedDisposalSchedule: %v", err)
	}
	if len(due) != 3 {
		t.Fatalf("expected 3 entries (2024 reached, 2032 upcoming for lot A, 2030 upcoming for lot B), got %d", len(due))
	}

	if !due[0].Reached || !due[0].AnniversaryDate.Equal(mustDate(t, "2024-03-15")) {
		t.Errorf("first entry = %s (reached %v), want 2024-03-15 reached", due[0].AnniversaryDate.Format("2006-01-02"), due[0].Reached)
	}
	assertDecimal(t, "reached quantity", due[0].Quantity, eur(t, "100"))

	// Ordered by date: 2030 (lot B) before 2032 (lot A's next).
	if !due[1].AnniversaryDate.Equal(mustDate(t, "2030-06-01")) || due[1].Reached {
		t.Errorf("second entry = %s (reached %v), want 2030-06-01 upcoming", due[1].AnniversaryDate.Format("2006-01-02"), due[1].Reached)
	}
	if !due[2].AnniversaryDate.Equal(mustDate(t, "2032-03-15")) || due[2].Reached {
		t.Errorf("third entry = %s (reached %v), want 2032-03-15 upcoming", due[2].AnniversaryDate.Format("2006-01-02"), due[2].Reached)
	}
}

func TestDeemedDisposalSchedule_NeedsNoValuations(t *testing.T) {
	// The schedule is what tells the user which values to go and find,
	// so it must work before any value exists.
	txs := []ledger.Transaction{buyTx(t, "IE_ETF", "2016-03-15", 100, 10)}

	due, err := DeemedDisposalSchedule(txs, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("DeemedDisposalSchedule: %v", err)
	}
	if len(due) == 0 || !due[0].Reached {
		t.Fatal("expected the reached 2024 anniversary to be listed with no valuation on record")
	}
}

func TestDeemedDisposalSchedule_SoldLotDropsOut(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2020-01-01", 100, 15),
	}

	due, err := DeemedDisposalSchedule(txs, mustDate(t, "2026-09-07"))
	if err != nil {
		t.Fatalf("DeemedDisposalSchedule: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("expected a fully sold holding to have no anniversaries, got %d", len(due))
	}
}

func TestDeemedDisposalSchedule_PartialSaleReducesTheQuantityAtTheAnniversary(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2016-03-15", 100, 10),
		sellTx(t, "IE_ETF", "2020-01-01", 40, 15),
	}

	due, err := DeemedDisposalSchedule(txs, mustDate(t, "2024-12-31"))
	if err != nil {
		t.Fatalf("DeemedDisposalSchedule: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected the reached anniversary plus the next one, got %d", len(due))
	}
	assertDecimal(t, "quantity at the 2024 anniversary", due[0].Quantity, eur(t, "60"))
}
