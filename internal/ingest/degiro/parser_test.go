package degiro

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// header is the column layout of Degiro's English-locale
// "Transactions" export, confirmed against a real export (see
// parser.go's wantHeader and tasks/plan.md CHECKPOINT 3). Indices 8
// and 10 are blank in Degiro's own header row — they carry the
// currency for the adjacent Price / Local value amounts.
const header = "Date,Time,Product,ISIN,Reference exchange,Venue,Quantity,Price,,Local value,,Value EUR,Exchange rate,AutoFX Fee,Transaction and/or third party fees EUR,Total EUR,Order ID\n"

func TestParse_BuyRow(t *testing.T) {
	csv := header + "15-03-2024,10:32,ISHARES CORE MSCI WORLD,IE00B4L5Y983,EAM,XAMS,10,85.32,EUR,-853.20,EUR,-853.20,,0.00,-1.00,-854.20,abc-123\n"

	txs, _, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}

	tx := txs[0]
	if tx.Platform != ledger.PlatformDegiro {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformDegiro)
	}
	if tx.Type != ledger.TypeBuy {
		t.Errorf("Type = %q, want %q (positive quantity)", tx.Type, ledger.TypeBuy)
	}
	if tx.Instrument != "IE00B4L5Y983" {
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
		t.Errorf("Price = %s, want 85.32", tx.Price)
	}
	if tx.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", tx.Currency)
	}
	if tx.SourceRef != "abc-123" {
		t.Errorf("SourceRef = %q, want abc-123", tx.SourceRef)
	}
	if tx.Description != "ISHARES CORE MSCI WORLD" {
		t.Errorf("Description = %q, want the Product column's value", tx.Description)
	}
}

func TestParse_SellRow_NegativeQuantity(t *testing.T) {
	csv := header + "20-04-2024,14:00,ISHARES CORE MSCI WORLD,IE00B4L5Y983,EAM,XAMS,-5,90.00,EUR,450.00,EUR,450.00,,0.00,-1.00,449.00,xyz-456\n"

	txs, _, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}

	tx := txs[0]
	if tx.Type != ledger.TypeSell {
		t.Errorf("Type = %q, want %q (negative quantity)", tx.Type, ledger.TypeSell)
	}
	// Quantity is stored as a positive magnitude; direction is
	// carried by Type, not by sign.
	if !tx.Quantity.Equal(decimal.NewFromInt(5)) {
		t.Errorf("Quantity = %s, want 5 (positive magnitude)", tx.Quantity)
	}
}

func TestParse_MultipleRows(t *testing.T) {
	csv := header +
		"15-03-2024,10:32,ISHARES CORE MSCI WORLD,IE00B4L5Y983,EAM,XAMS,10,85.32,EUR,-853.20,EUR,-853.20,,0.00,-1.00,-854.20,abc-123\n" +
		"20-04-2024,14:00,VANGUARD FTSE ALL-WORLD,IE00BK5BQT80,EAM,XAMS,3,110.50,EUR,-331.50,EUR,-331.50,,0.00,-1.00,-332.50,def-789\n"

	txs, _, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txs))
	}
}

// TestParse_NetsMultiRowOrderByOrderID covers Degiro booking a single
// order as several rows that share one Order ID (provisional fills,
// venue corrections). They must collapse to one transaction whose
// quantity is the net and whose price is derived from the netted cash.
func TestParse_NetsMultiRowOrderByOrderID(t *testing.T) {
	csv := header +
		"04-03-2024,14:45,SAMPLE LARGE-CAP ETF (DIST),IE00FIXTURE001,EAM,XAMS,7,29.5000,EUR,-206.50,EUR,-206.50,,0.00,,-206.50,ord-1\n" +
		"04-03-2024,14:45,SAMPLE LARGE-CAP ETF (DIST),IE00FIXTURE001,EAM,,-7,29.5000,EUR,206.50,EUR,206.50,,0.00,,206.50,ord-1\n" +
		"04-03-2024,17:15,SAMPLE LARGE-CAP ETF (DIST),IE00FIXTURE001,EAM,,7,29.4560,EUR,-206.19,EUR,-206.19,,0.00,,-206.19,ord-1\n" +
		"04-03-2024,17:15,SAMPLE LARGE-CAP ETF (DIST),IE00FIXTURE001,EAM,,-7,29.4560,EUR,206.19,EUR,206.19,,0.00,,206.19,ord-1\n" +
		"04-03-2024,17:15,SAMPLE LARGE-CAP ETF (DIST),IE00FIXTURE001,EAM,XAMS,7,29.4560,EUR,-206.19,EUR,-206.19,,0.00,,-206.19,ord-1\n"

	txs, _, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 netted transaction, got %d", len(txs))
	}
	tx := txs[0]
	if tx.Type != ledger.TypeBuy {
		t.Errorf("Type = %q, want %q", tx.Type, ledger.TypeBuy)
	}
	if !tx.Quantity.Equal(decimal.NewFromInt(7)) {
		t.Errorf("Quantity = %s, want 7 (net of the five bookings)", tx.Quantity)
	}
	// Netted cash is -206.19 over 7 units.
	if want := decimal.RequireFromString("29.4557"); !tx.Price.Equal(want) {
		t.Errorf("Price = %s, want %s (netted cash / net quantity)", tx.Price, want)
	}
	if tx.SourceRef != "ord-1" {
		t.Errorf("SourceRef = %q, want ord-1", tx.SourceRef)
	}
}

