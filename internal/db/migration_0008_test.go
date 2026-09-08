package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrations_0008_DeemedDisposalValuations_UpDownUp(t *testing.T) {
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
		`INSERT INTO deemed_disposal_valuations (instrument, valuation_date, value_per_unit, currency, created_at)
		 VALUES ('IE00B4L5Y983','2024-03-15T00:00:00Z','102.47','EUR','2026-09-07T10:00:00Z')`,
	); err != nil {
		t.Fatalf("insert into deemed_disposal_valuations: %v", err)
	}

	// One value per instrument per date: a second entry for the same
	// anniversary would leave the engine choosing between two
	// contradictory facts about one day.
	if _, err := conn.Exec(
		`INSERT INTO deemed_disposal_valuations (instrument, valuation_date, value_per_unit, currency, created_at)
		 VALUES ('IE00B4L5Y983','2024-03-15T00:00:00Z','99.00','EUR','2026-09-07T11:00:00Z')`,
	); err == nil {
		t.Error("expected a duplicate (instrument, valuation_date) to be rejected")
	}

	if err := downTo(conn, 7); err != nil {
		t.Fatalf("down: %v", err)
	}
	if tableExists(t, conn, "deemed_disposal_valuations") {
		t.Errorf("deemed_disposal_valuations still present after Down")
	}

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	if !tableExists(t, conn, "deemed_disposal_valuations") {
		t.Errorf("deemed_disposal_valuations missing after re-Up")
	}
}
