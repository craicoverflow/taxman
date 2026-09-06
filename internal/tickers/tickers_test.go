package tickers

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
)

func openMigratedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Up(conn, db.Migrations); err != nil {
		t.Fatalf("running migrations: %v", err)
	}
	return conn
}

func TestGet_NoMapping_ReturnsNotFound(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	symbol, ok, err := store.Get("IE00B4L5Y983")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok || symbol != "" {
		t.Errorf("Get(no mapping) = (%q, %v), want (\"\", false)", symbol, ok)
	}
}

func TestUpsert_ThenGet_ReturnsSymbol(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	if err := store.Upsert("US0378331005", "AAPL"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	symbol, ok, err := store.Get("US0378331005")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || symbol != "AAPL" {
		t.Errorf("Get after Upsert = (%q, %v), want (\"AAPL\", true)", symbol, ok)
	}
}

func TestUpsert_ExistingInstrument_OverwritesSymbol(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	if err := store.Upsert("IE00B3RBWM25", "VWRL.AS"); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	if err := store.Upsert("IE00B3RBWM25", "VWRL.L"); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	symbol, _, err := store.Get("IE00B3RBWM25")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if symbol != "VWRL.L" {
		t.Errorf("symbol after overwrite = %q, want %q", symbol, "VWRL.L")
	}
}

func TestAll_ReturnsEveryMapping(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	if err := store.Upsert("US0378331005", "AAPL"); err != nil {
		t.Fatalf("Upsert AAPL: %v", err)
	}
	if err := store.Upsert("IE00B3RBWM25", "VWRL.L"); err != nil {
		t.Fatalf("Upsert VWRL: %v", err)
	}

	all, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	want := map[string]string{"US0378331005": "AAPL", "IE00B3RBWM25": "VWRL.L"}
	if len(all) != len(want) {
		t.Fatalf("All() has %d entries, want %d: %v", len(all), len(want), all)
	}
	for k, v := range want {
		if all[k] != v {
			t.Errorf("All()[%q] = %q, want %q", k, all[k], v)
		}
	}
}
