package degiro

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// accountHeader is Degiro's "Account" (cash-ledger) export column
// layout — distinct from the "Transactions" export handled by
// parser.go. Buy/sell trades appear here as free-text rows in
// Description (e.g. "Buy 1 Widget Corp@10 EUR (IE0000000001)") rather
// than dedicated Quantity/Price columns. See account.go.
const accountHeader = "Date,Time,Value date,Product,ISIN,Description,FX,Change,,Balance,,Order Id\n"

func TestParseAccount_BuyRow(t *testing.T) {
	csv := accountHeader +
		"15-03-2024,10:32,15-03-2024,WIDGET CORP,IE0000000001,Buy 10 Widget Corp@85.32 EUR (IE0000000001),,EUR,-853.20,EUR,100.00,abc-123\n"

	txs, warnings, err := ParseAccount(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseAccount: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}

	tx := txs[0]
	if tx.Platform != ledger.PlatformDegiro {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformDegiro)
	}
	if tx.Type != ledger.TypeBuy {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeBuy)
	}
	if tx.Instrument != "IE0000000001" {
		t.Errorf("Instrument = %q, want ISIN", tx.Instrument)
	}
	wantDate := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	if !tx.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", tx.Date, wantDate)
	}
	if !tx.Quantity.Equal(decimal.NewFromInt(10)) {
		t.Errorf("Quantity = %s, want 10", tx.Quantity)
	}
	if !tx.Price.Equal(decimal.RequireFromString("85.32")) {
		t.Errorf("Price = %s, want 85.32 (|Change| / Quantity)", tx.Price)
	}
	if tx.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", tx.Currency)
	}
	if tx.Description != "WIDGET CORP" {
		t.Errorf("Description = %q, want the Product column's value", tx.Description)
	}
	if tx.SourceRef != "abc-123" {
		t.Errorf("SourceRef = %q, want abc-123", tx.SourceRef)
	}
}

func TestParseAccount_SellRow(t *testing.T) {
	csv := accountHeader +
		"20-04-2024,14:00,20-04-2024,WIDGET CORP,IE0000000001,Sell 5 Widget Corp@90 EUR (IE0000000001),,EUR,450.00,EUR,550.00,xyz-456\n"

	txs, _, err := ParseAccount(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseAccount: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}
	if txs[0].Type != ledger.TypeSell {
		t.Errorf("Type = %q, want %q", txs[0].Type, ledger.TypeSell)
	}
	if !txs[0].Quantity.Equal(decimal.NewFromInt(5)) {
		t.Errorf("Quantity = %s, want 5", txs[0].Quantity)
	}
}

func TestParseAccount_TradeInForeignCurrency_KeepsItsCurrency(t *testing.T) {
	// No FX conversion is performed at parse time — the transaction
	// is stored in whatever currency it actually settled in. The
	// engine already refuses non-EUR transactions explicitly rather
	// than guessing at a rate (see internal/engine/cgt.go); that's
	// out of scope here.
	csv := accountHeader +
		"23-04-2025,15:35,23-04-2025,WIDGET CORP,IE0000000001,Buy 1 Widget Corp@577 USD (IE0000000001),,USD,-577.00,USD,-577.00,order-1\n"

	txs, _, err := ParseAccount(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseAccount: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}
	if txs[0].Currency != "USD" {
		t.Errorf("Currency = %q, want USD", txs[0].Currency)
	}
}

