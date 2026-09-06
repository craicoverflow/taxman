package etrade

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// header is the column layout this parser expects: a single unified
// transaction export covering both RSU vests and sales. This is a v1
// assumption — ETRADE's real exports vary by report type (Benefit
// History vs. G&L vs. Transactions) and this has NOT been checked
// against a real export, same caveat as Degiro's and IBKR's parsers
// (see tasks/plan.md CHECKPOINT 3, which applies to every ingest
// parser built before real export samples are reviewed).
const header = "TransactionType,Date,Symbol,Quantity,PricePerShare,Currency,Reference\n"

func TestParse_VestRow(t *testing.T) {
	csv := header + "Vest,2024-01-15,ETRADE_CO,20,40.00,EUR,vest-001\n"

	txs, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}

	tx := txs[0]
	if tx.Platform != ledger.PlatformETRADE {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformETRADE)
	}
	if tx.Type != ledger.TypeRSUVest {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeRSUVest)
	}
	wantDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	if !tx.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", tx.Date, wantDate)
	}
	if !tx.Quantity.Equal(decimal.NewFromInt(20)) {
		t.Errorf("Quantity = %s, want 20", tx.Quantity)
	}
	// Price carries the vest-date FMV — internal/engine's ComputeCGT
	// (task 4.3) relies on this being the fair market value, not a
	// purchase price.
	if !tx.Price.Equal(decimal.RequireFromString("40.00")) {
		t.Errorf("Price (vest-date FMV) = %s, want 40.00", tx.Price)
	}
	if tx.SourceRef != "vest-001" {
		t.Errorf("SourceRef = %q, want vest-001", tx.SourceRef)
	}
}

func TestParse_SellRow(t *testing.T) {
	csv := header + "Sell,2024-08-01,ETRADE_CO,20,60.00,EUR,sell-001\n"

	txs, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}

	tx := txs[0]
	if tx.Type != ledger.TypeSell {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeSell)
	}
	if !tx.Quantity.Equal(decimal.NewFromInt(20)) {
		t.Errorf("Quantity = %s, want 20", tx.Quantity)
	}
}

func TestParse_VestThenSell_BothRowsParsed(t *testing.T) {
	csv := header +
		"Vest,2024-01-15,ETRADE_CO,20,40.00,EUR,vest-001\n" +
		"Sell,2024-08-01,ETRADE_CO,20,60.00,EUR,sell-001\n"

	txs, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txs))
	}
	if txs[0].Type != ledger.TypeRSUVest {
		t.Errorf("txs[0].Type = %q, want %q", txs[0].Type, ledger.TypeRSUVest)
	}
	if txs[1].Type != ledger.TypeSell {
		t.Errorf("txs[1].Type = %q, want %q", txs[1].Type, ledger.TypeSell)
	}
}

func TestParse_UnrecognizedTransactionType_ReturnsExplicitError(t *testing.T) {
	csv := header + "Transfer,2024-08-01,ETRADE_CO,20,60.00,EUR,xfer-001\n"

	_, err := Parse(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected an error for an unrecognized transaction type, got nil")
	}
}

func TestParse_MalformedRow_ReturnsExplicitError(t *testing.T) {
	csv := header + "Sell,2024-08-01,ETRADE_CO,not-a-number,60.00,EUR,sell-001\n"

	_, err := Parse(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected an error for a malformed quantity, got nil")
	}
}

func TestParse_UnrecognizedHeader_ReturnsExplicitError(t *testing.T) {
	csv := "Totally,Different,Columns\nfoo,bar,baz\n"

	_, err := Parse(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected an error for an unrecognized header shape, got nil")
	}
}

func TestParse_EmptyFile_ReturnsExplicitError(t *testing.T) {
	_, err := Parse(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected an error for an empty file, got nil")
	}
}

func TestDetect_RecognizesETRADEHeader(t *testing.T) {
	fields := strings.Split(strings.TrimSuffix(header, "\n"), ",")
	if !Detect(fields) {
		t.Error("expected Detect to recognize the ETRADE header shape")
	}
}

func TestDetect_RejectsUnrelatedHeader(t *testing.T) {
	if Detect([]string{"Totally", "Different", "Columns"}) {
		t.Error("expected Detect to reject an unrelated header shape")
	}
}
