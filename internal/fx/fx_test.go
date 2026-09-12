package fx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

// eurPerUnit is what Rate should return for an ECB "units per euro"
// figure taken straight from eurofxref-hist.csv.
func eurPerUnit(ecb string) decimal.Decimal {
	return decimal.NewFromInt(1).Div(decimal.RequireFromString(ecb))
}

func TestRate_EURIsIdentity(t *testing.T) {
	for _, c := range []string{"EUR", "eur", "", "  "} {
		got, err := Rate(c, mustDate(t, "2021-01-25"))
		if err != nil {
			t.Fatalf("Rate(%q): %v", c, err)
		}
		if !got.Equal(decimal.NewFromInt(1)) {
			t.Errorf("Rate(%q) = %s, want 1", c, got)
		}
	}
}

func TestRate_BusinessDay_UsesThatDaysRate(t *testing.T) {
	// eurofxref-hist.csv: 2021-01-25 (a Monday) has USD = 1.2152.
	got, err := Rate("USD", mustDate(t, "2021-01-25"))
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	want := eurPerUnit("1.2152")
	if !got.Equal(want) {
		t.Errorf("Rate(USD, 2021-01-25) = %s, want %s", got, want)
	}
}

func TestRate_LowercaseCurrency_IsNormalized(t *testing.T) {
	got, err := Rate("usd", mustDate(t, "2021-01-25"))
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if !got.Equal(eurPerUnit("1.2152")) {
		t.Errorf("Rate(usd, ...) = %s, want the USD rate", got)
	}
}

func TestRate_WeekendDate_ResolvesToPriorPublicationDay(t *testing.T) {
	// 2021-01-23 and -24 are a weekend; the prior TARGET day is
	// 2021-01-22, USD = 1.2158.
	want := eurPerUnit("1.2158")
	for _, d := range []string{"2021-01-23", "2021-01-24"} {
		got, err := Rate("USD", mustDate(t, d))
		if err != nil {
			t.Fatalf("Rate(USD, %s): %v", d, err)
		}
		if !got.Equal(want) {
			t.Errorf("Rate(USD, %s) = %s, want the 2021-01-22 rate %s", d, got, want)
		}
	}
}

func TestRate_UnknownCurrency_Errors(t *testing.T) {
	_, err := Rate("XYZ", mustDate(t, "2021-01-25"))
	if err == nil {
		t.Fatal("expected an error for an unknown currency")
	}
}

func TestRate_DateBeforeSeriesBegins_Errors(t *testing.T) {
	_, err := Rate("USD", mustDate(t, "1998-06-01"))
	if err == nil {
		t.Fatal("expected an error for a date before the ECB series begins")
	}
}

func TestRate_DateFarPastLastPublication_Errors(t *testing.T) {
	// A live refresh is exercised separately in refresh_test.go; stub
	// it here to fail fast (as if offline) so this stays a pure
	// stale-data test with no real network call.
	stubFetch(t, func(ctx context.Context) (string, error) {
		return "", errors.New("network unreachable")
	})

	_, err := Rate("USD", mustDate(t, "2099-01-01"))
	if err == nil {
		t.Fatal("expected an error for a date well past the last published rate")
	}
}

func TestRate_RetiredCurrency_ErrorsRatherThanReachingBack(t *testing.T) {
	// HRK (Croatian kuna) stopped being published when Croatia
	// adopted the euro on 2023-01-01. A query well after that must
	// not silently return a 2022 rate.
	_, err := Rate("HRK", mustDate(t, "2024-06-03"))
	if err == nil {
		t.Fatal("expected an error for a retired currency queried long after its last publication")
	}
	// ...but a date while it was still published resolves fine.
	if _, err := Rate("HRK", mustDate(t, "2020-06-01")); err != nil {
		t.Errorf("Rate(HRK, 2020-06-01) should still work: %v", err)
	}
}

func TestSource_NamesECBAndCurrency(t *testing.T) {
	s := Source()
	if s == "" {
		t.Fatal("Source() is empty")
	}
	if want := "ECB eurofxref-hist"; !contains(s, want) {
		t.Errorf("Source() = %q, want it to start with %q", s, want)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && s[:len(sub)] == sub
}