func TestParseAccount_NonTradeRows_AreSkippedNotStored(t *testing.T) {
	csv := accountHeader +
		"10-08-2024,12:06,31-07-2024,,,Interest,,EUR,-0.05,EUR,-7.96,\n" +
		"29-06-2024,10:59,26-06-2024,WIDGET CORP,IE0000000001,Dividend,,USD,0.75,USD,0.52,\n" +
		"29-06-2024,10:59,26-06-2024,WIDGET CORP,IE0000000001,Dividend Tax,,USD,-0.23,USD,-0.23,\n" +
		"23-02-2024,10:43,31-01-2024,,,DEGIRO Exchange Connection Fee 2024 (London Stock Exchange (LSE) - LSE),,EUR,-2.50,EUR,-5.62,\n" +
		"14-04-2024,23:59,14-04-2024,,,Deposit,,EUR,1000.00,EUR,1014.75,\n"

	txs, warnings, err := ParseAccount(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseAccount: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("expected 0 transactions (all rows are non-trade activity), got %d", len(txs))
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for ordinary non-trade rows, got %v", warnings)
	}
}

func TestParseAccount_CorporateAction_SkippedWithWarning_DoesNotBlockImport(t *testing.T) {
	tests := []struct {
		name        string
		description string
	}{
		{"merger", "MERGER: Buy 9 Widget Corp@15.4993 EUR (IE0000000001)"},
		{"split adjustment", "SPLIT ADJUSTMENT: 30 Widget Corp @ 0.6748 EUR (IE0000000001)"},
		{"product change", "PRODUCT CHANGE: Buy 3 Widget Corp - Non tradeable@0.6748 EUR (IE0000000001)"},
		{"internal transfer", "INTERNAL TRANSFER: Buy 9 Widget Corp@15.4993 EUR (IE0000000001)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A corporate-action row alongside an ordinary trade row:
			// the corp-action row must not prevent the trade from
			// still being imported.
			csv := accountHeader +
				"28-02-2024,07:16,27-02-2024,WIDGET CORP,IE0000000001," + tt.description + ",,EUR,0.00,EUR,-6.87,\n" +
				"15-03-2024,10:32,15-03-2024,WIDGET CORP,IE0000000001,Buy 10 Widget Corp@85.32 EUR (IE0000000001),,EUR,-853.20,EUR,100.00,abc-123\n"

			txs, warnings, err := ParseAccount(strings.NewReader(csv))
			if err != nil {
				t.Fatalf("ParseAccount: %v (expected corp-action rows to warn, not fail)", err)
			}
			if len(txs) != 1 {
				t.Fatalf("expected the ordinary trade row to still be imported, got %d transactions", len(txs))
			}
			if len(warnings) != 1 {
				t.Fatalf("expected exactly 1 warning for the corporate-action row, got %d: %v", len(warnings), warnings)
			}
			if !strings.Contains(warnings[0], tt.description) {
				t.Errorf("expected warning to name the skipped row %q, got %q", tt.description, warnings[0])
			}
		})
	}
}

func TestParseAccount_UnrecognizedHeader_ReturnsExplicitError(t *testing.T) {
	_, _, err := ParseAccount(strings.NewReader("Totally,Different,Columns\nfoo,bar,baz\n"))
	if err == nil {
		t.Fatal("expected an error for an unrecognized header shape, got nil")
	}
}

func TestDetectAccount_RecognizesAccountHeader(t *testing.T) {
	fields := strings.Split(strings.TrimSuffix(accountHeader, "\n"), ",")
	if !DetectAccount(fields) {
		t.Error("expected DetectAccount to recognize the Degiro Account header shape")
	}
}

func TestDetect_RecognizesEitherDegiroLayout(t *testing.T) {
	txFields := strings.Split(strings.TrimSuffix(header, "\n"), ",")
	if !Detect(txFields) {
		t.Error("expected Detect to still recognize the Transactions header shape")
	}

	acctFields := strings.Split(strings.TrimSuffix(accountHeader, "\n"), ",")
	if !Detect(acctFields) {
		t.Error("expected Detect to also recognize the Account header shape")
	}
}

func TestParse_DispatchesToAccountParserForAccountHeader(t *testing.T) {
	csv := accountHeader +
		"15-03-2024,10:32,15-03-2024,WIDGET CORP,IE0000000001,Buy 10 Widget Corp@85.32 EUR (IE0000000001),,EUR,-853.20,EUR,100.00,abc-123\n"

	txs, _, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}
}
