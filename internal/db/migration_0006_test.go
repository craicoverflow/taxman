package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrations_0006_PriceQuotes_UpDownUp(t *testing.T) {
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
		`INSERT INTO price_quotes (symbol, price, currency, fetched_at) VALUES ('AAPL','195.89','USD','2024-01-05T22:00:00Z')`,
	); err != nil {
		t.Fatalf("insert into price_quotes: %v", err)
	}

	if err := downTo(conn, 5); err != nil {
		t.Fatalf("down: %v", err)
	}
	if tableExists(t, conn, "price_quotes") {
		t.Errorf("price_quotes still present after Down")
	}

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	if !tableExists(t, conn, "price_quotes") {
		t.Errorf("price_quotes missing after re-Up")
	}
}