// TestParse_DropsFullyCancelledOrder covers an Order ID whose rows net
// to zero quantity — a booking that was entirely reversed. Nothing
// should be imported for it.
func TestParse_DropsFullyCancelledOrder(t *testing.T) {
	csv := header +
		"04-03-2024,17:15,SAMPLE LARGE-CAP ETF (DIST),IE00FIXTURE001,EAM,,7,29.4560,EUR,-206.19,EUR,-206.19,,0.00,,-206.19,ord-9\n" +
		"04-03-2024,17:15,SAMPLE LARGE-CAP ETF (DIST),IE00FIXTURE001,EAM,,-7,29.4560,EUR,206.19,EUR,206.19,,0.00,,206.19,ord-9\n"

	txs, _, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("expected 0 transactions for a fully-cancelled order, got %d", len(txs))
	}
}

// TestParse_DropsVenueTransferPairWithNoOrderID covers a holding
// moved between listing venues (or renamed by a product change): an
// equal-and-opposite pair of rows, same date/instrument/price, no
// Order ID. Both sides must be dropped.
func TestParse_DropsVenueTransferPairWithNoOrderID(t *testing.T) {
	csv := header +
		"11-06-2024,00:00,SAMPLE ADR CLASS A,US00FIXTURE02,TDG,,5,35.9000,EUR,-179.50,EUR,-179.50,,0.00,,-179.50,\n" +
		`11-06-2024,00:00,"SAMPLE ADR, ALT VENUE NAME",US00FIXTURE02,FRA,,-5,35.9000,EUR,179.50,EUR,179.50,,0.00,,179.50,` + "\n"

	txs, _, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("expected 0 transactions for a venue-transfer pair, got %d", len(txs))
	}
}

// TestParse_KeepsUnpairedNoOrderIDRow covers a genuine disposal that
// Degiro booked without an Order ID: with no opposite row to cancel
// against, it must survive as an ordinary sell.
func TestParse_KeepsUnpairedNoOrderIDRow(t *testing.T) {
	csv := header +
		"22-07-2024,00:00,SAMPLE GROWTH CO,CA00FIXTURE03,FRA,,-11,12.9900,EUR,142.89,EUR,142.89,,0.00,,142.89,\n"

	txs, _, err := Parse(strings.NewReader(csv))
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
	if !tx.Quantity.Equal(decimal.NewFromInt(11)) {
		t.Errorf("Quantity = %s, want 11", tx.Quantity)
	}
	if tx.SourceRef != "" {
		t.Errorf("SourceRef = %q, want empty", tx.SourceRef)
	}
}

func TestParse_MalformedRow_ReturnsExplicitError(t *testing.T) {
	// Quantity column is not a number.
	csv := header + "15-03-2024,10:32,ISHARES CORE MSCI WORLD,IE00B4L5Y983,EAM,XAMS,not-a-number,85.32,EUR,-853.20,EUR,-853.20,,0.00,-1.00,-854.20,abc-123\n"

	_, _, err := Parse(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected an error for a malformed quantity, got nil")
	}
}

func TestParse_MissingColumns_ReturnsExplicitError(t *testing.T) {
	csv := header + "15-03-2024,10:32,ISHARES CORE MSCI WORLD,IE00B4L5Y983\n" // truncated row

	_, _, err := Parse(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected an error for a row with missing columns, got nil")
	}
}

func TestParse_UnrecognizedHeader_ReturnsExplicitError(t *testing.T) {
	csv := "Totally,Different,Columns\nfoo,bar,baz\n"

	_, _, err := Parse(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected an error for an unrecognized header shape, got nil")
	}
}

func TestParse_EmptyFile_ReturnsExplicitError(t *testing.T) {
	_, _, err := Parse(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected an error for an empty file, got nil")
	}
}

func TestParse_HeaderOnly_ReturnsNoTransactionsNoError(t *testing.T) {
	txs, _, err := Parse(strings.NewReader(header))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("expected 0 transactions for a header-only file, got %d", len(txs))
	}
}

func TestDetect_RecognizesDegiroHeader(t *testing.T) {
	fields := strings.Split(strings.TrimSuffix(header, "\n"), ",")
	if !Detect(fields) {
		t.Error("expected Detect to recognize the Degiro header shape")
	}
}

func TestDetect_RejectsUnrelatedHeader(t *testing.T) {
	if Detect([]string{"Totally", "Different", "Columns"}) {
		t.Error("expected Detect to reject an unrelated header shape")
	}
}
