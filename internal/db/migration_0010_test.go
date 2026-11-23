package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrations_0010_PriceHistory_UpDownUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO price_history (symbol, currency, closes, fetched_at) VALUES ('AAPL','USD','[["2024-01-05","181.18"]]','2024-01-05T22:00:00Z')`,
	); err != nil {
		t.Fatalf("insert into price_history: %v", err)
	}

	if err := downTo(conn, 9); err != nil {
		t.Fatalf("down: %v", err)
	}
	if tableExists(t, conn, "price_history") {
		t.Errorf("price_history still present after Down")
	}
	if !tableExists(t, conn, "price_quotes") {
		t.Errorf("price_quotes dropped by 0010's Down")
	}

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	if !tableExists(t, conn, "price_history") {
		t.Errorf("price_history missing after re-Up")
	}
}
