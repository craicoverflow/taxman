package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunReport_Text_CGTHolding(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("importing degiro fixture: %v", err)
	}
	// degiro fixture holds IE00B4L5Y983 with a buy(10) then a sell(5)
	// in 2024 — classify it as a CGT asset so runReport can compute a
	// real disposal, exercising the full ledger->classify->engine->
	// audit chain rather than just an unclassified-holding path.
	if err := runClassify([]string{"--set", "IE00B4L5Y983", "CGT_ASSET", "--db", dbPath}); err != nil {
		t.Fatalf("classifying: %v", err)
	}

	output := captureStdout(t, func() {
		if err := runReport([]string{"--year", "2024", "--format", "text", "--db", dbPath}); err != nil {
			t.Fatalf("runReport: %v", err)
		}
	})

	if !strings.Contains(output, "IE00B4L5Y983") {
		t.Errorf("expected the classified holding in the report output, got: %s", output)
	}
	if !strings.Contains(strings.ToLower(output), "cgt") {
		t.Errorf("expected a CGT liability section in the report output, got: %s", output)
	}
}

func TestRunReport_JSON_IsValidAndContainsHolding(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("importing degiro fixture: %v", err)
	}
	if err := runClassify([]string{"--set", "IE00B4L5Y983", "CGT_ASSET", "--db", dbPath}); err != nil {
		t.Fatalf("classifying: %v", err)
	}

	output := captureStdout(t, func() {
		if err := runReport([]string{"--year", "2024", "--format", "json", "--db", dbPath}); err != nil {
			t.Fatalf("runReport: %v", err)
		}
	})

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}
	if !strings.Contains(output, "IE00B4L5Y983") {
		t.Errorf("expected the classified holding in the JSON report, got: %s", output)
	}
}

func TestRunReport_UnclassifiedHolding_FlaggedNotSilentlySkipped(t *testing.T) {
	dbPath := newTestDBPath(t)

	if err := runImport([]string{degiroFixture, "--platform", "degiro", "--db", dbPath}); err != nil {
		t.Fatalf("importing degiro fixture: %v", err)
	}
	// Deliberately leave both degiro holdings unclassified.

	output := captureStdout(t, func() {
		if err := runReport([]string{"--year", "2024", "--format", "text", "--db", dbPath}); err != nil {
			t.Fatalf("runReport: %v", err)
		}
	})

	if !strings.Contains(strings.ToLower(output), "unclassified") {
		t.Errorf("expected unclassified holdings to be visibly flagged in the report, got: %s", output)
	}
}

func TestRunReport_UnsupportedFormat_ReturnsError(t *testing.T) {
	dbPath := newTestDBPath(t)

	err := runReport([]string{"--year", "2024", "--format", "pdf", "--db", dbPath})
	if err == nil {
		t.Fatal("expected an error for the unsupported pdf format (deferred per the user's answer)")
	}
}

func TestRunReport_MissingYear_ReturnsUsageError(t *testing.T) {
	err := runReport([]string{"--db", newTestDBPath(t)})
	if err == nil {
		t.Fatal("expected an error when --year is not given")
	}
	if !strings.Contains(err.Error(), "usage:") && !strings.Contains(err.Error(), "--year") {
		t.Errorf("expected a usage or --year-related error, got: %v", err)
	}
}

func TestRunReport_InvalidYear_ReturnsError(t *testing.T) {
	err := runReport([]string{"--year", "not-a-year", "--db", newTestDBPath(t)})
	if err == nil {
		t.Fatal("expected an error for a non-numeric --year")
	}
}
