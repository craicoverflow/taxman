package main

import "testing"

const etradeFixture = "../../testdata/fixtures/etrade/sample.csv"

func TestRunImport_ETRADE_InsertsAllRows(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{etradeFixture, "--platform", "etrade", "--db", dbPath}); err != nil {
		t.Fatalf("runImport: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 2 {
		t.Errorf("expected 2 transactions stored, got %d", got)
	}
}

func TestRunImport_ETRADE_AutoDetectsPlatform(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{etradeFixture, "--db", dbPath}); err != nil {
		t.Fatalf("runImport without --platform: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 2 {
		t.Errorf("expected 2 transactions stored via auto-detection, got %d", got)
	}
}

func TestRunImport_ETRADE_Rerun_IsIdempotent(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{etradeFixture, "--platform", "etrade", "--db", dbPath}); err != nil {
		t.Fatalf("first runImport: %v", err)
	}
	if err := runImport([]string{etradeFixture, "--platform", "etrade", "--db", dbPath}); err != nil {
		t.Fatalf("second runImport: %v", err)
	}

	if got := countTransactions(t, dbPath); got != 2 {
		t.Errorf("expected 2 transactions after re-importing the identical file, got %d", got)
	}
}
