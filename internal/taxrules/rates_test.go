package taxrules

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

func TestLookup_ExitTax_RateTransition(t *testing.T) {
	tests := []struct {
		name string
		date string
		want string // decimal string, e.g. "0.41"
	}{
		{"day before transition", "2025-12-31", "0.41"},
		{"day of transition", "2026-01-01", "0.38"},
		{"well after transition", "2026-06-15", "0.38"},
		{"well before transition", "2020-01-01", "0.41"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, err := Lookup(KindExitTax, mustDate(t, tt.date))
			if err != nil {
				t.Fatalf("Lookup(KindExitTax, %s): unexpected error: %v", tt.date, err)
			}
			want := decimal.RequireFromString(tt.want)
			if !rate.Rate.Equal(want) {
				t.Errorf("Lookup(KindExitTax, %s).Rate = %s, want %s", tt.date, rate.Rate, want)
			}
		})
	}
}

// Historical schedules (finding #4): a backfilled disposal/credit in an
// earlier tax year must resolve to that year's rate, not today's. The
// pre-2013 CGT / pre-2014 DIRT & exit-tax values are UNVERIFIED (see
// rates.go) — these cases pin the table's shape and the resolve-by-date
// behaviour, not a citation.
func TestLookup_HistoricalRates_ResolveByEventDate(t *testing.T) {
	tests := []struct {
		kind Kind
		date string
		want string
	}{
		{KindCGT, "2007-06-01", "0.20"},
		{KindCGT, "2008-10-20", "0.22"},
		{KindCGT, "2010-01-01", "0.25"},
		{KindCGT, "2012-01-01", "0.30"},
		{KindCGT, "2012-12-06", "0.33"},
		{KindCGT, "2024-06-01", "0.33"},

		{KindDIRT, "2013-06-01", "0.33"},
		{KindDIRT, "2014-06-01", "0.41"},
		{KindDIRT, "2016-12-31", "0.41"},
		{KindDIRT, "2017-06-01", "0.39"},
		{KindDIRT, "2018-06-01", "0.37"},
		{KindDIRT, "2019-06-01", "0.35"},
		{KindDIRT, "2020-06-01", "0.33"},

		{KindExitTax, "2012-06-01", "0.33"},
		{KindExitTax, "2013-06-01", "0.36"},
		{KindExitTax, "2014-06-01", "0.41"},
		{KindExitTax, "2025-12-31", "0.41"},
		{KindExitTax, "2026-01-01", "0.38"},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind)+"_"+tt.date, func(t *testing.T) {
			rate, err := Lookup(tt.kind, mustDate(t, tt.date))
			if err != nil {
				t.Fatalf("Lookup(%s, %s): %v", tt.kind, tt.date, err)
			}
			if want := decimal.RequireFromString(tt.want); !rate.Rate.Equal(want) {
				t.Errorf("Lookup(%s, %s).Rate = %s, want %s", tt.kind, tt.date, rate.Rate, want)
			}
			if rate.EffectiveFrom.After(mustDate(t, tt.date)) {
				t.Errorf("EffectiveFrom %s is after the event date %s", rate.EffectiveFrom.Format("2006-01-02"), tt.date)
			}
		})
	}
}

func TestLookup_HistoricalCGT_KeepsAnnualExemption(t *testing.T) {
	rate, err := Lookup(KindCGT, mustDate(t, "2009-06-01"))
	if err != nil {
		t.Fatalf("Lookup(KindCGT, 2009): %v", err)
	}
	if want := decimal.RequireFromString("1270"); !rate.AnnualExemption.Equal(want) {
		t.Errorf("2009 CGT AnnualExemption = %s, want %s", rate.AnnualExemption, want)
	}
}

func TestLookup_CGT_RateAndExemption(t *testing.T) {
	rate, err := Lookup(KindCGT, mustDate(t, "2024-06-01"))
	if err != nil {
		t.Fatalf("Lookup(KindCGT): unexpected error: %v", err)
	}
	wantRate := decimal.RequireFromString("0.33")
	if !rate.Rate.Equal(wantRate) {
		t.Errorf("CGT Rate = %s, want %s", rate.Rate, wantRate)
	}
	wantExemption := decimal.RequireFromString("1270")
	if !rate.AnnualExemption.Equal(wantExemption) {
		t.Errorf("CGT AnnualExemption = %s, want %s", rate.AnnualExemption, wantExemption)
	}
}

