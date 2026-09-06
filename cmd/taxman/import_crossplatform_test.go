package main

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const ibkrFixture = "../../testdata/fixtures/ibkr/sample.csv"

// TestRunImport_DegiroAndIBKR_OverlappingISIN_DedupsCorrectly is the
// integration test SPEC.md §5 and tasks/plan.md task 6.1 explicitly
// name: "Degiro + IBKR overlapping the same ISIN". Both fixtures hold
// IE00B4L5Y983 (see testdata/fixtures/degiro/sample.csv and
// testdata/fixtures/ibkr/sample.csv). "Dedups correctly" here means
// two things at once, both asserted below:
//  1. legitimately distinct transactions on the SAME ISIN from
//     DIFFERENT platforms must NOT be collapsed into one — they're
//     separate holdings on separate brokers, and
//     ledger.Transaction.Fingerprint() includes Platform specifically
//     to guarantee this;
//  2. re-importing either file a second time still dedupes its own
//     rows correctly, unaffected by the other platform's data for the
//     same ISIN being present.
func TestRunImport_DegiroAndIBKR_OverlappingISIN_DedupsCorrectly(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("importing degiro fixture: %v", err)
	}
	if err := runImport([]string{ibkrFixture, "--platform", "ibkr", "--db", dbPath}); err != nil {
		t.Fatalf("importing ibkr fixture: %v", err)
	}

	// degiro fixture: 3 rows, ibkr fixture: 2 rows -> 5 total, none
	// collapsed against each other despite IE00B4L5Y983 appearing in
	// both.
	if got := countTransactions(t, dbPath); got != 5 {
		t.Fatalf("expected 5 transactions (3 degiro + 2 ibkr, no cross-platform collision on IE00B4L5Y983), got %d", got)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var degiroCount, ibkrCount int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions WHERE platform = 'degiro'`).Scan(&degiroCount); err != nil {
		t.Fatalf("counting degiro rows: %v", err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM transactions WHERE platform = 'ibkr'`).Scan(&ibkrCount); err != nil {
		t.Fatalf("counting ibkr rows: %v", err)
	}
	if degiroCount != 3 {
		t.Errorf("degiro transaction count = %d, want 3", degiroCount)
	}
	if ibkrCount != 2 {
		t.Errorf("ibkr transaction count = %d, want 2", ibkrCount)
	}

	// Re-importing both, in either order, must still be a true no-op
	// per-platform — the presence of the other platform's rows on the
	// same ISIN must not interfere with intra-platform dedup.
	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("re-importing degiro fixture: %v", err)
	}
	if err := runImport([]string{ibkrFixture, "--platform", "ibkr", "--db", dbPath}); err != nil {
		t.Fatalf("re-importing ibkr fixture: %v", err)
	}
	if got := countTransactions(t, dbPath); got != 5 {
		t.Errorf("expected still 5 transactions after re-importing both fixtures, got %d", got)
	}
}

func TestRunImport_IBKR_AutoDetectsPlatform(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{ibkrFixture, "--db", dbPath}); err != nil {
		t.Fatalf("runImport without --platform: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 2 {
		t.Errorf("expected 2 transactions stored via auto-detection, got %d", got)
	}
}
