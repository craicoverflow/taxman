package ibkr

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// IBKR's Activity Statement CSV interleaves multiple sections in one
// file, each introduced by a section-name column and a "Header" row.
// This fixture reproduces that shape with a Trades section (what this
// parser targets) alongside an unrelated section, to confirm the
// parser correctly isolates just the Trades rows.
const sampleStatement = `Statement,Header,Field Name,Field Value
Statement,Data,BrokerName,Interactive Brokers
Trades,Header,DataDiscriminator,Asset Category,Currency,Symbol,Date/Time,Quantity,T. Price,ISIN
Trades,Data,Order,Stocks,EUR,IWDA,2024-03-15,10,85.32,IE00B4L5Y983
Trades,Data,Order,Stocks,EUR,IWDA,2024-06-20,-5,90.00,IE00B4L5Y983
Dividends,Header,Currency,Date,Description,Amount
Dividends,Data,EUR,2024-04-01,IWDA Dividend,12.50
`

func TestParse_ExtractsOnlyTradesSection(t *testing.T) {
	txs, _, err := Parse(strings.NewReader(sampleStatement))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 2 {
		t.Fatalf("expected 2 transactions (Trades rows only, Dividends ignored), got %d", len(txs))
	}
}

func TestParse_BuyRow(t *testing.T) {
	txs, _, err := Parse(strings.NewReader(sampleStatement))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tx := txs[0]
	if tx.Platform != ledger.PlatformIBKR {
		t.Errorf("Platform = %q, want %q", tx.Platform, ledger.PlatformIBKR)
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
	if tx.Description != "IWDA" {
		t.Errorf("Description = %q, want the Symbol column's value", tx.Description)
	}
}

func TestParse_SellRow_NegativeQuantity(t *testing.T) {
	txs, _, err := Parse(strings.NewReader(sampleStatement))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tx := txs[1]
	if tx.Type != ledger.TypeSell {
		t.Errorf("Type = %q, want %q (negative quantity)", tx.Type, ledger.TypeSell)
	}
	if !tx.Quantity.Equal(decimal.NewFromInt(5)) {
		t.Errorf("Quantity = %s, want 5 (positive magnitude)", tx.Quantity)
	}
}

func TestParse_NoTradesSection_ReturnsEmptyNoError(t *testing.T) {
	statement := "Dividends,Header,Currency,Date,Description,Amount\nDividends,Data,EUR,2024-04-01,IWDA Dividend,12.50\n"
	txs, _, err := Parse(strings.NewReader(statement))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("expected 0 transactions when there's no Trades section, got %d", len(txs))
	}
}

func TestParse_MalformedTradesRow_ReturnsExplicitError(t *testing.T) {
	statement := "Trades,Header,DataDiscriminator,Asset Category,Currency,Symbol,Date/Time,Quantity,T. Price,ISIN\n" +
		"Trades,Data,Order,Stocks,EUR,IWDA,2024-03-15,not-a-number,85.32,IE00B4L5Y983\n"

	_, _, err := Parse(strings.NewReader(statement))
	if err == nil {
		t.Fatal("expected an error for a malformed quantity in a Trades row, got nil")
	}
}

func TestParse_EmptyFile_ReturnsEmptyNoError(t *testing.T) {
	txs, _, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("expected 0 transactions for an empty file, got %d", len(txs))
	}
}

func TestDetect_RecognizesIBKRFirstLine(t *testing.T) {
	// cmd/taxman's platform auto-detection reads only the file's
	// first row (see cmd/taxman/import.go's detectPlatform) — for a
	// real IBKR Activity Statement export, that's the Statement
	// section's own header line, not the Trades section's (which may
	// appear many lines later). Detect must work off THIS shape.
	firstLine := []string{"Statement", "Header", "Field Name", "Field Value"}
	if !Detect(firstLine) {
		t.Error("expected Detect to recognize the IBKR statement's first-line shape")
	}
}

