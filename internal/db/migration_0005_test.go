package db

import (
	"path/filepath"
	"testing"

	"database/sql"

	_ "modernc.org/sqlite"
)

func TestMigrations_0005_InstrumentTickers_UpDownUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO instrument_tickers (instrument, symbol, created_at) VALUES ('X','Y','Z')`); err != nil {
		t.Fatalf("insert into instrument_tickers: %v", err)
	}
	// Down reverses one migration at a time; step back past 0005 (and
	// every migration newer than it) so this test stays correct as
	// later migrations are added.
	if err := downTo(conn, 4); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='instrument_tickers'`).Scan(&n); err != nil {
		t.Fatalf("check: %v", err)
	}
	if n != 0 {
		t.Errorf("instrument_tickers still present after Down: count=%d", n)
	}
	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
