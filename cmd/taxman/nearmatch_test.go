package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// logByHand stores the trade the Degiro fixture's first row also
// records (10 units of IE00B4L5Y983 on 2024-03-15), as a hand-entered
// trade at a slightly different price.
func logByHand(t *testing.T, dbPath string) {
	t.Helper()
	d, err := ledger.NewStore(openTestConn(t, dbPath)).Insert(ledger.Transaction{
		Platform: ledger.PlatformManual, Type: ledger.TypeBuy,
		Date:       mustParseDate(t, "2024-03-15"),
		Instrument: "IE00B4L5Y983",
		Quantity:   decimal.NewFromInt(10), Price: decimal.RequireFromString("85"), Currency: "EUR",
	})
	if err != nil || !d {
		t.Fatalf("logging the manual trade: inserted=%v err=%v", d, err)
	}
}

func TestRunImport_WarnsWhenARowMatchesAHandEnteredTrade_AndStillImportsIt(t *testing.T) {
	dbPath := newTestDBPath(t)
	logByHand(t, dbPath)

	output := captureStdout(t, func() {
		if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
			t.Fatalf("runImport: %v", err)
		}
	})

	if !strings.Contains(output, "logged by hand") || !strings.Contains(output, "IE00B4L5Y983") {
		t.Errorf("expected a near-match warning, got: %s", output)
	}
	// 3 imported rows + the manual one: the warning is advisory only.
	if got := countTransactions(t, dbPath); got != 4 {
		t.Errorf("transactions = %d, want 4: a near-match must not skip or delete anything", got)
	}
}

func TestRunImport_NoManualTrades_NoNearMatchWarning(t *testing.T) {
	dbPath := newTestDBPath(t)
	output := captureStdout(t, func() {
		if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
			t.Fatalf("runImport: %v", err)
		}
	})
	if strings.Contains(output, "logged by hand") {
		t.Errorf("unexpected near-match warning: %s", output)
	}
}

func TestRunImport_DryRun_ReportsNearMatchesAndWritesNothing(t *testing.T) {
	dbPath := newTestDBPath(t)
	logByHand(t, dbPath)

	output := captureStdout(t, func() {
		if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath, "--dry-run"}); err != nil {
			t.Fatalf("runImport --dry-run: %v", err)
		}
	})

	if !strings.Contains(output, "logged by hand") {
		t.Errorf("expected --dry-run to report the near-match, got: %s", output)
	}
	if got := countTransactions(t, dbPath); got != 1 {
		t.Errorf("transactions = %d, want only the manual one after --dry-run", got)
	}
}

func TestRunImport_DryRun_WithNoDatabaseYet_StillWorksAndCreatesNothing(t *testing.T) {
	dbPath := t.TempDir() + "/never-created.db"
	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath, "--dry-run"}); err != nil {
		t.Fatalf("runImport --dry-run: %v", err)
	}
	if _, err := openStat(dbPath); err == nil {
		t.Errorf("--dry-run created %s", dbPath)
	}
}

func TestRunBackfill_WarnsOfNearMatches(t *testing.T) {
	dbPath := newTestDBPath(t)
	logByHand(t, dbPath)
	dir := backfillDir(t)

	output := captureStdout(t, func() {
		if err := runBackfill([]string{dir, "--db", dbPath}); err != nil {
			t.Fatalf("runBackfill: %v", err)
		}
	})
	if !strings.Contains(output, "logged by hand") {
		t.Errorf("expected backfill to report the near-match, got: %s", output)
	}
}

func mustParseDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

func openStat(path string) (os.FileInfo, error) { return os.Stat(path) }