func TestDetect_RejectsDegiroHeader(t *testing.T) {
	degiroHeader := []string{
		"Date", "Time", "Product", "ISIN", "Exchange", "Venue",
		"Quantity", "Price", "Local Value", "Value", "Currency",
		"Exchange Rate", "Transaction Fee", "Total", "Order ID",
	}
	if Detect(degiroHeader) {
		t.Error("expected Detect to reject a Degiro-shaped header")
	}
}

// --- flat trades export (Flex Query / "Transactions" report) ---

// sampleFlat reproduces the flat "Transactions" (Flex Query) export
// shape: one header row, one row per fill, no currency column, a mix
// of EUR and non-EUR (FXRateToBase != 1) instruments, fractional
// quantities, and currency-conversion rows (blank ISIN) that must be
// skipped. All identifiers and amounts here are fabricated.
const sampleFlat = `ClientAccountID,TradeDate,OrderTime,Symbol,ISIN,ListingExchange,Exchange,Quantity,TradePrice,TradeMoney,NetCash,FXRateToBase
U1,20250515,20250514;130656,EURT,LU00FIXTURE05,SBF,SBF,12,300,3600,-3603.75,1
U1,20250515,20250514;130656,EURT,LU00FIXTURE05,SBF,SBF,18,300,5400,-5403.13,1
U1,20250412,20250411;155247,EURT,LU00FIXTURE05,SBF,SBF,-1,296.00,-296.00,292.13,1
U1,20250320,20250320;035118,SPXT,IE00FIXTURE04,LSEETF,LSEETF,5,600,3000,-3004,0.90000
U1,20250210,20250210;093000,EUR.USD,,,IDEALFX,-1500.00,1.15000,-1725.00,0,0.85000
`

func parseFlat_t(t *testing.T, csv string) ([]ledger.Transaction, []string) {
	t.Helper()
	txs, warnings, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return txs, warnings
}

func TestParse_Flat_SkipsBlankISINCashRowsWithWarning(t *testing.T) {
	txs, warnings := parseFlat_t(t, sampleFlat)

	if len(txs) != 4 {
		t.Fatalf("expected 4 securities trades (the EUR.USD row skipped), got %d", len(txs))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "skipped 1") {
		t.Errorf("expected one 'skipped 1 ...' warning, got %v", warnings)
	}
}

func TestParse_Flat_BuyRow_ConvertsPriceToEURViaFXRate(t *testing.T) {
	txs, _ := parseFlat_t(t, sampleFlat)

	// The non-EUR row: TradePrice 600 * FXRateToBase 0.90000 = 540.
	var nonEUR *ledger.Transaction
	for i := range txs {
		if txs[i].Instrument == "IE00FIXTURE04" {
			nonEUR = &txs[i]
		}
	}
	if nonEUR == nil {
		t.Fatal("non-EUR trade not found")
	}
	if nonEUR.Type != ledger.TypeBuy {
		t.Errorf("Type = %q, want buy", nonEUR.Type)
	}
	if nonEUR.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR (converted at FXRateToBase)", nonEUR.Currency)
	}
	if want := decimal.RequireFromString("540"); !nonEUR.Price.Equal(want) {
		t.Errorf("Price = %s, want %s (600 * 0.90000)", nonEUR.Price, want)
	}
	if !nonEUR.Quantity.Equal(decimal.NewFromInt(5)) {
		t.Errorf("Quantity = %s, want 5", nonEUR.Quantity)
	}
}

func TestParse_Flat_EURRow_PriceUnchanged(t *testing.T) {
	txs, _ := parseFlat_t(t, sampleFlat)

	// First EUR row: FXRateToBase 1, so price stays 300.
	if txs[0].Instrument != "LU00FIXTURE05" {
		t.Fatalf("first tx instrument = %q, want the EUR-row ISIN", txs[0].Instrument)
	}
	if want := decimal.RequireFromString("300"); !txs[0].Price.Equal(want) {
		t.Errorf("Price = %s, want %s", txs[0].Price, want)
	}
}