func TestLookup_DIRT_Rate(t *testing.T) {
	rate, err := Lookup(KindDIRT, mustDate(t, "2024-06-01"))
	if err != nil {
		t.Fatalf("Lookup(KindDIRT): unexpected error: %v", err)
	}
	want := decimal.RequireFromString("0.33")
	if !rate.Rate.Equal(want) {
		t.Errorf("DIRT Rate = %s, want %s", rate.Rate, want)
	}
}

func TestLookup_UndefinedDate_ReturnsExplicitError(t *testing.T) {
	// Deliberately before the schedule's earliest defined entry for
	// any kind, so this can never accidentally coincide with a
	// boundary added later.
	_, err := Lookup(KindCGT, mustDate(t, "0001-01-01"))
	if err == nil {
		t.Fatal("expected an error for a date with no defined rate, got nil")
	}
}

func TestLookup_UndefinedDate_ReturnsZeroValue(t *testing.T) {
	rate, err := Lookup(KindCGT, mustDate(t, "0001-01-01"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !rate.Rate.IsZero() {
		t.Errorf("expected zero-value Rate on error, got %s", rate.Rate)
	}
}

func TestLookup_UnknownKind_ReturnsError(t *testing.T) {
	_, err := Lookup(Kind("not-a-real-kind"), mustDate(t, "2024-01-01"))
	if err == nil {
		t.Fatal("expected an error for an unknown Kind, got nil")
	}
}

func TestLookup_EnvOverride_ReplacesScheduledRate(t *testing.T) {
	// Pre-transition date would otherwise resolve to 0.41; the
	// override wins regardless of event date.
	t.Setenv("TAXMAN_EXIT_TAX_RATE", "0.38")

	rate, err := Lookup(KindExitTax, mustDate(t, "2024-06-01"))
	if err != nil {
		t.Fatalf("Lookup(KindExitTax): unexpected error: %v", err)
	}
	if want := decimal.RequireFromString("0.38"); !rate.Rate.Equal(want) {
		t.Errorf("Rate = %s, want %s (env override)", rate.Rate, want)
	}
	// EffectiveFrom still points at the schedule entry that was
	// overridden, not zeroed.
	if rate.EffectiveFrom.IsZero() {
		t.Error("EffectiveFrom = zero, want the overridden schedule entry's date")
	}
}

func TestLookup_EnvOverride_KeepsCGTAnnualExemption(t *testing.T) {
	t.Setenv("TAXMAN_CGT_RATE", "0.20")

	rate, err := Lookup(KindCGT, mustDate(t, "2024-06-01"))
	if err != nil {
		t.Fatalf("Lookup(KindCGT): unexpected error: %v", err)
	}
	if want := decimal.RequireFromString("0.20"); !rate.Rate.Equal(want) {
		t.Errorf("Rate = %s, want %s", rate.Rate, want)
	}
	if want := decimal.RequireFromString("1270"); !rate.AnnualExemption.Equal(want) {
		t.Errorf("AnnualExemption = %s, want %s (schedule value, untouched by rate override)", rate.AnnualExemption, want)
	}
}

func TestLookup_EnvOverride_InvalidValue_ReturnsError(t *testing.T) {
	for _, raw := range []string{"not-a-number", "38", "-0.1", "1.5"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("TAXMAN_DIRT_RATE", raw)
			if _, err := Lookup(KindDIRT, mustDate(t, "2024-06-01")); err == nil {
				t.Fatalf("Lookup with TAXMAN_DIRT_RATE=%q: expected an error, got nil", raw)
			}
		})
	}
}

func TestLookup_EnvOverride_EmptyIsIgnored(t *testing.T) {
	t.Setenv("TAXMAN_EXIT_TAX_RATE", "")

	rate, err := Lookup(KindExitTax, mustDate(t, "2025-12-31"))
	if err != nil {
		t.Fatalf("Lookup(KindExitTax): unexpected error: %v", err)
	}
	if want := decimal.RequireFromString("0.41"); !rate.Rate.Equal(want) {
		t.Errorf("Rate = %s, want %s (schedule value; empty env var is not an override)", rate.Rate, want)
	}
}
