package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
)

const degiroFixture = "../../testdata/fixtures/degiro/sample.csv"

func newTestDBPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := db.Up(conn, db.Migrations); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return path
}

func countTransactions(t *testing.T, dbPath string) int {
	t.Helper()
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("counting transactions: %v", err)
	}
	return count
}

func TestRunImport_Degiro_InsertsAllRows(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("runImport: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 3 {
		t.Errorf("expected 3 transactions stored, got %d", got)
	}
}

func TestRunImport_Rerun_IsIdempotent(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("first runImport: %v", err)
	}
	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("second runImport: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 3 {
		t.Errorf("expected 3 transactions after re-importing the identical file, got %d", got)
	}
}

func TestRunImport_DryRun_WritesNothing(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath, "--dry-run"}); err != nil {
		t.Fatalf("runImport --dry-run: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 0 {
		t.Errorf("expected 0 transactions after --dry-run, got %d", got)
	}
}

func TestRunImport_AutoDetectsPlatform(t *testing.T) {
	dbPath := newTestDBPath(t)

	// No --platform flag: must be inferred from the file's header.
	if err := runImport([]string{degiroFixture, "--db", dbPath}); err != nil {
		t.Fatalf("runImport without --platform: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 3 {
		t.Errorf("expected 3 transactions stored via auto-detection, got %d", got)
	}
}

func TestRunImport_AmbiguousFile_DoesNotGuess(t *testing.T) {
	dbPath := newTestDBPath(t)

	tmp := filepath.Join(t.TempDir(), "unrecognized.csv")
	if err := os.WriteFile(tmp, []byte("Foo,Bar,Baz\n1,2,3\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	err := runImport([]string{tmp, "--db", dbPath})
	if err == nil {
		t.Fatal("expected an error for a file whose platform cannot be auto-detected")
	}
}

func TestRunImport_MissingFile_ReturnsError(t *testing.T) {
	dbPath := newTestDBPath(t)

	err := runImport([]string{"/nonexistent/path.csv", "--platform", "degiro", "--db", dbPath})
	if err == nil {
		t.Fatal("expected an error for a nonexistent file")
	}
}

func TestRunImport_NoFileArgument_ReturnsUsageError(t *testing.T) {
	err := runImport(nil)
	if err == nil {
		t.Fatal("expected an error when no file is given")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("expected usage text in error, got: %v", err)
	}
}
