package ibkr

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// firstLine is the shape of an IBKR Activity Statement export's very
// first row (the Statement section's own header), which is what
// platform auto-detection has to work off of — see
// cmd/taxman/import.go's detectPlatform, which reads only the file's
// first row.
var firstLine = []string{"Statement", "Header", "Field Name", "Field Value"}

// tradesHeader is the column layout of the Trades section's header
// row in an IBKR Activity Statement export, wherever it appears in the
// file. This shape has not been checked against a real Activity
// Statement export; the flat export below (flatHeader) has.
var tradesHeader = []string{
	"Trades", "Header", "DataDiscriminator", "Asset Category",
	"Currency", "Symbol", "Date/Time", "Quantity", "T. Price", "ISIN",
}

// flatHeader is the column layout of IBKR's flat trades export — a
// Flex Query / "Transactions" report with one header row and one row
// per fill, rather than the multi-section Activity Statement. Matched
// against a real export (All_Transactions.csv). Notably there is NO
// currency column: FXRateToBase is the trade currency → account base
// (EUR) rate for that fill, which is what converts each price to euro
// (see parseFlatRow and docs/adr/0002).
var flatHeader = []string{
	"ClientAccountID", "TradeDate", "OrderTime", "Symbol", "ISIN",
	"ListingExchange", "Exchange", "Quantity", "TradePrice", "TradeMoney",
	"NetCash", "FXRateToBase",
}

const (
	dateLayout     = "2006-01-02" // Activity Statement Date/Time column
	flatDateLayout = "20060102"   // flat export TradeDate column
	flatTimeLayout = "150405"     // flat export OrderTime, the "HHMMSS" after the ";"
)

// column indices into an Activity Statement Trades data row (same
// shape as tradesHeader).
const (
	colSection = iota
	colRowType
	colDataDiscriminator
	colAssetCategory
	colCurrency
	colSymbol
	colDateTime
	colQuantity
	colPrice
	colISIN
)

// column indices into a flat-export row (same shape as flatHeader).
const (
	flatColAccountID = iota
	flatColTradeDate
	flatColOrderTime
	flatColSymbol
	flatColISIN
	flatColListingExchange
	flatColExchange
	flatColQuantity
	flatColTradePrice
	flatColTradeMoney
	flatColNetCash
	flatColFXRateToBase
)

// Detect reports whether header matches an IBKR export this parser
// understands: the Activity Statement's first-line shape, or the flat
// trades export's header row. Used by platform auto-detection in
// cmd/taxman, which sees only the file's first row.
func Detect(header []string) bool {
	return headerMatches(header, firstLine) || headerMatches(header, flatHeader)
}

func headerMatches(header, want []string) bool {
	if len(header) != len(want) {
		return false
	}
	for i, w := range want {
		if strings.TrimSpace(header[i]) != w {
			return false
		}
	}
	return true
}

// Parse reads an IBKR CSV export — either the flat trades export or
// the multi-section Activity Statement — and returns one
// ledger.Transaction per fill, plus any non-fatal warnings about rows
// that were skipped rather than imported (currency-conversion / cash
// rows in a flat export). A malformed row is an explicit error, never
// a silent skip; a file with trades in neither recognized shape
// yields zero transactions without error.
func Parse(r io.Reader) ([]ledger.Transaction, []string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // rows across Activity Statement sections have varying column counts

	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("ibkr: reading CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil, nil
	}

	if headerMatches(records[0], flatHeader) {
		return parseFlat(records[1:])
	}
	return parseActivityStatement(records)
}

// parseFlat handles the flat trades export (rows after the header).
// Rows with no ISIN — currency conversions (EUR.USD ...) and other
// cash movements — are counted and skipped with a warning rather than
// imported as bogus holdings.
func parseFlat(rows [][]string) ([]ledger.Transaction, []string, error) {
	var out []ledger.Transaction
	skippedCash := 0

	for i, record := range rows {
		rowNum := i + 2 // header was row 1
		if len(record) != len(flatHeader) {
			return nil, nil, fmt.Errorf("ibkr: row %d: expected %d columns, got %d", rowNum, len(flatHeader), len(record))
		}

		if strings.TrimSpace(record[flatColISIN]) == "" {
			skippedCash++
			continue
		}

		tx, err := parseFlatRow(record)
		if err != nil {
			return nil, nil, fmt.Errorf("ibkr: row %d: %w", rowNum, err)
		}
		out = append(out, tx)
	}

	var warnings []string
	if skippedCash > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"ibkr: skipped %d currency-conversion / cash row(s) with no ISIN (not securities trades)", skippedCash))
	}
	return out, warnings, nil
}

