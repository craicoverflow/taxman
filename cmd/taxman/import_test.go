package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/ledger"
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

const widgetBuyCSV = "Date,Time,Value date,Product,ISIN,Description,FX,Change,,Balance,,Order Id\n" +
	"15-03-2024,10:32,15-03-2024,WIDGET CORP,IE0000000001,Buy 10 Widget Corp@85.32 EUR (IE0000000001),,EUR,-853.20,EUR,100.00,abc-123\n"

// widgetImportWithManualBuy returns a DB holding a hand-entered buy
// that matches widgetBuyCSV, and the path of that CSV on disk.
func widgetImportWithManualBuy(t *testing.T) (dbPath, csvPath string) {
	t.Helper()
	dbPath = newTestDBPath(t)
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()
	d, _ := time.Parse("2006-01-02", "2024-03-15")
	if _, err := ledger.NewStore(conn).Insert(ledger.Transaction{
		Platform: ledger.PlatformManual, Type: ledger.TypeBuy, Date: d, Instrument: "IE0000000001",
		Quantity: decimal.NewFromInt(10), Price: decimal.NewFromInt(1), Currency: "EUR",
	}); err != nil {
		t.Fatalf("seeding manual buy: %v", err)
	}
	csvPath = filepath.Join(t.TempDir(), "account.csv")
	if err := os.WriteFile(csvPath, []byte(widgetBuyCSV), 0o600); err != nil {
		t.Fatalf("writing csv: %v", err)
	}
	return dbPath, csvPath
}

func TestRunImport_WarnsOfAManuallyEnteredDuplicateAndStillImports(t *testing.T) {
	dbPath, csvPath := widgetImportWithManualBuy(t)

	var err error
	out := captureStdout(t, func() { err = runImport([]string{csvPath, "--platform", "degiro", "--db", dbPath}) })
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	if !strings.Contains(out, "entered by hand") {
		t.Errorf("expected a near-match warning, got: %s", out)
	}
	if got := countTransactions(t, dbPath); got != 2 {
		t.Errorf("transactions = %d, want 2 (warning is advisory)", got)
	}
}

func TestRunImport_DryRun_ReportsTheNearMatchWithoutWriting(t *testing.T) {
	dbPath, csvPath := widgetImportWithManualBuy(t)

	var err error
	out := captureStdout(t, func() {
		err = runImport([]string{csvPath, "--platform", "degiro", "--db", dbPath, "--dry-run"})
	})
	if err != nil {
		t.Fatalf("runImport --dry-run: %v", err)
	}
	if !strings.Contains(out, "entered by hand") {
		t.Errorf("dry run should report the near-match, got: %s", out)
	}
	if got := countTransactions(t, dbPath); got != 1 {
		t.Errorf("dry run wrote rows: transactions = %d, want 1 (the manual trade only)", got)
	}
}

func TestRunImport_DryRun_NoDatabaseYet_StillWorks(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.db")
	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", missing, "--dry-run"}); err != nil {
		t.Fatalf("dry run against a missing db: %v", err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("dry run must not create the database file")
	}
}
