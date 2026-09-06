package main

import (
	"strings"
	"testing"

	"github.com/craicoverflow/taxman/internal/classify"
)

func TestRunClassify_Set_PersistsClassification(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runClassify([]string{"--set", "AAPL", "CGT_ASSET", "--db", dbPath}); err != nil {
		t.Fatalf("runClassify --set: %v", err)
	}

	conn := openTestConn(t, dbPath)
	got, err := classify.NewStore(conn).Classify("AAPL")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != classify.CGTAsset {
		t.Errorf("Classify(AAPL) = %q, want %q", got, classify.CGTAsset)
	}
}

func TestRunClassify_Set_ExitTaxFund(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runClassify([]string{"--set", "IE00B4L5Y983", "EXIT_TAX_FUND", "--db", dbPath}); err != nil {
		t.Fatalf("runClassify --set: %v", err)
	}

	conn := openTestConn(t, dbPath)
	got, err := classify.NewStore(conn).Classify("IE00B4L5Y983")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != classify.ExitTaxFund {
		t.Errorf("Classify = %q, want %q", got, classify.ExitTaxFund)
	}
}

func TestRunClassify_Set_InvalidClassification_ReturnsError(t *testing.T) {
	dbPath := newTestDBPath(t)

	err := runClassify([]string{"--set", "AAPL", "NOT_A_REAL_CLASSIFICATION", "--db", dbPath})
	if err == nil {
		t.Fatal("expected an error for an invalid classification value")
	}
}

func TestRunClassify_ListUnclassified_ReportsUnclassifiedHoldings(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("importing degiro fixture: %v", err)
	}

	output := captureStdout(t, func() {
		if err := runClassify([]string{"--list-unclassified", "--db", dbPath}); err != nil {
			t.Fatalf("runClassify --list-unclassified: %v", err)
		}
	})

	// degiro fixture holds IE00B4L5Y983 and IE00BK5BQT80, neither
	// classified yet.
	if !strings.Contains(output, "IE00B4L5Y983") {
		t.Errorf("expected IE00B4L5Y983 in unclassified output, got: %s", output)
	}
	if !strings.Contains(output, "IE00BK5BQT80") {
		t.Errorf("expected IE00BK5BQT80 in unclassified output, got: %s", output)
	}
}

func TestRunClassify_ListUnclassified_ExcludesClassifiedHoldings(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("importing degiro fixture: %v", err)
	}
	if err := runClassify([]string{"--set", "IE00B4L5Y983", "CGT_ASSET", "--db", dbPath}); err != nil {
		t.Fatalf("runClassify --set: %v", err)
	}

	output := captureStdout(t, func() {
		if err := runClassify([]string{"--list-unclassified", "--db", dbPath}); err != nil {
			t.Fatalf("runClassify --list-unclassified: %v", err)
		}
	})

	if strings.Contains(output, "IE00B4L5Y983") {
		t.Errorf("expected IE00B4L5Y983 to be excluded once classified, got: %s", output)
	}
	if !strings.Contains(output, "IE00BK5BQT80") {
		t.Errorf("expected IE00BK5BQT80 still listed as unclassified, got: %s", output)
	}
}

func TestRunClassify_NoFlags_ReturnsUsageError(t *testing.T) {
	err := runClassify(nil)
	if err == nil {
		t.Fatal("expected an error when neither --set nor --list-unclassified is given")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("expected usage text in error, got: %v", err)
	}
}
