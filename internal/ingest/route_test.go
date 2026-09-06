package ingest

import (
	"os"
	"strings"
	"testing"
)

const degiroFixture = "../../testdata/fixtures/degiro/sample.csv"

func TestParseFile_AutoDetectsPlatform(t *testing.T) {
	platform, txs, _, err := ParseFile(degiroFixture, "")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if platform != "degiro" {
		t.Errorf("platform = %q, want %q", platform, "degiro")
	}
	if len(txs) != 3 {
		t.Errorf("len(txs) = %d, want 3", len(txs))
	}
}

func TestParseFile_ExplicitPlatform_SkipsDetection(t *testing.T) {
	platform, txs, _, err := ParseFile(degiroFixture, "degiro")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if platform != "degiro" {
		t.Errorf("platform = %q, want %q", platform, "degiro")
	}
	if len(txs) != 3 {
		t.Errorf("len(txs) = %d, want 3", len(txs))
	}
}

func TestParseReader_AutoDetectsPlatform(t *testing.T) {
	content, err := readFixture(degiroFixture)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	platform, txs, _, err := ParseReader(strings.NewReader(content), "upload.csv", "")
	if err != nil {
		t.Fatalf("ParseReader: %v", err)
	}
	if platform != "degiro" {
		t.Errorf("platform = %q, want %q", platform, "degiro")
	}
	if len(txs) != 3 {
		t.Errorf("len(txs) = %d, want 3", len(txs))
	}
}

func TestParseReader_UnrecognizedHeader_DoesNotGuess(t *testing.T) {
	_, _, _, err := ParseReader(strings.NewReader("Foo,Bar,Baz\n1,2,3\n"), "upload.csv", "")
	if err == nil {
		t.Fatal("expected an error for a header no detector recognizes")
	}
}

func TestParseReader_UnsupportedExplicitPlatform_ReturnsError(t *testing.T) {
	_, _, _, err := ParseReader(strings.NewReader("a,b\n1,2\n"), "upload.csv", "coinbase")
	if err == nil {
		t.Fatal("expected an error for an unsupported explicit platform")
	}
}

func TestParseFile_MissingFile_ReturnsError(t *testing.T) {
	_, _, _, err := ParseFile("/nonexistent/path.csv", "degiro")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file")
	}
}

func TestParseFile_AutoDetectsIBKRFlatExport(t *testing.T) {
	platform, txs, warnings, err := ParseFile("../../testdata/fixtures/ibkr/flat_sample.csv", "")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if platform != "ibkr" {
		t.Errorf("platform = %q, want %q", platform, "ibkr")
	}
	// 5 securities rows; the 2 blank-ISIN EUR.USD rows are skipped.
	if len(txs) != 5 {
		t.Errorf("len(txs) = %d, want 5", len(txs))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "skipped 2") {
		t.Errorf("expected a 'skipped 2 ...' cash-row warning, got %v", warnings)
	}
}

func TestParseReader_PassesThroughWarningsWithoutFailing(t *testing.T) {
	// A Degiro Account-export row taxman deliberately skips (a
	// corporate action) should surface as a warning through
	// ParseReader, not block the rest of the file — see
	// internal/ingest/degiro/account.go.
	csv := "Date,Time,Value date,Product,ISIN,Description,FX,Change,,Balance,,Order Id\n" +
		"28-02-2024,07:16,27-02-2024,WIDGET CORP,IE0000000001,MERGER: Buy 9 Widget Corp@15.4993 EUR (IE0000000001),,EUR,0.00,EUR,-6.87,\n" +
		"15-03-2024,10:32,15-03-2024,WIDGET CORP,IE0000000001,Buy 10 Widget Corp@85.32 EUR (IE0000000001),,EUR,-853.20,EUR,100.00,abc-123\n"

	_, txs, warnings, err := ParseReader(strings.NewReader(csv), "account.csv", "degiro")
	if err != nil {
		t.Fatalf("ParseReader: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected the ordinary trade row to still import, got %d transactions", len(txs))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for the corporate-action row, got %d: %v", len(warnings), warnings)
	}
}

func readFixture(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
