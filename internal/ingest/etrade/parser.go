package etrade

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// wantHeader is the column layout this parser expects: a single
// unified transaction export covering both RSU vests and sales. This
// is a v1 assumption — ETRADE's real exports vary by report type
// (Benefit History vs. G&L vs. Transactions) and has NOT yet been
// checked against a real export; see tasks/plan.md CHECKPOINT 3.
var wantHeader = []string{
	"TransactionType", "Date", "Symbol", "Quantity", "PricePerShare", "Currency", "Reference",
}

const dateLayout = "2006-01-02"

const (
	colTransactionType = iota
	colDate
	colSymbol
	colQuantity
	colPricePerShare
	colCurrency
	colReference
)

// transactionTypes maps ETRADE's TransactionType column values to
// ledger.Type. An unrecognized value is an explicit error, not a
// silent skip or guess.
var transactionTypes = map[string]ledger.Type{
	"Vest": ledger.TypeRSUVest,
	"Sell": ledger.TypeSell,
}

// Detect reports whether header matches this parser's expected ETRADE
// column layout. Used by platform auto-detection in cmd/taxman.
func Detect(header []string) bool {
	if len(header) != len(wantHeader) {
		return false
	}
	for i, want := range wantHeader {
		if strings.TrimSpace(header[i]) != want {
			return false
		}
	}
	return true
}

// Parse reads an ETRADE transaction CSV export and returns one
// ledger.Transaction per row. A Vest row's PricePerShare is the
// vest-date fair market value, carried through unchanged as
// ledger.Transaction.Price — internal/engine's ComputeCGT (task 4.3)
// depends on this being the FMV, not a purchase price. An
// unrecognized TransactionType or a malformed row returns an explicit
// error rather than being skipped or guessed.
func Parse(r io.Reader) ([]ledger.Transaction, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("etrade: reading header: %w", err)
	}
	if !Detect(header) {
		return nil, fmt.Errorf("etrade: unrecognized header shape: %v", header)
	}

	var out []ledger.Transaction
	rowNum := 1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("etrade: reading row %d: %w", rowNum+1, err)
		}
		rowNum++

		tx, err := parseRow(record)
		if err != nil {
			return nil, fmt.Errorf("etrade: row %d: %w", rowNum, err)
		}
		out = append(out, tx)
	}

	return out, nil
}

func parseRow(record []string) (ledger.Transaction, error) {
	if len(record) != len(wantHeader) {
		return ledger.Transaction{}, fmt.Errorf("expected %d columns, got %d", len(wantHeader), len(record))
	}

	txType, ok := transactionTypes[strings.TrimSpace(record[colTransactionType])]
	if !ok {
		return ledger.Transaction{}, fmt.Errorf("unrecognized transaction type %q", record[colTransactionType])
	}

	date, err := time.Parse(dateLayout, strings.TrimSpace(record[colDate]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing date %q: %w", record[colDate], err)
	}

	symbol := strings.TrimSpace(record[colSymbol])
	if symbol == "" {
		return ledger.Transaction{}, fmt.Errorf("empty symbol")
	}

	quantity, err := decimal.NewFromString(strings.TrimSpace(record[colQuantity]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing quantity %q: %w", record[colQuantity], err)
	}

	price, err := decimal.NewFromString(strings.TrimSpace(record[colPricePerShare]))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("parsing price per share %q: %w", record[colPricePerShare], err)
	}

	currency := strings.TrimSpace(record[colCurrency])
	if currency == "" {
		return ledger.Transaction{}, fmt.Errorf("empty currency")
	}

	return ledger.Transaction{
		Platform:   ledger.PlatformETRADE,
		Type:       txType,
		Date:       date.UTC(),
		Instrument: symbol,
		Quantity:   quantity.Abs(),
		Price:      price.Abs(),
		Currency:   currency,
		SourceRef:  strings.TrimSpace(record[colReference]),
	}, nil
}
