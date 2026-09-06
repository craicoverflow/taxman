package engine

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// These tests specifically exercise the 41%->38% exit-tax rate
// transition (Finance Act 2025, effective 2026-01-01) as seen through
// ComputeExitTax, distinct from exittax_test.go's general FIFO
// coverage. See SPEC.md §5's "Exit-tax rate transition" fixture.

func TestComputeExitTax_RateTransition_DisposalBeforeBoundary_Uses41Percent(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2020-01-01", 100, 10),
		sellTx(t, "IE_ETF", "2025-12-31", 100, 20), // disposal dated the day before the boundary
	}

	result, err := ComputeExitTax(txs)
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	// gain 1000 * 0.41 = 410
	want := decimal.RequireFromString("410")
	if !result.TaxDue.Equal(want) {
		t.Errorf("TaxDue = %s, want %s (0.41 rate for a 2025-12-31 disposal)", result.TaxDue, want)
	}
}

func TestComputeExitTax_RateTransition_DisposalOnBoundary_Uses38Percent(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2020-01-01", 100, 10),
		sellTx(t, "IE_ETF", "2026-01-01", 100, 20), // disposal dated exactly on the boundary
	}

	result, err := ComputeExitTax(txs)
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	// gain 1000 * 0.38 = 380
	want := decimal.RequireFromString("380")
	if !result.TaxDue.Equal(want) {
		t.Errorf("TaxDue = %s, want %s (0.38 rate for a 2026-01-01 disposal)", result.TaxDue, want)
	}
}

// TestComputeExitTax_RateTransition_ResolvesByEventDate_NotCallTime is
// the regression the task explicitly calls for: a disposal EVENT dated
// in 2025 must resolve to the pre-transition rate regardless of when
// ComputeExitTax is actually invoked (i.e. "today," whenever that is,
// including well after 2026-01-01). ComputeExitTax has no notion of
// "the current date" at all — it only ever looks at tx.Date — so this
// test's real assertion is that no such wall-clock dependency exists:
// calling it long after the transition still uses the 41% rate for a
// pre-transition disposal, proving the rate lookup is keyed purely by
// the transaction data, never by when the code happens to run.
func TestComputeExitTax_RateTransition_ResolvesByEventDate_NotCallTime(t *testing.T) {
	txs := []ledger.Transaction{
		buyTx(t, "IE_ETF", "2020-01-01", 100, 10),
		sellTx(t, "IE_ETF", "2025-06-15", 100, 20), // squarely pre-transition
	}

	// Call ComputeExitTax "now" (whenever the test actually runs,
	// necessarily after 2025-06-15) and confirm it still resolves the
	// pre-transition rate for this disposal's own date.
	result, err := ComputeExitTax(txs)
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	want := decimal.RequireFromString("410")
	if !result.TaxDue.Equal(want) {
		t.Errorf("TaxDue = %s, want %s — a 2025-06-15 disposal must resolve 41%% regardless of when Compute is called", result.TaxDue, want)
	}
}
