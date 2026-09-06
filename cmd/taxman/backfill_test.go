package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// backfillDir builds a temp directory containing copies of the
// Degiro, IBKR, and ETRADE synthetic fixtures — a mixed-platform
// directory, per SPEC.md §3's description of `taxman backfill`.
func backfillDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	copyFixture := func(src, destName string) {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("reading fixture %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(dir, destName), data, 0o600); err != nil {
			t.Fatalf("writing fixture copy %s: %v", destName, err)
		}
	}
	copyFixture(degiroFixture, "degiro-export.csv")
	copyFixture(ibkrFixture, "ibkr-export.csv")
	copyFixture(etradeFixture, "etrade-export.csv")

	return dir
}

func TestRunBackfill_ImportsEveryFileInDirectory(t *testing.T) {
	dbPath := newTestDBPath(t)
	dir := backfillDir(t)

	if err := runBackfill([]string{dir, "--db", dbPath}); err != nil {
		t.Fatalf("runBackfill: %v", err)
	}

	// degiro 3 + ibkr 2 + etrade 2 = 7
	if got := countTransactions(t, dbPath); got != 7 {
		t.Errorf("expected 7 transactions across all backfilled files, got %d", got)
	}
}

func TestRunBackfill_Rerun_IsIdempotent(t *testing.T) {
	dbPath := newTestDBPath(t)
	dir := backfillDir(t)

	if err := runBackfill([]string{dir, "--db", dbPath}); err != nil {
		t.Fatalf("first runBackfill: %v", err)
	}
	if err := runBackfill([]string{dir, "--db", dbPath}); err != nil {
		t.Fatalf("second runBackfill: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 7 {
		t.Errorf("expected still 7 transactions after re-running backfill, got %d", got)
	}
}

func TestRunBackfill_UnrecognizedFile_ReturnsError(t *testing.T) {
	dbPath := newTestDBPath(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mystery.csv"), []byte("Foo,Bar\n1,2\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	err := runBackfill([]string{dir, "--db", dbPath})
	if err == nil {
		t.Fatal("expected an error for a file whose platform cannot be auto-detected")
	}
}

func TestRunBackfill_EmptyDirectory_NoError(t *testing.T) {
	dbPath := newTestDBPath(t)
	dir := t.TempDir()

	if err := runBackfill([]string{dir, "--db", dbPath}); err != nil {
		t.Fatalf("runBackfill on an empty directory: %v", err)
	}
	if got := countTransactions(t, dbPath); got != 0 {
		t.Errorf("expected 0 transactions for an empty directory, got %d", got)
	}
}

func TestRunBackfill_NonexistentDirectory_ReturnsError(t *testing.T) {
	dbPath := newTestDBPath(t)

	err := runBackfill([]string{"/nonexistent/directory", "--db", dbPath})
	if err == nil {
		t.Fatal("expected an error for a nonexistent directory")
	}
}

func TestRunBackfill_NoDirArgument_ReturnsUsageError(t *testing.T) {
	err := runBackfill(nil)
	if err == nil {
		t.Fatal("expected an error when no directory is given")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("expected usage text in error, got: %v", err)
	}
}

func TestRunBackfill_LogsDistinctlyFromImport(t *testing.T) {
	dbPath := newTestDBPath(t)
	dir := backfillDir(t)

	output := captureStdout(t, func() {
		if err := runBackfill([]string{dir, "--db", dbPath}); err != nil {
			t.Fatalf("runBackfill: %v", err)
		}
	})

	// SPEC.md §3: "logged distinctly from import" — the operational
	// difference between "loading history" and "adding this month's
	// export" should be visible in the log output, not just in which
	// command was typed.
	if !strings.Contains(output, "backfill") {
		t.Errorf("expected backfill's output to be labeled distinctly from a plain import, got: %s", output)
	}
}
