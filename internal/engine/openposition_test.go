package engine

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func TestOpenPositions_NoDisposals_ReturnsTotalAcquired(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2023-01-01", 5, 100), // 5 @ 100 = 500
		buyTx(t, "AAPL", "2023-06-01", 5, 120), // 5 @ 120 = 600
	}

	pos, err := OpenPositions(txs)
	if err != nil {
		t.Fatalf("OpenPositions: %v", err)
	}
	if !pos.Quantity.Equal(decimal.NewFromInt(10)) {
		t.Errorf("Quantity = %s, want 10", pos.Quantity)
	}
	if !pos.CostBasis.Equal(decimal.NewFromInt(1100)) {
		t.Errorf("CostBasis = %s, want 1100", pos.CostBasis)
	}
}

func TestOpenPositions_PartialFIFOConsumption_SurvivingLotsOnly(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2023-01-01", 5, 100),  // lot 1: 5 @ 100 = 500
		buyTx(t, "AAPL", "2023-06-01", 5, 120),  // lot 2: 5 @ 120 = 600
		buyTx(t, "AAPL", "2023-09-01", 5, 140),  // lot 3: 5 @ 140 = 700
		sellTx(t, "AAPL", "2024-01-01", 8, 200), // consumes all of lot 1, 3 of lot 2
	}

	pos, err := OpenPositions(txs)
	if err != nil {
		t.Fatalf("OpenPositions: %v", err)
	}
	// Surviving: 2 of lot 2 @ 120 (= 240) + all of lot 3 (5 @ 140 = 700).
	if !pos.Quantity.Equal(decimal.NewFromInt(7)) {
		t.Errorf("Quantity = %s, want 7", pos.Quantity)
	}
	if !pos.CostBasis.Equal(decimal.NewFromInt(940)) {
		t.Errorf("CostBasis = %s, want 940", pos.CostBasis)
	}
}

func TestOpenPositions_FullyDisposed_ReturnsZero(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2023-01-01", 10, 100),
		sellTx(t, "AAPL", "2024-01-01", 10, 150),
	}

	pos, err := OpenPositions(txs)
	if err != nil {
		t.Fatalf("OpenPositions: %v", err)
	}
	if !pos.Quantity.IsZero() {
		t.Errorf("Quantity = %s, want 0", pos.Quantity)
	}
	if !pos.CostBasis.IsZero() {
		t.Errorf("CostBasis = %s, want 0", pos.CostBasis)
	}
}

func TestOpenPositions_Oversell_ReturnsErrorWithoutPanic(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2023-01-01", 5, 100),
		sellTx(t, "AAPL", "2024-01-01", 8, 150), // sells more than is held
	}

	if _, err := OpenPositions(txs); err == nil {
		t.Fatal("expected an error for selling more than the held quantity, got nil")
	} else if !strings.Contains(err.Error(), "exceeds available lot quantity") {
		t.Errorf("error = %q, want it to mention exceeding available lot quantity", err)
	}
}

func TestOpenPositions_NonEURBuy_CostBasisRestatedInEUR(t *testing.T) {
	// ECB USD reference rate 1.2152/EUR on 2021-01-25 — the same rate
	// the CGT and exit-tax FX-conversion tests pin against.
	buy := buyTx(t, "AAPL", "2021-01-25", 10, 100) // 1000 USD
	buy.Currency = "USD"

	pos, err := OpenPositions([]ledger.Transaction{buy})
	if err != nil {
		t.Fatalf("OpenPositions: %v", err)
	}
	want := decimal.NewFromInt(1000).Mul(decimal.NewFromInt(1).Div(decimal.RequireFromString("1.2152")))
	if !pos.CostBasis.Equal(want) {
		t.Errorf("CostBasis = %s, want %s (1000 USD at ECB 1.2152)", pos.CostBasis, want)
	}
	if !pos.Quantity.Equal(decimal.NewFromInt(10)) {
		t.Errorf("Quantity = %s, want 10", pos.Quantity)
	}
}
