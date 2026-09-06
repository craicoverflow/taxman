package engine

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// These cover the per-tax-year computations that back
// cmd/taxman/report.go and the dashboard's year selector:
// AggregateCGTYear for CGT (year-level netting, loss relief and the
// single annual exemption across every holding), and
// ComputeExitTaxForYear / ComputeDIRTForYear for the regimes that stay
// per-holding. Each matches a holding's full history for cost basis but
// reports and taxes only the selected calendar year.

// cgtDisposals is a test shorthand: FIFO-match one holding and fail the
// test on error.
func cgtDisposals(t *testing.T, txs []ledger.Transaction) []Disposal {
	t.Helper()
	ds, err := CGTDisposals(txs)
	if err != nil {
		t.Fatalf("CGTDisposals: %v", err)
	}
	return ds
}

func TestAggregateCGTYear_SplitsDisposalsAcrossYears(t *testing.T) {
	// One 2023 lot, disposed in two tranches in different years.
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2023-02-01", 20, 10),    // lot: 20 @ 10, cost 200
		sellTx(t, "AAPL", "2024-06-01", 10, 300),  // 2024: proceeds 3000, cost 100, gain 2900
		sellTx(t, "AAPL", "2025-06-01", 10, 500),  // 2025: proceeds 5000, cost 100, gain 4900
	}
	byHolding := map[string][]Disposal{"AAPL": cgtDisposals(t, txs)}

	y2024, err := AggregateCGTYear(byHolding, 2024)
	if err != nil {
		t.Fatalf("AggregateCGTYear(2024): %v", err)
	}
	if !y2024.NetChargeableGain.Equal(decimal.NewFromInt(2900)) {
		t.Errorf("2024 NetChargeableGain = %s, want 2900", y2024.NetChargeableGain)
	}
	// taxable = 2900 - 1270 exemption = 1630; tax = 1630 * 0.33 = 537.9 -> 537 (rounded down)
	if want := decimal.NewFromInt(1630); !y2024.TaxableGain.Equal(want) {
		t.Errorf("2024 TaxableGain = %s, want %s", y2024.TaxableGain, want)
	}
	if want := decimal.NewFromInt(537); !y2024.TaxDue.Equal(want) {
		t.Errorf("2024 TaxDue = %s, want %s (537.9 rounded down to whole euro)", y2024.TaxDue, want)
	}

	y2025, err := AggregateCGTYear(byHolding, 2025)
	if err != nil {
		t.Fatalf("AggregateCGTYear(2025): %v", err)
	}
	if !y2025.NetChargeableGain.Equal(decimal.NewFromInt(4900)) {
		t.Errorf("2025 NetChargeableGain = %s, want 4900 (cost basis still from the 2023 lot)", y2025.NetChargeableGain)
	}
	// taxable = 4900 - 1270 = 3630; tax = 3630 * 0.33 = 1197.9 -> 1197
	if want := decimal.NewFromInt(1197); !y2025.TaxDue.Equal(want) {
		t.Errorf("2025 TaxDue = %s, want %s", y2025.TaxDue, want)
	}
	if !y2025.LossBroughtForward.IsZero() {
		t.Errorf("2025 LossBroughtForward = %s, want 0 (2024 was a gain year)", y2025.LossBroughtForward)
	}
}

func TestAggregateCGTYear_NoDisposalsInYear_ZeroResult(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2023-02-01", 10, 10),
		sellTx(t, "AAPL", "2024-06-01", 10, 50),
	}
	byHolding := map[string][]Disposal{"AAPL": cgtDisposals(t, txs)}

	result, err := AggregateCGTYear(byHolding, 2023)
	if err != nil {
		t.Fatalf("AggregateCGTYear(2023): %v", err)
	}
	if len(result.Holdings) != 0 {
		t.Errorf("expected no per-holding rows for 2023, got %d", len(result.Holdings))
	}
	if !result.NetChargeableGain.IsZero() || !result.TaxableGain.IsZero() || !result.TaxDue.IsZero() {
		t.Errorf("expected an all-zero result for a year with no disposals, got %+v", result)
	}
}

// Finding #1: the €1,270 annual exemption is personal — one per tax
// year, not one per holding.
func TestAggregateCGTYear_SingleExemptionAcrossHoldings(t *testing.T) {
	aapl := []ledger.Transaction{
		buyTx(t, "AAPL", "2024-01-01", 10, 100),  // cost 1000
		sellTx(t, "AAPL", "2024-06-01", 10, 300), // proceeds 3000, gain 2000
	}
	msft := []ledger.Transaction{
		buyTx(t, "MSFT", "2024-01-01", 10, 100),  // cost 1000
		sellTx(t, "MSFT", "2024-07-01", 10, 400), // proceeds 4000, gain 3000
	}
	byHolding := map[string][]Disposal{
		"AAPL": cgtDisposals(t, aapl),
		"MSFT": cgtDisposals(t, msft),
	}

	result, err := AggregateCGTYear(byHolding, 2024)
	if err != nil {
		t.Fatalf("AggregateCGTYear: %v", err)
	}
	// Net gain 5000, ONE exemption of 1270 -> taxable 3730; tax 3730 * 0.33 = 1230.9 -> 1230.
	// (The old per-holding model applied 1270 twice: taxable 2460, tax 811.)
	if want := decimal.NewFromInt(1270); !result.AnnualExemptionUsed.Equal(want) {
		t.Errorf("AnnualExemptionUsed = %s, want %s (exactly one personal exemption)", result.AnnualExemptionUsed, want)
	}
	if want := decimal.NewFromInt(3730); !result.TaxableGain.Equal(want) {
		t.Errorf("TaxableGain = %s, want %s", result.TaxableGain, want)
	}
	if want := decimal.NewFromInt(1230); !result.TaxDue.Equal(want) {
		t.Errorf("TaxDue = %s, want %s", result.TaxDue, want)
	}
}

