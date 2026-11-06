package ingest

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

func nmTx(platform ledger.Platform, typ ledger.Type, instrument, date, qty, price string) ledger.Transaction {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return ledger.Transaction{
		Platform: platform, Type: typ, Date: d, Instrument: instrument,
		Quantity: decimal.RequireFromString(qty), Price: decimal.RequireFromString(price), Currency: "EUR",
	}
}

func TestNearMatches(t *testing.T) {
	manual := []ledger.Transaction{nmTx(ledger.PlatformManual, ledger.TypeBuy, "IE00B4L5Y983", "2024-03-15", "10", "85")}

	cases := []struct {
		name     string
		imported ledger.Transaction
		aliases  map[string]string
		want     int
	}{
		{"same trade, same price", nmTx(ledger.PlatformDegiro, ledger.TypeBuy, "IE00B4L5Y983", "2024-03-15", "10", "85"), nil, 1},
		{"same trade, different price still warns", nmTx(ledger.PlatformDegiro, ledger.TypeBuy, "IE00B4L5Y983", "2024-03-15", "10", "85.32"), nil, 1},
		{"different date", nmTx(ledger.PlatformDegiro, ledger.TypeBuy, "IE00B4L5Y983", "2024-03-16", "10", "85"), nil, 0},
		{"different quantity", nmTx(ledger.PlatformDegiro, ledger.TypeBuy, "IE00B4L5Y983", "2024-03-15", "9", "85"), nil, 0},
		{"different type", nmTx(ledger.PlatformDegiro, ledger.TypeSell, "IE00B4L5Y983", "2024-03-15", "10", "85"), nil, 0},
		{"different instrument", nmTx(ledger.PlatformDegiro, ledger.TypeBuy, "IE00BK5BQT80", "2024-03-15", "10", "85"), nil, 0},
		{"interest is never a trade", nmTx(ledger.PlatformDegiro, ledger.TypeInterest, "IE00B4L5Y983", "2024-03-15", "10", "85"), nil, 0},
		{
			"imported key merged into the manual one's key",
			nmTx(ledger.PlatformIBKR, ledger.TypeBuy, "IWDA", "2024-03-15", "10", "85"),
			map[string]string{"IWDA": "IE00B4L5Y983"}, 1,
		},
		{
			"manual key merged into the imported one's key",
			nmTx(ledger.PlatformDegiro, ledger.TypeBuy, "ISHARES", "2024-03-15", "10", "85"),
			map[string]string{"IE00B4L5Y983": "ISHARES"}, 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NearMatches(manual, []ledger.Transaction{tc.imported}, tc.aliases)
			if len(got) != tc.want {
				t.Fatalf("NearMatches = %d warnings %q, want %d", len(got), got, tc.want)
			}
		})
	}
}

func TestNearMatches_WarningNamesBothTradesAndTheRemedy(t *testing.T) {
	manual := []ledger.Transaction{nmTx(ledger.PlatformManual, ledger.TypeBuy, "IE00B4L5Y983", "2024-03-15", "10", "85")}
	imported := []ledger.Transaction{nmTx(ledger.PlatformDegiro, ledger.TypeBuy, "IE00B4L5Y983", "2024-03-15", "10", "85.32")}

	got := NearMatches(manual, imported, nil)
	if len(got) != 1 {
		t.Fatalf("warnings = %q, want 1", got)
	}
	for _, want := range []string{"IE00B4L5Y983", "2024-03-15", "85", "85.32", "manual", "delete"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("warning %q does not mention %q", got[0], want)
		}
	}
}

func TestNearMatches_NoManualTrades_NoWarnings(t *testing.T) {
	imported := []ledger.Transaction{nmTx(ledger.PlatformDegiro, ledger.TypeBuy, "X", "2024-03-15", "10", "85")}
	if got := NearMatches(nil, imported, nil); len(got) != 0 {
		t.Errorf("warnings = %q, want none", got)
	}
}

func TestNearMatches_ImportedManualRowsAreNotCompared(t *testing.T) {
	// Re-importing nothing but manual-platform rows (never a real
	// import) must not warn about itself.
	manual := []ledger.Transaction{nmTx(ledger.PlatformManual, ledger.TypeBuy, "X", "2024-03-15", "10", "85")}
	if got := NearMatches(manual, manual, nil); len(got) != 0 {
		t.Errorf("warnings = %q, want none: a manual row is not an import", got)
	}
}
