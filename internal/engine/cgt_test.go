package engine

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func sellTx(t *testing.T, instrument, date string, qty, price int64) ledger.Transaction {
	t.Helper()
	tx := buyTx(t, instrument, date, qty, price)
	tx.Type = ledger.TypeSell
	return tx
}

func TestComputeCGT_SimpleFIFO_SingleLotFullDisposal(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2024-01-01", 10, 100),  // cost basis 1000
		sellTx(t, "AAPL", "2024-06-01", 10, 150), // proceeds 1500
	}

	result, err := ComputeCGT(txs)
	if err != nil {
		t.Fatalf("ComputeCGT: %v", err)
	}

	if len(result.Disposals) != 1 {
		t.Fatalf("expected 1 disposal, got %d", len(result.Disposals))
	}
	d := result.Disposals[0]
	if !d.Proceeds.Equal(decimal.NewFromInt(1500)) {
		t.Errorf("Proceeds = %s, want 1500", d.Proceeds)
	}
	if !d.CostBasis.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("CostBasis = %s, want 1000", d.CostBasis)
	}
	if !d.Gain.Equal(decimal.NewFromInt(500)) {
		t.Errorf("Gain = %s, want 500", d.Gain)
	}

	// 2024: CGT 33%, exemption 1270. Gain 500 < exemption -> no tax.
	wantExemption := decimal.RequireFromString("1270")
	if !result.TotalGain.Equal(decimal.NewFromInt(500)) {
		t.Errorf("TotalGain = %s, want 500", result.TotalGain)
	}
	if !result.TaxableGain.IsZero() {
		t.Errorf("TaxableGain = %s, want 0 (gain below annual exemption %s)", result.TaxableGain, wantExemption)
	}
	if !result.TaxDue.IsZero() {
		t.Errorf("TaxDue = %s, want 0", result.TaxDue)
	}
}

func TestComputeCGT_GainAboveExemption_TaxDueAtCorrectRate(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2024-01-01", 10, 100),  // cost basis 1000
		sellTx(t, "AAPL", "2024-06-01", 10, 500), // proceeds 5000, gain 4000
	}

	result, err := ComputeCGT(txs)
	if err != nil {
		t.Fatalf("ComputeCGT: %v", err)
	}

	// gain 4000 - exemption 1270 = 2730 taxable; tax = 2730 * 0.33 =
	// 900.90, rounded down to whole euro -> 900.
	wantTaxable := decimal.RequireFromString("2730")
	if !result.TaxableGain.Equal(wantTaxable) {
		t.Errorf("TaxableGain = %s, want %s", result.TaxableGain, wantTaxable)
	}
	wantTax := decimal.RequireFromString("900")
	if !result.TaxDue.Equal(wantTax) {
		t.Errorf("TaxDue = %s, want %s (900.90 rounded down)", result.TaxDue, wantTax)
	}
}

func TestComputeCGT_MultiLotFIFO_OrderingAndPartialConsumption(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2023-01-01", 5, 100),  // lot 1: 5 @ 100 = 500
		buyTx(t, "AAPL", "2023-06-01", 5, 120),  // lot 2: 5 @ 120 = 600
		buyTx(t, "AAPL", "2023-09-01", 5, 140),  // lot 3: 5 @ 140 = 700
		sellTx(t, "AAPL", "2024-01-01", 8, 200), // sells all of lot 1, 3 of lot 2
	}

	result, err := ComputeCGT(txs)
	if err != nil {
		t.Fatalf("ComputeCGT: %v", err)
	}
	if len(result.Disposals) != 1 {
		t.Fatalf("expected 1 disposal event, got %d", len(result.Disposals))
	}

	d := result.Disposals[0]
	// FIFO: all 5 of lot 1 (cost 500) + 3 of lot 2 (cost 3*120=360) = 860
	wantCost := decimal.NewFromInt(860)
	if !d.CostBasis.Equal(wantCost) {
		t.Errorf("CostBasis = %s, want %s (FIFO should consume lot 1 fully, then 3 units of lot 2)", d.CostBasis, wantCost)
	}
	wantProceeds := decimal.NewFromInt(1600) // 8 * 200
	if !d.Proceeds.Equal(wantProceeds) {
		t.Errorf("Proceeds = %s, want %s", d.Proceeds, wantProceeds)
	}

	// A second disposal should consume the remainder of lot 2 (2
	// units) then lot 3 - confirms partial-lot state carried forward.
	txs = append(txs, sellTx(t, "AAPL", "2024-02-01", 4, 200))
	result2, err := ComputeCGT(txs)
	if err != nil {
		t.Fatalf("ComputeCGT (second disposal): %v", err)
	}
	if len(result2.Disposals) != 2 {
		t.Fatalf("expected 2 disposal events, got %d", len(result2.Disposals))
	}
	d2 := result2.Disposals[1]
	// remaining 2 of lot 2 (cost 2*120=240) + 2 of lot 3 (cost 2*140=280) = 520
	wantCost2 := decimal.NewFromInt(520)
	if !d2.CostBasis.Equal(wantCost2) {
		t.Errorf("second disposal CostBasis = %s, want %s (should continue FIFO from partially-consumed lot 2)", d2.CostBasis, wantCost2)
	}
}

func TestComputeCGT_NonEURCurrency_ConvertsAtTransactionDateECBRate(t *testing.T) {
	// eurofxref-hist.csv: USD is 1.2152 per EUR on 2021-01-25 and
	// 1.0956 per EUR on 2024-01-02. Cost basis is fixed in euro at the
	// buy-date rate, proceeds in euro at the sell-date rate.
	buy := buyTx(t, "AAPL", "2021-01-25", 10, 100)
	buy.Currency = "USD"
	sell := sellTx(t, "AAPL", "2024-01-02", 10, 150)
	sell.Currency = "USD"

	result, err := ComputeCGT([]ledger.Transaction{buy, sell})
	if err != nil {
		t.Fatalf("ComputeCGT: %v", err)
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
		t.Errorf("CostBasis = %s, want %s (1000 USD at the 2021-01-25 rate)", d.CostBasis, wantCost)
	}
	if !d.Proceeds.Equal(wantProceeds) {
		t.Errorf("Proceeds = %s, want %s (1500 USD at the 2024-01-02 rate)", d.Proceeds, wantProceeds)
	}
	if !d.Gain.Equal(wantProceeds.Sub(wantCost)) {
		t.Errorf("Gain = %s, want %s", d.Gain, wantProceeds.Sub(wantCost))
	}
}

func TestComputeCGT_UnresolvableFXRate_ReturnsError(t *testing.T) {
	buy := buyTx(t, "AAPL", "2024-01-01", 10, 100)
	buy.Currency = "ZZZ" // not an ECB reference currency

	_, err := ComputeCGT([]ledger.Transaction{buy})
	if err == nil {
		t.Fatal("expected an error when the FX rate for a transaction cannot be resolved")
	}
}

func TestComputeCGT_SellExceedsAvailableLots_ReturnsError(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2024-01-01", 5, 100),
		sellTx(t, "AAPL", "2024-06-01", 10, 150), // more than was ever bought
	}

	_, err := ComputeCGT(txs)
	if err == nil {
		t.Fatal("expected an error when a sell exceeds available lot quantity")
	}
}