// Finding #2: a loss on one holding nets against a gain on another in
// the same year (TCA 1997 s.31).
func TestAggregateCGTYear_LossOnOneHoldingOffsetsGainOnAnother(t *testing.T) {
	winner := []ledger.Transaction{
		buyTx(t, "WIN", "2024-01-01", 10, 100),
		sellTx(t, "WIN", "2024-06-01", 10, 600), // gain 5000
	}
	loser := []ledger.Transaction{
		buyTx(t, "LOSE", "2024-01-01", 10, 300),
		sellTx(t, "LOSE", "2024-06-01", 10, 100), // loss 2000
	}
	byHolding := map[string][]Disposal{
		"WIN":  cgtDisposals(t, winner),
		"LOSE": cgtDisposals(t, loser),
	}

	result, err := AggregateCGTYear(byHolding, 2024)
	if err != nil {
		t.Fatalf("AggregateCGTYear: %v", err)
	}
	// Net 5000 - 2000 = 3000; exemption 1270 -> taxable 1730; tax 570.9 -> 570.
	if want := decimal.NewFromInt(3000); !result.NetChargeableGain.Equal(want) {
		t.Errorf("NetChargeableGain = %s, want %s (5000 gain netted with 2000 loss)", result.NetChargeableGain, want)
	}
	if want := decimal.NewFromInt(1730); !result.TaxableGain.Equal(want) {
		t.Errorf("TaxableGain = %s, want %s", result.TaxableGain, want)
	}
	if want := decimal.NewFromInt(570); !result.TaxDue.Equal(want) {
		t.Errorf("TaxDue = %s, want %s", result.TaxDue, want)
	}
	if !result.LossCarriedForward.IsZero() {
		t.Errorf("LossCarriedForward = %s, want 0 (the loss was fully used in-year)", result.LossCarriedForward)
	}
}

// Finding #2: a net loss for the year carries forward and reduces a
// later year's gain.
func TestAggregateCGTYear_LossCarriedForwardToLaterYear(t *testing.T) {
	// 2024: a net loss of 4000. 2025: a gain of 10000.
	lossHolding := []ledger.Transaction{
		buyTx(t, "OLD", "2023-01-01", 10, 500),
		sellTx(t, "OLD", "2024-03-01", 10, 100), // loss 4000
	}
	gainHolding := []ledger.Transaction{
		buyTx(t, "NEW", "2024-01-01", 10, 100),
		sellTx(t, "NEW", "2025-06-01", 10, 1100), // gain 10000
	}
	byHolding := map[string][]Disposal{
		"OLD": cgtDisposals(t, lossHolding),
		"NEW": cgtDisposals(t, gainHolding),
	}

	y2024, err := AggregateCGTYear(byHolding, 2024)
	if err != nil {
		t.Fatalf("AggregateCGTYear(2024): %v", err)
	}
	if want := decimal.NewFromInt(4000); !y2024.LossCarriedForward.Equal(want) {
		t.Errorf("2024 LossCarriedForward = %s, want %s", y2024.LossCarriedForward, want)
	}
	if !y2024.TaxDue.IsZero() {
		t.Errorf("2024 TaxDue = %s, want 0", y2024.TaxDue)
	}

	y2025, err := AggregateCGTYear(byHolding, 2025)
	if err != nil {
		t.Fatalf("AggregateCGTYear(2025): %v", err)
	}
	if want := decimal.NewFromInt(4000); !y2025.LossBroughtForward.Equal(want) {
		t.Errorf("2025 LossBroughtForward = %s, want %s", y2025.LossBroughtForward, want)
	}
	// s.601(4): use brought-forward losses only down to the exemption.
	// Net gain 10000; usable = 10000 - 1270 = 8730; pool 4000 < 8730 so
	// all 4000 used. after losses 6000; exemption 1270 -> taxable 4730;
	// tax 4730 * 0.33 = 1560.9 -> 1560.
	if want := decimal.NewFromInt(4000); !y2025.LossBroughtForwardUsed.Equal(want) {
		t.Errorf("2025 LossBroughtForwardUsed = %s, want %s", y2025.LossBroughtForwardUsed, want)
	}
	if want := decimal.NewFromInt(4730); !y2025.TaxableGain.Equal(want) {
		t.Errorf("2025 TaxableGain = %s, want %s", y2025.TaxableGain, want)
	}
	if want := decimal.NewFromInt(1560); !y2025.TaxDue.Equal(want) {
		t.Errorf("2025 TaxDue = %s, want %s", y2025.TaxDue, want)
	}
	if !y2025.LossCarriedForward.IsZero() {
		t.Errorf("2025 LossCarriedForward = %s, want 0", y2025.LossCarriedForward)
	}
}

