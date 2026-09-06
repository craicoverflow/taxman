package engine

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func interestTx(t *testing.T, date string, amount int64) ledger.Transaction {
	t.Helper()
	tx := buyTx(t, "Savings account", date, 1, amount) // Quantity=1, Price=amount -> amount
	tx.Type = ledger.TypeInterest
	return tx
}

func TestComputeDIRT_SingleCredit(t *testing.T) {
	txs := []ledger.Transaction{
		interestTx(t, "2024-06-01", 200), // €200 interest credited
	}

	result, err := ComputeDIRT(txs)
	if err != nil {
		t.Fatalf("ComputeDIRT: %v", err)
	}

	wantInterest := decimal.NewFromInt(200)
	if !result.TotalInterest.Equal(wantInterest) {
		t.Errorf("TotalInterest = %s, want %s", result.TotalInterest, wantInterest)
	}
	// DIRT 33% * 200 = 66
	wantTax := decimal.NewFromInt(66)
	if !result.TaxDue.Equal(wantTax) {
		t.Errorf("TaxDue = %s, want %s", result.TaxDue, wantTax)
	}
}

func TestComputeDIRT_MultipleCredits_Summed(t *testing.T) {
	txs := []ledger.Transaction{
		interestTx(t, "2024-03-01", 100),
		interestTx(t, "2024-06-01", 50),
		interestTx(t, "2024-09-01", 75),
	}

	result, err := ComputeDIRT(txs)
	if err != nil {
		t.Fatalf("ComputeDIRT: %v", err)
	}

	wantInterest := decimal.NewFromInt(225)
	if !result.TotalInterest.Equal(wantInterest) {
		t.Errorf("TotalInterest = %s, want %s", result.TotalInterest, wantInterest)
	}
	wantTax := decimal.RequireFromString("74") // 225 * 0.33 = 74.25, rounded down to whole euro
	if !result.TaxDue.Equal(wantTax) {
		t.Errorf("TaxDue = %s, want %s (74.25 rounded down)", result.TaxDue, wantTax)
	}
}

func TestComputeDIRT_NoInterestCredits_ZeroResult(t *testing.T) {
	result, err := ComputeDIRT(nil)
	if err != nil {
		t.Fatalf("ComputeDIRT: %v", err)
	}
	if !result.TotalInterest.IsZero() {
		t.Errorf("TotalInterest = %s, want 0", result.TotalInterest)
	}
	if !result.TaxDue.IsZero() {
		t.Errorf("TaxDue = %s, want 0", result.TaxDue)
	}
}

func TestComputeDIRT_IgnoresNonInterestTransactions(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "AAPL", "2024-01-01", 10, 100), // not interest - must be ignored
		interestTx(t, "2024-06-01", 50),
	}

	result, err := ComputeDIRT(txs)
	if err != nil {
		t.Fatalf("ComputeDIRT: %v", err)
	}
	wantInterest := decimal.NewFromInt(50)
	if !result.TotalInterest.Equal(wantInterest) {
		t.Errorf("TotalInterest = %s, want %s (should ignore the non-interest transaction)", result.TotalInterest, wantInterest)
	}
}

func TestComputeDIRT_NonEURCurrency_ReturnsExplicitError(t *testing.T) {
	tx := interestTx(t, "2024-06-01", 100)
	tx.Currency = "USD"

	_, err := ComputeDIRT([]ledger.Transaction{tx})
	if err == nil {
		t.Fatal("expected an explicit error for a non-EUR interest transaction")
	}
}
