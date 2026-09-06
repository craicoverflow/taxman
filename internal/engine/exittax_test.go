package engine

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func TestComputeExitTax_SimpleFIFO_ActualDisposal(t *testing.T) {
	// Buy 100 units @ €10 (cost 1000), sell all 100 @ €15 (proceeds
	// 1500) in 2024 - before the 2026-01-01 rate transition, so 41%.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2024-01-01", 100, 10),
		sellTx(t, "IE_ETF", "2024-06-01", 100, 15),
	}

	result, err := ComputeExitTax(txs)
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	if len(result.Disposals) != 1 {
		t.Fatalf("expected 1 disposal, got %d", len(result.Disposals))
	}

	d := result.Disposals[0]
	if !d.Gain.Equal(decimal.NewFromInt(500)) {
		t.Errorf("Gain = %s, want 500", d.Gain)
	}

	// No annual exemption for exit tax: full 500 gain is taxable.
	if !result.TaxableGain.Equal(decimal.NewFromInt(500)) {
		t.Errorf("TaxableGain = %s, want 500 (no annual exemption applies to exit tax)", result.TaxableGain)
	}
	// 500 * 0.41 = 205
	wantTax := decimal.RequireFromString("205")
	if !result.TaxDue.Equal(wantTax) {
		t.Errorf("TaxDue = %s, want %s", result.TaxDue, wantTax)
	}
}

func TestComputeExitTax_MultiLotFIFO(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2023-01-01", 50, 10),  // lot 1: 50 @ 10 = 500
		buyTx(t, "IE_ETF", "2023-06-01", 50, 12),  // lot 2: 50 @ 12 = 600
		sellTx(t, "IE_ETF", "2024-01-01", 70, 20), // consumes all lot 1, 20 of lot 2
	}

	result, err := ComputeExitTax(txs)
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	d := result.Disposals[0]
	// FIFO: 50@10=500 + 20@12=240 = 740
	wantCost := decimal.NewFromInt(740)
	if !d.CostBasis.Equal(wantCost) {
		t.Errorf("CostBasis = %s, want %s", d.CostBasis, wantCost)
	}
}

func TestComputeExitTax_Loss_NotOffsetAgainstGains(t *testing.T) {
	// Fund A: a loss. Fund B: a gain. Exit tax disallows loss relief -
	// a loss on one holding cannot reduce taxable gain overall.
	// ComputeExitTax operates on one holding's transactions at a time
	// (same contract as ComputeCGT/ComputeDIRT), so this test confirms
	// a holding-level loss floors at zero rather than going negative
	// and being available to net against anything else.
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF_LOSS", "2024-01-01", 100, 20),
		sellTx(t, "IE_ETF_LOSS", "2024-06-01", 100, 15), // loss of 500
	}

	result, err := ComputeExitTax(txs)
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	if !result.Disposals[0].Gain.Equal(decimal.NewFromInt(-500)) {
		t.Errorf("Gain = %s, want -500 (Gain itself may be negative; TaxableGain must not be)", result.Disposals[0].Gain)
	}
	if !result.TaxableGain.IsZero() {
		t.Errorf("TaxableGain = %s, want 0 (a loss must floor at zero, not offset anything or go negative)", result.TaxableGain)
	}
	if !result.TaxDue.IsZero() {
		t.Errorf("TaxDue = %s, want 0", result.TaxDue)
	}
}

// Finding #3: exit tax has no loss relief PER CHARGEABLE EVENT. A
// losing disposal of a fund must not net against a gaining disposal of
// the SAME fund in the same year.
func TestComputeExitTax_LosingDisposalDoesNotNetAgainstGainingDisposal(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2020-01-01", 200, 10), // one lot, 200 @ 10
		sellTx(t, "IE_ETF", "2024-03-01", 100, 20), // disposal 1: gain 1000
		sellTx(t, "IE_ETF", "2024-09-01", 100, 3),  // disposal 2: loss 700
	}

	result, err := ComputeExitTax(txs)
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	if len(result.Disposals) != 2 {
		t.Fatalf("expected 2 disposals, got %d", len(result.Disposals))
	}
	// TotalGain is the raw sum for transparency: 1000 + (-700) = 300.
	if !result.TotalGain.Equal(decimal.NewFromInt(300)) {
		t.Errorf("TotalGain = %s, want 300 (raw arithmetic sum)", result.TotalGain)
	}
	// TaxableGain counts only the positive disposal: 1000, NOT 300.
	if !result.TaxableGain.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("TaxableGain = %s, want 1000 (the loss is ignored, not netted)", result.TaxableGain)
	}
	// 1000 * 0.41 = 410.
	if want := decimal.NewFromInt(410); !result.TaxDue.Equal(want) {
		t.Errorf("TaxDue = %s, want %s", result.TaxDue, want)
	}
}

func TestComputeExitTax_NonEURCurrency_ConvertsAtTransactionDateECBRate(t *testing.T) {
	// Same ECB rates as the CGT case: USD 1.2152/EUR on 2021-01-25,
	// 1.0956/EUR on 2024-01-02.
	buy := buyTx(t, "IE_ETF", "2021-01-25", 10, 100)
	buy.Currency = "USD"
	sell := sellTx(t, "IE_ETF", "2024-01-02", 10, 150)
	sell.Currency = "USD"

	result, err := ComputeExitTax([]ledger.Transaction{buy, sell})
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	if len(result.Disposals) != 1 {
		t.Fatalf("expected 1 disposal, got %d", len(result.Disposals))
	}
	d := result.Disposals[0]

	eurPerUSD := func(ecb string) decimal.Decimal {
		return decimal.NewFromInt(1).Div(decimal.RequireFromString(ecb))
	}
	wantCost := decimal.NewFromInt(1000).Mul(eurPerUSD("1.2152"))
	wantProceeds := decimal.NewFromInt(1500).Mul(eurPerUSD("1.0956"))
	if !d.CostBasis.Equal(wantCost) {
		t.Errorf("CostBasis = %s, want %s", d.CostBasis, wantCost)
	}
	if !d.Proceeds.Equal(wantProceeds) {
		t.Errorf("Proceeds = %s, want %s", d.Proceeds, wantProceeds)
	}
}