// s.601(4): brought-forward losses must not be used to a point that
// wastes the annual exemption.
func TestAggregateCGTYear_BroughtForwardLossDoesNotWasteExemption(t *testing.T) {
	// 2024: loss of 10000 carried forward. 2025: a small gain of 2000.
	lossHolding := []ledger.Transaction{
		buyTx(t, "OLD", "2023-01-01", 10, 1100),
		sellTx(t, "OLD", "2024-03-01", 10, 100), // loss 10000
	}
	gainHolding := []ledger.Transaction{
		buyTx(t, "NEW", "2024-01-01", 10, 100),
		sellTx(t, "NEW", "2025-06-01", 10, 300), // gain 2000
	}
	byHolding := map[string][]Disposal{
		"OLD": cgtDisposals(t, lossHolding),
		"NEW": cgtDisposals(t, gainHolding),
	}

	y2025, err := AggregateCGTYear(byHolding, 2025)
	if err != nil {
		t.Fatalf("AggregateCGTYear(2025): %v", err)
	}
	// Net gain 2000; usable brought-forward = max(0, 2000 - 1270) = 730.
	// Only 730 of the 10000 pool is used; the exemption still absorbs
	// the remaining 1270. Taxable 0, tax 0. 9270 carries forward.
	if want := decimal.NewFromInt(730); !y2025.LossBroughtForwardUsed.Equal(want) {
		t.Errorf("2025 LossBroughtForwardUsed = %s, want %s (only enough to reach the exemption)", y2025.LossBroughtForwardUsed, want)
	}
	if !y2025.TaxDue.IsZero() {
		t.Errorf("2025 TaxDue = %s, want 0", y2025.TaxDue)
	}
	if want := decimal.NewFromInt(9270); !y2025.LossCarriedForward.Equal(want) {
		t.Errorf("2025 LossCarriedForward = %s, want %s", y2025.LossCarriedForward, want)
	}
}

func TestComputeExitTaxForYear_AppliesTheSelectedYearsRate(t *testing.T) {
	// Straddles the 41%->38% exit-tax transition (2026-01-01).
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2020-01-01", 200, 10),
		sellTx(t, "IE_ETF", "2025-06-15", 100, 20), // gain 1000
		sellTx(t, "IE_ETF", "2026-06-15", 100, 30), // cost 100@10, gain 2000
	}

	y2025, err := ComputeExitTaxForYear(txs, 2025)
	if err != nil {
		t.Fatalf("ComputeExitTaxForYear(2025): %v", err)
	}
	if want := decimal.RequireFromString("410"); !y2025.TaxDue.Equal(want) { // 1000 * 0.41
		t.Errorf("2025 TaxDue = %s, want %s (pre-transition 41%%)", y2025.TaxDue, want)
	}

	y2026, err := ComputeExitTaxForYear(txs, 2026)
	if err != nil {
		t.Fatalf("ComputeExitTaxForYear(2026): %v", err)
	}
	if want := decimal.RequireFromString("760"); !y2026.TaxDue.Equal(want) { // 2000 * 0.38
		t.Errorf("2026 TaxDue = %s, want %s (post-transition 38%%)", y2026.TaxDue, want)
	}
}

func TestComputeDIRTForYear_FiltersCreditsByYear(t *testing.T) {
	txs := []ledger.Transaction{
		interestTx(t, "2024-03-01", 100),
		interestTx(t, "2025-03-01", 200),
		interestTx(t, "2025-09-01", 50),
	}

	y2024, err := ComputeDIRTForYear(txs, 2024)
	if err != nil {
		t.Fatalf("ComputeDIRTForYear(2024): %v", err)
	}
	if !y2024.TotalInterest.Equal(decimal.NewFromInt(100)) {
		t.Errorf("2024 TotalInterest = %s, want 100", y2024.TotalInterest)
	}
	if want := decimal.RequireFromString("33"); !y2024.TaxDue.Equal(want) { // 100 * 0.33
		t.Errorf("2024 TaxDue = %s, want %s", y2024.TaxDue, want)
	}

	y2025, err := ComputeDIRTForYear(txs, 2025)
	if err != nil {
		t.Fatalf("ComputeDIRTForYear(2025): %v", err)
	}
	if !y2025.TotalInterest.Equal(decimal.NewFromInt(250)) {
		t.Errorf("2025 TotalInterest = %s, want 250", y2025.TotalInterest)
	}

	y2023, err := ComputeDIRTForYear(txs, 2023)
	if err != nil {
		t.Fatalf("ComputeDIRTForYear(2023): %v", err)
	}
	if !y2023.TotalInterest.IsZero() || !y2023.TaxDue.IsZero() {
		t.Errorf("expected a zero DIRT result for a year with no credits, got %+v", y2023)
	}
}
