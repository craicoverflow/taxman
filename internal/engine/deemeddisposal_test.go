package engine

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestNextDeemedDisposalAnniversary_EightYearsFromAcquisition(t *testing.T) {
	lot := Lot{
		AcquiredDate: mustDate(t, "2020-03-15"),
		Quantity:     decimal.NewFromInt(10),
		Remaining:    decimal.NewFromInt(10),
		CostBasis:    decimal.NewFromInt(1000),
	}

	got := NextDeemedDisposalAnniversary(lot, mustDate(t, "2024-01-01"))
	want := mustDate(t, "2028-03-15")
	if !got.Equal(want) {
		t.Errorf("NextDeemedDisposalAnniversary = %v, want %v (8 years from acquisition)", got, want)
	}
}

func TestNextDeemedDisposalAnniversary_SecondCycle_SixteenYears(t *testing.T) {
	lot := Lot{
		AcquiredDate: mustDate(t, "2010-03-15"),
		Quantity:     decimal.NewFromInt(10),
		Remaining:    decimal.NewFromInt(10),
		CostBasis:    decimal.NewFromInt(1000),
	}

	// asOf is after the first anniversary (2018-03-15) but before the
	// second (2026-03-15) - should return the second, not the first.
	got := NextDeemedDisposalAnniversary(lot, mustDate(t, "2024-01-01"))
	want := mustDate(t, "2026-03-15")
	if !got.Equal(want) {
		t.Errorf("NextDeemedDisposalAnniversary = %v, want %v (16 years from acquisition, the next upcoming cycle)", got, want)
	}
}

func TestNextDeemedDisposalAnniversary_ThirdCycle_TwentyFourYears(t *testing.T) {
	lot := Lot{
		AcquiredDate: mustDate(t, "2000-03-15"),
		Quantity:     decimal.NewFromInt(10),
		Remaining:    decimal.NewFromInt(10),
		CostBasis:    decimal.NewFromInt(1000),
	}

	// asOf is after both the first (2008) and second (2016)
	// anniversaries - should return the third (2024).
	got := NextDeemedDisposalAnniversary(lot, mustDate(t, "2020-01-01"))
	want := mustDate(t, "2024-03-15")
	if !got.Equal(want) {
		t.Errorf("NextDeemedDisposalAnniversary = %v, want %v (24 years from acquisition)", got, want)
	}
}

func TestNextDeemedDisposalAnniversary_ExactlyOnAnniversary_ReturnsThatDate(t *testing.T) {
	lot := Lot{
		AcquiredDate: mustDate(t, "2020-03-15"),
		Quantity:     decimal.NewFromInt(10),
		Remaining:    decimal.NewFromInt(10),
		CostBasis:    decimal.NewFromInt(1000),
	}

	// asOf is exactly on the 8-year anniversary - that date itself
	// counts as "reached," so the next anniversary is the following
	// cycle (16 years), not the one that's already here.
	got := NextDeemedDisposalAnniversary(lot, mustDate(t, "2028-03-15"))
	want := mustDate(t, "2036-03-15")
	if !got.Equal(want) {
		t.Errorf("NextDeemedDisposalAnniversary on the anniversary itself = %v, want %v (the following cycle)", got, want)
	}
}

func TestNextDeemedDisposalAnniversary_LeapYearAcquisition(t *testing.T) {
	// 2020-02-29 was a leap day. +8 years lands on 2028, also a leap
	// year, so this should resolve cleanly to 2028-02-29 rather than
	// erroring or silently shifting to 2028-03-01.
	lot := Lot{
		AcquiredDate: mustDate(t, "2020-02-29"),
		Quantity:     decimal.NewFromInt(10),
		Remaining:    decimal.NewFromInt(10),
		CostBasis:    decimal.NewFromInt(1000),
	}

	got := NextDeemedDisposalAnniversary(lot, mustDate(t, "2024-01-01"))
	want := time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("NextDeemedDisposalAnniversary (leap year) = %v, want %v", got, want)
	}
}

func TestNextDeemedDisposalAnniversary_DoesNotComputeLiability(t *testing.T) {
	// This is a compile-time/structural assertion as much as a
	// runtime one: NextDeemedDisposalAnniversary takes no Classifier,
	// no taxrules.Kind, and returns only a time.Time - it has no way
	// to touch internal/taxrules or produce a liability figure. The
	// split is deliberate and still worth holding: the date questions
	// ("when is the next one", "which ones need a value") must stay
	// answerable on a holding whose anniversary values have not been
	// entered yet, which is exactly when they are asked. Liability is
	// ComputeFundTax's job.
	lot := Lot{
		AcquiredDate: mustDate(t, "2020-03-15"),
		Quantity:     decimal.NewFromInt(10),
		Remaining:    decimal.NewFromInt(10),
		CostBasis:    decimal.NewFromInt(1000),
	}
	got := NextDeemedDisposalAnniversary(lot, mustDate(t, "2024-01-01"))
	if got.IsZero() {
		t.Fatal("expected a non-zero anniversary date")
	}
	// Nothing else to assert here beyond the function signature itself
	// (time.Time in, time.Time out) — the real guarantee is enforced
	// by the type system, not a runtime check.
}