func parseFlatRow(record []string) (ledger.Transaction, error) {
	date, err := time.Parse(flatDateLayout, strings.TrimSpace(record[flatColTradeDate]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing trade date %q: %w", record[flatColTradeDate], err)
	}
	// OrderTime is "YYYYMMDD;HHMMSS"; take the time-of-day and pin it
	// onto TradeDate (the CGT-relevant date) so same-day fills sort in
	// execution order for FIFO. A missing or odd OrderTime just leaves
	// the trade at midnight — same-day order then follows file order.
	if _, hms, ok := strings.Cut(strings.TrimSpace(record[flatColOrderTime]), ";"); ok {
		if tod, terr := time.Parse(flatTimeLayout, hms); terr == nil {
			date = date.Add(time.Duration(tod.Hour())*time.Hour +
				time.Duration(tod.Minute())*time.Minute +
				time.Duration(tod.Second())*time.Second)
		}
	}

	isin := strings.TrimSpace(record[flatColISIN])

	quantity, err := decimal.NewFromString(strings.TrimSpace(record[flatColQuantity]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing quantity %q: %w", record[flatColQuantity], err)
	}
	if quantity.IsZero() {
		return ledger.Transaction{}, fmt.Errorf("zero quantity")
	}

	price, err := decimal.NewFromString(strings.TrimSpace(record[flatColTradePrice]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing trade price %q: %w", record[flatColTradePrice], err)
	}

	// No currency column: FXRateToBase converts a trade-currency
	// amount to the account's base currency (EUR). Applying it here
	// stores every price already in euro — see docs/adr/0002 for why
	// this uses IBKR's own execution rate rather than the ECB path the
	// rest of the codebase takes (there is no currency code to look an
	// ECB rate up by).
	fxRate, err := decimal.NewFromString(strings.TrimSpace(record[flatColFXRateToBase]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing FX rate %q: %w", record[flatColFXRateToBase], err)
	}
	if !fxRate.IsPositive() {
		return ledger.Transaction{}, fmt.Errorf("non-positive FX rate %q", record[flatColFXRateToBase])
	}

	txType := ledger.TypeBuy
	if quantity.IsNegative() {
		txType = ledger.TypeSell
	}

	return ledger.Transaction{
		Platform:    ledger.PlatformIBKR,
		Type:        txType,
		Date:        date.UTC(),
		Instrument:  isin,
		Quantity:    quantity.Abs(),
		Price:       price.Abs().Mul(fxRate), // euro, per share
		Currency:    "EUR",
		SourceRef:   "", // the flat export carries no per-fill order ID
		Description: strings.TrimSpace(record[flatColSymbol]),
	}, nil
}

// parseActivityStatement handles the multi-section Activity Statement:
// one ledger.Transaction per Trades data row, every other section
// ignored. A file with no Trades section is not an error — it yields
// zero transactions.
func parseActivityStatement(records [][]string) ([]ledger.Transaction, []string, error) {
	var out []ledger.Transaction
	sawTradesHeader := false

	for i, record := range records {
		rowNum := i + 1

		if len(record) == 0 || record[0] != "Trades" {
			continue
		}

		if isTradesHeaderRow(record) {
			sawTradesHeader = true
			continue
		}

		if !sawTradesHeader {
			// A Trades data row before its header implies a layout we
			// don't recognize; refuse to guess column positions.
			return nil, nil, fmt.Errorf("ibkr: row %d: Trades data row appeared before a recognized Trades header", rowNum)
		}

		if len(record) < 2 || record[1] != "Data" {
			// Trades,SubTotal / Trades,Total rows are interspersed with
			// data rows; skip anything that isn't a data row.
			continue
		}

		tx, err := parseTradesRow(record)
		if err != nil {
			return nil, nil, fmt.Errorf("ibkr: row %d: %w", rowNum, err)
		}
		out = append(out, tx)
	}

	return out, nil, nil
}

func isTradesHeaderRow(record []string) bool {
	if len(record) != len(tradesHeader) {
		return false
	}
	for i, want := range tradesHeader {
		if strings.TrimSpace(record[i]) != want {
			return false
		}
	}
	return true
}

func parseTradesRow(record []string) (ledger.Transaction, error) {
	if len(record) != len(tradesHeader) {
		return ledger.Transaction{}, fmt.Errorf("expected %d columns, got %d", len(tradesHeader), len(record))
	}

	date, err := time.Parse(dateLayout, strings.TrimSpace(record[colDateTime]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing date %q: %w", record[colDateTime], err)
	}

	isin := strings.TrimSpace(record[colISIN])
	if isin == "" {
		return ledger.Transaction{}, fmt.Errorf("empty ISIN")
	}

	quantity, err := decimal.NewFromString(strings.TrimSpace(record[colQuantity]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing quantity %q: %w", record[colQuantity], err)
	}

	price, err := decimal.NewFromString(strings.TrimSpace(record[colPrice]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing price %q: %w", record[colPrice], err)
	}

	currency := strings.TrimSpace(record[colCurrency])
	if currency == "" {
		return ledger.Transaction{}, fmt.Errorf("empty currency")
	}

	txType := ledger.TypeBuy
	if quantity.IsNegative() {
		txType = ledger.TypeSell
	}

	return ledger.Transaction{
		Platform:    ledger.PlatformIBKR,
		Type:        txType,
		Date:        date.UTC(),
		Instrument:  isin,
		Quantity:    quantity.Abs(),
		Price:       price.Abs(),
		Currency:    currency,
		SourceRef:   "", // IBKR's Activity Statement Trades rows carry no per-row order ID in this layout
		Description: strings.TrimSpace(record[colSymbol]),
	}, nil
}
