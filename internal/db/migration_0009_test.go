package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrations_0009_InstrumentAliases_UpDownUp(t *testing.T) {
	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO instrument_aliases (alias, canonical, created_at) VALUES ('SXR8','IE00B5BMR087','2026-10-13T10:00:00Z')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO instrument_aliases (alias, canonical, created_at) VALUES ('X','X','2026-10-13T10:00:00Z')`); err == nil {
		t.Error("expected a self-alias to be rejected")
	}
	if err := downTo(conn, 8); err != nil {
		t.Fatalf("down: %v", err)
	}
	if tableExists(t, conn, "instrument_aliases") {
		t.Error("instrument_aliases still present after Down")
	}
	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