func TestParse_Flat_SellRow_NegativeQuantityBecomesSell(t *testing.T) {
	txs, _ := parseFlat_t(t, sampleFlat)

	var sell *ledger.Transaction
	for i := range txs {
		if txs[i].Type == ledger.TypeSell {
			sell = &txs[i]
		}
	}
	if sell == nil {
		t.Fatal("expected a sell among the parsed trades")
	}
	if !sell.Quantity.Equal(decimal.NewFromInt(1)) {
		t.Errorf("sell Quantity = %s, want 1 (magnitude)", sell.Quantity)
	}
}

func TestParse_Flat_DateFromTradeDatePlusOrderTimeOfDay(t *testing.T) {
	txs, _ := parseFlat_t(t, sampleFlat)

	// First row: TradeDate 20250515, OrderTime 20250514;130656 ->
	// 2025-05-15 13:06:56 (calendar date from TradeDate, time-of-day
	// from OrderTime).
	want := time.Date(2025, 5, 15, 13, 6, 56, 0, time.UTC)
	if !txs[0].Date.Equal(want) {
		t.Errorf("Date = %v, want %v", txs[0].Date, want)
	}
}

func TestParse_Flat_FractionalQuantity(t *testing.T) {
	csv := "ClientAccountID,TradeDate,OrderTime,Symbol,ISIN,ListingExchange,Exchange,Quantity,TradePrice,TradeMoney,NetCash,FXRateToBase\n" +
		"U1,20250515,20250515;030445,EURT,LU00FIXTURE05,SBF,IBRECINV,0.5000,300.000000,150.00,-153.00,1\n"
	txs, _ := parseFlat_t(t, csv)
	if len(txs) != 1 {
		t.Fatalf("expected 1 tx, got %d", len(txs))
	}
	if want := decimal.RequireFromString("0.5000"); !txs[0].Quantity.Equal(want) {
		t.Errorf("Quantity = %s, want %s", txs[0].Quantity, want)
	}
}

func TestParse_Flat_MalformedRow_ReturnsExplicitError(t *testing.T) {
	for _, bad := range []string{
		"U1,20250515,20250515;030445,EURT,LU00FIXTURE05,SBF,SBF,not-a-number,300,3600,-3603,1",
		"U1,NOTADATE,20250515;030445,EURT,LU00FIXTURE05,SBF,SBF,12,300,3600,-3603,1",
		"U1,20250515,20250515;030445,EURT,LU00FIXTURE05,SBF,SBF,12,300,3600,-3603,0",
	} {
		csv := "ClientAccountID,TradeDate,OrderTime,Symbol,ISIN,ListingExchange,Exchange,Quantity,TradePrice,TradeMoney,NetCash,FXRateToBase\n" + bad + "\n"
		if _, _, err := Parse(strings.NewReader(csv)); err == nil {
			t.Errorf("expected an error for row %q, got nil", bad)
		}
	}
}

func TestDetect_RecognizesFlatHeader(t *testing.T) {
	header := []string{
		"ClientAccountID", "TradeDate", "OrderTime", "Symbol", "ISIN",
		"ListingExchange", "Exchange", "Quantity", "TradePrice", "TradeMoney",
		"NetCash", "FXRateToBase",
	}
	if !Detect(header) {
		t.Error("expected Detect to recognize the flat trades export header")
	}
}

