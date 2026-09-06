package engine

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func vestTx(t *testing.T, instrument, date string, qty, fmv int64) ledger.Transaction {
	t.Helper()
	tx := buyTx(t, instrument, date, qty, fmv)
	tx.Type = ledger.TypeRSUVest
	return tx
}

func TestComputeCGT_RSUVest_CostBasisIsVestDateFMV(t *testing.T) {
	// Vest 20 shares at FMV €40/share, then sell all 20 at €60/share.
	// Cost basis must be the vest-date FMV (20*40=800), NOT zero and
	// NOT the eventual sale price.
	txs := []ledger.Transaction{
		vestTx(t, "ETRADE_CO", "2024-01-15", 20, 40),
		sellTx(t, "ETRADE_CO", "2024-08-01", 20, 60),
	}

	result, err := ComputeCGT(txs)
	if err != nil {
		t.Fatalf("ComputeCGT: %v", err)
	}
	if len(result.Disposals) != 1 {
		t.Fatalf("expected 1 disposal, got %d", len(result.Disposals))
	}

	d := result.Disposals[0]
	wantCostBasis := decimal.NewFromInt(800) // 20 * 40 (vest-date FMV), not 0 or 20*60
	if !d.CostBasis.Equal(wantCostBasis) {
		t.Errorf("CostBasis = %s, want %s (vest-date FMV, not zero or sale price)", d.CostBasis, wantCostBasis)
	}
	wantGain := decimal.NewFromInt(400) // (60-40)*20
	if !d.Gain.Equal(wantGain) {
		t.Errorf("Gain = %s, want %s", d.Gain, wantGain)
	}
}

func TestComputeCGT_RSUVest_ThenPartialSale_FIFOWithBuys(t *testing.T) {
	// A vest lot participates in FIFO ordering exactly like a buy lot.
	txs := []ledger.Transaction{
		vestTx(t, "ETRADE_CO", "2023-01-01", 10, 40), // vest lot: 10 @ FMV 40 = 400
		buyTx(t, "ETRADE_CO", "2023-06-01", 10, 50),  // buy lot: 10 @ 50 = 500
		sellTx(t, "ETRADE_CO", "2024-01-01", 15, 70), // sells all vest lot + 5 of buy lot
	}

	result, err := ComputeCGT(txs)
	if err != nil {
		t.Fatalf("ComputeCGT: %v", err)
	}
	if len(result.Disposals) != 1 {
		t.Fatalf("expected 1 disposal, got %d", len(result.Disposals))
	}

	d := result.Disposals[0]
	// FIFO: all 10 of the vest lot (cost 400) + 5 of the buy lot (5*50=250) = 650
	wantCostBasis := decimal.NewFromInt(650)
	if !d.CostBasis.Equal(wantCostBasis) {
		t.Errorf("CostBasis = %s, want %s (FIFO across vest and buy lots)", d.CostBasis, wantCostBasis)
	}
}
