package main

import (
	"strings"
	"testing"
)

func TestRunValidate_CGTSimpleFIFO_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/cgt_simple_fifo", "--kind", "cgt"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_RSUVestDisposal_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/rsu_vest_disposal", "--kind", "cgt"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_ExitTaxActualDisposal_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/exittax_actual_disposal", "--kind", "exit_tax"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_ExitTaxRateTransition_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/exittax_rate_transition", "--kind", "exit_tax"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_CGTYearSingleExemption_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/cgt_year_single_exemption", "--kind", "cgt_year", "--year", "2024"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_CGTLossCarryforward_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/cgt_loss_carryforward", "--kind", "cgt_year", "--year", "2025"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_ExitTaxNoIntraFundNetting_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/exittax_no_intra_fund_netting", "--kind", "exit_tax"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_DIRTHistoricalRate_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/dirt_historical_rate", "--kind", "dirt"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_CGTYearMissingYear_ReturnsError(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/cgt_year_single_exemption", "--kind", "cgt_year"})
	if err == nil {
		t.Fatal("expected an error when --kind cgt_year is given without --year")
	}
}

func TestRunValidate_CGTFXConversion_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/cgt_fx_conversion", "--kind", "cgt"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_DIRTSimple_Passes(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/dirt_simple", "--kind", "dirt"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_MismatchedFixture_ReturnsDiffError(t *testing.T) {
	// exittax_actual_disposal's input, validated as if it were CGT
	// (different rate, different exemption treatment), should NOT
	// match its own expected.json — confirming runValidate actually
	// diffs rather than always reporting success.
	err := runValidate([]string{"--fixture", "../../testdata/golden/exittax_actual_disposal", "--kind", "cgt"})
	if err == nil {
		t.Fatal("expected a mismatch error when validating exit-tax input against CGT rules")
	}
}

func TestRunValidate_MissingFixtureFlag_ReturnsUsageError(t *testing.T) {
	err := runValidate(nil)
	if err == nil {
		t.Fatal("expected an error when --fixture is not given")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("expected usage text in error, got: %v", err)
	}
}

func TestRunValidate_NonexistentFixture_ReturnsError(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/does_not_exist", "--kind", "cgt"})
	if err == nil {
		t.Fatal("expected an error for a nonexistent fixture directory")
	}
}

func TestRunValidate_InvalidKind_ReturnsError(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/cgt_simple_fifo", "--kind", "not-a-real-kind"})
	if err == nil {
		t.Fatal("expected an error for an invalid --kind value")
	}
}

func TestRunValidate_DeemedDisposalFirstEvent(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/deemed_disposal_first_event", "--kind", "deemed", "--year", "2024"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_DeemedDisposalCreditOnActualDisposal(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/deemed_disposal_credit_on_actual_disposal", "--kind", "deemed", "--year", "2024"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_DeemedDisposalRefundOnLaterFall(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/deemed_disposal_refund_on_later_fall", "--kind", "deemed", "--year", "2024"})
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
}

func TestRunValidate_DeemedRequiresYear(t *testing.T) {
	err := runValidate([]string{"--fixture", "../../testdata/golden/deemed_disposal_first_event", "--kind", "deemed"})
	if err == nil {
		t.Fatal("expected --kind deemed without --year to be rejected")
	}
}
