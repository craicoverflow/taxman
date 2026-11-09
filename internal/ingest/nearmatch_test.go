package ingest

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/aliases"
	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/ledger"
)

func nmTx(t *testing.T, platform ledger.Platform, typ ledger.Type, instrument, date, qty, price string) ledger.Transaction {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("date %q: %v", date, err)
	}
	return ledger.Transaction{
		Platform: platform, Type: typ, Date: d, Instrument: instrument,
		Quantity: decimal.RequireFromString(qty), Price: decimal.RequireFromString(price), Currency: "EUR",
	}
}

func TestNearMatches(t *testing.T) {
	manual := []ledger.Transaction{
		nmTx(t, ledger.PlatformManual, ledger.TypeBuy, "AAPL", "2024-03-15", "10", "85.32"),
	}
	tests := []struct {
		name   string
		parsed ledger.Transaction
		want   int
	}{
		{"same trade", nmTx(t, ledger.PlatformDegiro, ledger.TypeBuy, "AAPL", "2024-03-15", "10", "85.32"), 1},
		{"price differs, still matches", nmTx(t, ledger.PlatformDegiro, ledger.TypeBuy, "AAPL", "2024-03-15", "10", "99"), 1},
		{"quantity differs", nmTx(t, ledger.PlatformDegiro, ledger.TypeBuy, "AAPL", "2024-03-15", "11", "85.32"), 0},
		{"date differs", nmTx(t, ledger.PlatformDegiro, ledger.TypeBuy, "AAPL", "2024-03-16", "10", "85.32"), 0},
		{"type differs", nmTx(t, ledger.PlatformDegiro, ledger.TypeSell, "AAPL", "2024-03-15", "10", "85.32"), 0},
		{"instrument differs", nmTx(t, ledger.PlatformDegiro, ledger.TypeBuy, "MSFT", "2024-03-15", "10", "85.32"), 0},
		{"interest is not a trade", nmTx(t, ledger.PlatformDegiro, ledger.TypeInterest, "AAPL", "2024-03-15", "10", "1"), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NearMatches(manual, []ledger.Transaction{tt.parsed})
			if len(got) != tt.want {
				t.Fatalf("got %d warnings %v, want %d", len(got), got, tt.want)
			}
			if tt.want == 1 {
				for _, frag := range []string{"AAPL", "2024-03-15", "hand", "delete"} {
					if !strings.Contains(got[0], frag) {
						t.Errorf("warning %q should mention %q", got[0], frag)
					}
				}
			}
		})
	}
}

func TestNearMatches_NoManualTrades_NoWarnings(t *testing.T) {
	parsed := []ledger.Transaction{nmTx(t, ledger.PlatformDegiro, ledger.TypeBuy, "AAPL", "2024-03-15", "10", "1")}
	if got := NearMatches(nil, parsed); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func openNearMatchDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "nm.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Up(conn, db.Migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

func TestNearMatchWarnings_ResolvesAliasesOnBothSides(t *testing.T) {
	conn := openNearMatchDB(t)
	// The manual trade was typed against the ticker; the import carries
	// the ISIN; the user has merged the ISIN into the ticker.
	if _, err := ledger.NewStore(conn).Insert(nmTx(t, ledger.PlatformManual, ledger.TypeBuy, "SXR8", "2024-03-15", "10", "5")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := aliases.NewStore(conn).Merge("IE00B5BMR087", "SXR8"); err != nil {
		t.Fatalf("alias: %v", err)
	}

	parsed := []ledger.Transaction{nmTx(t, ledger.PlatformDegiro, ledger.TypeBuy, "IE00B5BMR087", "2024-03-15", "10", "5")}
	got, err := NearMatchWarnings(conn, parsed)
	if err != nil {
		t.Fatalf("NearMatchWarnings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v, want one warning through the alias", got)
	}
}

func TestNearMatchWarnings_NothingManual_NoWarnings(t *testing.T) {
	conn := openNearMatchDB(t)
	parsed := []ledger.Transaction{nmTx(t, ledger.PlatformDegiro, ledger.TypeBuy, "AAPL", "2024-03-15", "10", "5")}
	got, err := NearMatchWarnings(conn, parsed)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want no warnings, no error", got, err)
	}
}
