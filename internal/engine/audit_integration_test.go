package engine

import (
	"testing"

	"github.com/craicoverflow/taxman/internal/audit"
	"github.com/craicoverflow/taxman/internal/ledger"
	"github.com/craicoverflow/taxman/internal/taxrules"
)

// The *Txs helpers below reproduce each SPEC.md §5 golden fixture's
// exact input (see testdata/golden/*), matching the equivalent cases
// in cgt_test.go, rsu_test.go, exittax_test.go, and
// exittax_ratetransition_test.go.

func cgtSimpleFIFOTxs(t *testing.T) []ledger.Transaction {
	t.Helper()
	return []ledger.Transaction{
		buyTx(t, "AAPL", "2024-01-01", 10, 100),
		sellTx(t, "AAPL", "2024-06-01", 10, 150),
	}
}

func rsuVestTxs(t *testing.T) []ledger.Transaction {
	t.Helper()
	return []ledger.Transaction{
		vestTx(t, "ETRADE_CO", "2024-01-15", 20, 40),
		sellTx(t, "ETRADE_CO", "2024-08-01", 20, 60),
	}
}

func exitTaxActualTxs(t *testing.T) []ledger.Transaction {
	t.Helper()
	return []ledger.Transaction{
		buyTx(t, "IE_ETF", "2024-01-01", 100, 10),
		sellTx(t, "IE_ETF", "2024-06-01", 100, 15),
	}
}

func exitTaxRateTransitionTxs(t *testing.T) []ledger.Transaction {
	t.Helper()
	return []ledger.Transaction{
		buyTx(t, "IE_ETF", "2020-01-01", 100, 10),
		sellTx(t, "IE_ETF", "2025-12-31", 100, 20),
	}
}

// toDisposalLike bridges engine.Disposal to audit.DisposalLike. engine
// depends on audit (not the reverse — see audit.go's DisposalLike doc
// comment), so this conversion lives here, on the engine side.
func toDisposalLike(ds []Disposal) []audit.DisposalLike {
	out := make([]audit.DisposalLike, len(ds))
	for i, d := range ds {
		out[i] = audit.DisposalLike{
			Date:      d.Date,
			Quantity:  d.Quantity,
			Proceeds:  d.Proceeds,
			CostBasis: d.CostBasis,
			Gain:      d.Gain,
		}
	}
	return out
}

// auditRateInfo converts a taxrules.Rate into the audit.RateInfo shape.
func auditRateInfo(rate taxrules.Rate) audit.RateInfo {
	return audit.RateInfo{
		EffectiveFrom: rate.EffectiveFrom,
		Rate:          rate.Rate,
	}
}

// TestEngineDisposals_EveryLiabilityFigureHasAnAuditRecord is the
// invariant test tasks/plan.md task 5.1 calls for, run across each of
// the 5 unblocked SPEC.md §5 golden-fixture scenarios (4.2-4.6): every
// disposal engine produces must be traceable to an audit.Record
// carrying its source disposal date and the exact taxrules.Rate
// version applied.
func TestEngineDisposals_EveryLiabilityFigureHasAnAuditRecord(t *testing.T) {
	cases := []struct {
		name         string
		disposals    []Disposal
		auditKind    audit.Kind
		taxrulesKind taxrules.Kind
	}{
		{
			name:         "cgt_simple_fifo",
			disposals:    cgtSimpleFIFODisposals(t),
			auditKind:    audit.KindCGT,
			taxrulesKind: taxrules.KindCGT,
		},
		{
			name:         "rsu_vest_disposal",
			disposals:    rsuVestDisposals(t),
			auditKind:    audit.KindCGT,
			taxrulesKind: taxrules.KindCGT,
		},
		{
			name:         "exittax_actual_disposal",
			disposals:    exitTaxActualDisposals(t),
			auditKind:    audit.KindExitTax,
			taxrulesKind: taxrules.KindExitTax,
		},
		{
			name:         "exittax_rate_transition",
			disposals:    exitTaxRateTransitionDisposals(t),
			auditKind:    audit.KindExitTax,
			taxrulesKind: taxrules.KindExitTax,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if len(c.disposals) == 0 {
				t.Fatal("scenario produced no disposals to audit")
			}

			rate, err := taxrules.Lookup(c.taxrulesKind, latestDisposalDate(c.disposals))
			if err != nil {
				t.Fatalf("taxrules.Lookup: %v", err)
			}

			records, err := audit.FromDisposals(c.name, c.auditKind, toDisposalLike(c.disposals), auditRateInfo(rate))
			if err != nil {
				t.Fatalf("audit.FromDisposals: %v", err)
			}

			if len(records) != len(c.disposals) {
				t.Fatalf("expected %d audit records (one per disposal), got %d", len(c.disposals), len(records))
			}
			for i, rec := range records {
				if rec.Kind != c.auditKind {
					t.Errorf("record[%d].Kind = %q, want %q", i, rec.Kind, c.auditKind)
				}
				if !rec.DisposalDate.Equal(c.disposals[i].Date) {
					t.Errorf("record[%d].DisposalDate = %v, want %v", i, rec.DisposalDate, c.disposals[i].Date)
				}
				if !rec.RuleRate.Equal(rate.Rate) {
					t.Errorf("record[%d].RuleRate = %s, want %s", i, rec.RuleRate, rate.Rate)
				}
				if rec.RuleEffectiveFrom.IsZero() {
					t.Errorf("record[%d].RuleEffectiveFrom is zero; every record must trace to a specific rule version", i)
				}
			}
		})
	}
}

// The scenarios below reproduce the exact inputs from each SPEC.md §5
// golden fixture (see testdata/golden/*), matching cgt_test.go,
// rsu_test.go, and exittax_test.go's fixture-equivalent test cases.

func cgtSimpleFIFODisposals(t *testing.T) []Disposal {
	t.Helper()
	result, err := ComputeCGT(cgtSimpleFIFOTxs(t))
	if err != nil {
		t.Fatalf("ComputeCGT: %v", err)
	}
	return result.Disposals
}

func rsuVestDisposals(t *testing.T) []Disposal {
	t.Helper()
	result, err := ComputeCGT(rsuVestTxs(t))
	if err != nil {
		t.Fatalf("ComputeCGT: %v", err)
	}
	return result.Disposals
}

func exitTaxActualDisposals(t *testing.T) []Disposal {
	t.Helper()
	result, err := ComputeExitTax(exitTaxActualTxs(t))
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	return result.Disposals
}

func exitTaxRateTransitionDisposals(t *testing.T) []Disposal {
	t.Helper()
	result, err := ComputeExitTax(exitTaxRateTransitionTxs(t))
	if err != nil {
		t.Fatalf("ComputeExitTax: %v", err)
	}
	return result.Disposals
}