// IBKR's "Transaction History" report shares the Activity Statement's
// first row but carries its trades in a "Transaction History" section
// keyed by ticker (no ISIN) with a Price Currency column. Cash legs
// (Forex Trade Component, Adjustment, Deposit) sit alongside the trades.
const sampleTransactionHistory = `Statement,Header,Field Name,Field Value
Statement,Data,Title,Transaction History
Summary,Header,Field Name,Field Value
Summary,Data,Base Currency,EUR
Transaction History,Header,Date,Account,Description,Transaction Type,Symbol,Quantity,Price,Price Currency,Gross Amount ,Commission,Net Amount
Transaction History,Data,2025-01-10,U***00000,FIXTURE EUR FUND,Buy,FIXA,5.0,100.5,EUR,-502.5,-3.0,-505.5
Transaction History,Data,2025-01-10,U***00000,FIXTURE US STOCK,Buy,FIXB,2.0,50.25,USD,-90.0,-0.9,-90.9
Transaction History,Data,2025-01-10,U***00000,Net Amount in Base from Forex Trade: -90.9 EUR.USD,Forex Trade Component,EUR.USD,-90.9,1.1,USD,0.5,-,0.5
Transaction History,Data,2025-01-11,U***00000,FIXTURE US STOCK,Sell,FIXB,-1.0,60.0,USD,54.0,-0.9,53.1
Transaction History,Data,2025-01-11,U***00000,FX Translations P&L,Adjustment,-,-,-,-,0.08,-,0.08
Transaction History,Data,2025-01-09,U***00000,Electronic Fund Transfer,Deposit,-,-,-,-,1000.0,-,1000.0
`

func TestParse_TransactionHistory_ImportsTradesSkipsCashRowsWithWarning(t *testing.T) {
	txs, warnings, err := Parse(strings.NewReader(sampleTransactionHistory))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(txs) != 3 {
		t.Fatalf("expected 3 trades (2 buys + 1 sell), got %d", len(txs))
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "skipped 3") {
		t.Errorf("expected a warning about 3 skipped cash rows, got %v", warnings)
	}
}

func TestParse_TransactionHistory_KeepsNativeCurrencyAndTickerInstrument(t *testing.T) {
	txs, _, err := Parse(strings.NewReader(sampleTransactionHistory))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	eur, usd := txs[0], txs[1]
	if eur.Instrument != "FIXA" || eur.Currency != "EUR" {
		t.Errorf("EUR buy = %q/%q, want FIXA/EUR", eur.Instrument, eur.Currency)
	}
	if usd.Instrument != "FIXB" || usd.Currency != "USD" {
		t.Errorf("USD buy = %q/%q, want FIXB/USD (native; engine converts at ECB rate)", usd.Instrument, usd.Currency)
	}
	if !usd.Price.Equal(decimal.RequireFromString("50.25")) {
		t.Errorf("USD price = %s, want native 50.25", usd.Price)
	}
	if usd.Type != ledger.TypeBuy || !usd.Quantity.Equal(decimal.NewFromInt(2)) {
		t.Errorf("USD buy type/qty = %q/%s, want buy/2", usd.Type, usd.Quantity)
	}
	if usd.Platform != ledger.PlatformIBKR {
		t.Errorf("Platform = %q, want ibkr", usd.Platform)
	}
	wantDate := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	if !usd.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", usd.Date, wantDate)
	}
}

func TestParse_TransactionHistory_SellHasPositiveQuantity(t *testing.T) {
	txs, _, err := Parse(strings.NewReader(sampleTransactionHistory))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sell := txs[2]
	if sell.Type != ledger.TypeSell || !sell.Quantity.Equal(decimal.NewFromInt(1)) {
		t.Errorf("sell type/qty = %q/%s, want sell/1", sell.Type, sell.Quantity)
	}
}

func TestParse_TransactionHistory_UnknownTransactionType_ReturnsExplicitError(t *testing.T) {
	in := strings.Replace(sampleTransactionHistory, ",Deposit,", ",Mystery,", 1)
	if _, _, err := Parse(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), "Mystery") {
		t.Fatalf("expected an error naming the unrecognized type, got %v", err)
	}
}

func TestParse_TransactionHistory_MalformedTradeRow_ReturnsExplicitError(t *testing.T) {
	in := strings.Replace(sampleTransactionHistory, ",5.0,100.5,", ",five,100.5,", 1)
	if _, _, err := Parse(strings.NewReader(in)); err == nil {
		t.Fatal("expected an error for a non-numeric quantity")
	}
}
