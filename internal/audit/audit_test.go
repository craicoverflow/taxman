package audit

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestRecord_CapturesFullTrace(t *testing.T) {
	date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	r := Record{
		Instrument:         "AAPL",
		Kind:               KindCGT,
		LiabilityAmount:    decimal.RequireFromString("900.90"),
		SourceTransactions: []string{"fp-abc123", "fp-def456"},
		LotIDs:             []string{"lot-1", "lot-2"},
		DisposalDate:       date,
		RuleEffectiveFrom:  time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC),
		RuleRate:           decimal.RequireFromString("0.33"),
	}

	if r.Instrument != "AAPL" {
		t.Errorf("Instrument = %q, want AAPL", r.Instrument)
	}
	if len(r.SourceTransactions) != 2 {
		t.Errorf("expected 2 source transactions, got %d", len(r.SourceTransactions))
	}
	if len(r.LotIDs) != 2 {
		t.Errorf("expected 2 lot IDs, got %d", len(r.LotIDs))
	}
}

func TestFromCGTResult_OneRecordPerDisposal(t *testing.T) {
	// Minimal engine.CGTResult-shaped input, built directly here to
	// avoid an import cycle (internal/engine will depend on
	// internal/audit, not the reverse) — see audit.go's
	// CGTResultLike interface.
	disposals := []DisposalLike{
		{
			Date:      time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			CostBasis: decimal.NewFromInt(1000),
			Proceeds:  decimal.NewFromInt(1500),
			Gain:      decimal.NewFromInt(500),
		},
	}

	records, err := FromDisposals("AAPL", KindCGT, disposals, RateInfo{
		EffectiveFrom: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC),
		Rate:          decimal.RequireFromString("0.33"),
	})
	if err != nil {
		t.Fatalf("FromDisposals: %v", err)
	}

	if len(records) != len(disposals) {
		t.Fatalf("expected %d records (one per disposal), got %d", len(disposals), len(records))
	}
	if records[0].Instrument != "AAPL" {
		t.Errorf("Instrument = %q, want AAPL", records[0].Instrument)
	}
	if records[0].Kind != KindCGT {
		t.Errorf("Kind = %q, want %q", records[0].Kind, KindCGT)
	}
	if !records[0].RuleRate.Equal(decimal.RequireFromString("0.33")) {
		t.Errorf("RuleRate = %s, want 0.33", records[0].RuleRate)
	}
	if !records[0].DisposalDate.Equal(disposals[0].Date) {
		t.Errorf("DisposalDate = %v, want %v", records[0].DisposalDate, disposals[0].Date)
	}
}

func TestFromDisposals_NoDisposals_ReturnsEmpty(t *testing.T) {
	records, err := FromDisposals("AAPL", KindCGT, nil, RateInfo{})
	if err != nil {
		t.Fatalf("FromDisposals: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for no disposals, got %d", len(records))
	}
}
