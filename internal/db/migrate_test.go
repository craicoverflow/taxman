package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// fakeMigrations builds an in-memory fs.FS with two migrations, so
// migration-runner behavior can be tested independent of whatever
// real migrations later live under internal/db/migrations.
func fakeMigrations() fstest.MapFS {
	return fstest.MapFS{
		"migrations/0001_create_widgets.up.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL);`),
		},
		"migrations/0001_create_widgets.down.sql": &fstest.MapFile{
			Data: []byte(`DROP TABLE widgets;`),
		},
		"migrations/0002_add_widget_color.up.sql": &fstest.MapFile{
			Data: []byte(`ALTER TABLE widgets ADD COLUMN color TEXT;`),
		},
		"migrations/0002_add_widget_color.down.sql": &fstest.MapFile{
			Data: []byte(`ALTER TABLE widgets DROP COLUMN color;`),
		},
	}
}

func tableExists(t *testing.T, conn *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("checking table %q: %v", name, err)
	}
	return true
}

// downTo reverses migrations one at a time until the highest applied
// version is target or lower. It lets a per-migration test peel back
// exactly its own migration without breaking when newer ones land on
// top.
func downTo(conn *sql.DB, target int) error {
	for {
		applied, err := appliedVersionSet(conn)
		if err != nil {
			return err
		}
		highest := 0
		for v := range applied {
			if v > highest {
				highest = v
			}
		}
		if highest <= target {
			return nil
		}
		if err := Down(conn, Migrations); err != nil {
			return err
		}
	}
}

func appliedVersions(t *testing.T, conn *sql.DB) []int {
	t.Helper()
	rows, err := conn.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		versions = append(versions, v)
	}
	return versions
}

func TestUp_CreatesSchemaMigrationsTable(t *testing.T) {
	conn := openTestDB(t)
	if err := Up(conn, fakeMigrations()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !tableExists(t, conn, "schema_migrations") {
		t.Error("expected schema_migrations table to exist after Up")
	}
}

func TestUp_AppliesAllMigrationsInOrder(t *testing.T) {
	conn := openTestDB(t)
	if err := Up(conn, fakeMigrations()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if !tableExists(t, conn, "widgets") {
		t.Fatal("expected widgets table to exist after Up")
	}

	got := appliedVersions(t, conn)
	want := []int{1, 2}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("applied versions = %v, want %v", got, want)
	}

	// 0002 added a `color` column - confirm it's really there by
	// inserting a row that uses it.
	if _, err := conn.Exec(`INSERT INTO widgets (name, color) VALUES ('gadget', 'red')`); err != nil {
		t.Errorf("insert using color column: %v", err)
	}
}

func TestUp_IsIdempotent(t *testing.T) {
	conn := openTestDB(t)
	if err := Up(conn, fakeMigrations()); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if err := Up(conn, fakeMigrations()); err != nil {
		t.Fatalf("second Up should be a no-op, got error: %v", err)
	}

	got := appliedVersions(t, conn)
	if len(got) != 2 {
		t.Errorf("expected exactly 2 applied versions after running Up twice, got %v", got)
	}
}

func TestDown_ReversesMostRecentMigration(t *testing.T) {
	conn := openTestDB(t)
	if err := Up(conn, fakeMigrations()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if err := Down(conn, fakeMigrations()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	// 0002's down drops the color column - the table should still
	// exist (0001 not reversed) but without color.
	if !tableExists(t, conn, "widgets") {
		t.Fatal("expected widgets table to still exist after reversing only migration 0002")
	}
	if _, err := conn.Exec(`INSERT INTO widgets (name, color) VALUES ('gadget', 'red')`); err == nil {
		t.Error("expected inserting into dropped color column to fail after Down")
	}

	got := appliedVersions(t, conn)
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("applied versions after one Down = %v, want [1]", got)
	}
}

func TestDown_OnEmptyDB_ReturnsNoError(t *testing.T) {
	conn := openTestDB(t)
	if err := Up(conn, fakeMigrations()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := Down(conn, fakeMigrations()); err != nil {
		t.Fatalf("first Down: %v", err)
	}
	if err := Down(conn, fakeMigrations()); err != nil {
		t.Fatalf("second Down: %v", err)
	}

	// Fully reversed: neither migration applied, widgets table gone.
	if tableExists(t, conn, "widgets") {
		t.Error("expected widgets table to be gone after reversing all migrations")
	}
	got := appliedVersions(t, conn)
	if len(got) != 0 {
		t.Errorf("expected no applied versions, got %v", got)
	}

	// A third Down with nothing left to reverse must not error.
	if err := Down(conn, fakeMigrations()); err != nil {
		t.Fatalf("Down with nothing to reverse should be a no-op, got error: %v", err)
	}
}

func TestUp_RoundTripsAcrossReconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roundtrip.db")

	conn1, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Up(conn1, fakeMigrations()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := conn1.Close(); err != nil {
		t.Fatalf("closing conn1: %v", err)
	}

	conn2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	if !tableExists(t, conn2, "widgets") {
		t.Error("expected widgets table to persist across reconnect")
	}
	got := appliedVersions(t, conn2)
	if len(got) != 2 {
		t.Errorf("expected 2 applied versions to persist across reconnect, got %v", got)
	}
}
