package degiro

import (
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// wantAccountHeader is the column layout for Degiro's "Account"
// export — a cash-movement ledger, distinct from the "Transactions"
// export handled by parser.go. Confirmed against a real export (see
// tasks/plan.md CHECKPOINT 3 follow-up). Two columns are unnamed in
// Degiro's own header row: they hold the currency for the adjacent
// Change/Balance amount columns.
var wantAccountHeader = []string{
	"Date", "Time", "Value date", "Product", "ISIN", "Description",
	"FX", "Change", "", "Balance", "", "Order Id",
}

// column indices into wantAccountHeader.
const (
	acctColDate = iota
	acctColTime
	acctColValueDate
	acctColProduct
	acctColISIN
	acctColDescription
	acctColFX
	acctColChangeCurrency
	acctColChange
	acctColBalanceCurrency
	acctColBalance
	acctColOrderID
)

// corporateActionPrefixes marks Description text this parser refuses
// to treat as an ordinary trade. Mergers, splits, product changes,
// and internal transfers all affect cost basis in ways plain FIFO
// buy/sell doesn't capture correctly, so importing one requires a
// human decision, not a guess — see tasks/plan.md and SPEC.md §6.
var corporateActionPrefixes = []string{
	"MERGER:",
	"SPLIT ADJUSTMENT:",
	"PRODUCT CHANGE:",
	"INTERNAL TRANSFER:",
}

// tradeDescription matches an ordinary Buy/Sell row's leading
// "Buy 10 " / "Sell 5 " shape, e.g. "Buy 10 Widget Corp@85.32 EUR
// (IE0000000001)". Only the action and quantity are extracted here:
// the instrument comes from the row's own ISIN column, and price is
// derived from the row's Change amount (the actual settled value)
// rather than re-parsed from the free-text price, which may be
// rounded differently.
var tradeDescription = regexp.MustCompile(`^(Buy|Sell) ([0-9]+(?:\.[0-9]+)?) `)

// DetectAccount reports whether header matches Degiro's Account
// export column layout.
func DetectAccount(header []string) bool {
	if len(header) != len(wantAccountHeader) {
		return false
	}
	for i, want := range wantAccountHeader {
		if strings.TrimSpace(header[i]) != want {
			return false
		}
	}
	return true
}

// ParseAccount reads a Degiro Account (cash-ledger) CSV export and
// returns one ledger.Transaction per recognizable Buy/Sell row.
//
// Rows for cash movements that aren't trades — interest, dividends,
// fees, FX conversions, deposits/withdrawals, cash sweep transfers,
// and the like — are skipped silently: nothing in taxman currently
// computes from them, so storing them would just be dead data (see
// tasks/plan.md). Corporate-action rows (mergers, splits, product
// changes, internal transfers) are also skipped rather than parsed as
// ordinary trades — cost basis needs a human decision there, not a
// guess — but each one is named in the returned warnings rather than
// silently dropped, and does not stop the rest of the file importing.
func ParseAccount(r io.Reader) ([]ledger.Transaction, []string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // validated manually for a clearer error message

	header, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("degiro: account: reading header: %w", err)
	}
	if !DetectAccount(header) {
		return nil, nil, fmt.Errorf("degiro: account: unrecognized header shape: %v", header)
	}

	return parseAccountRows(reader)
}

// parseAccountRows reads the data rows of a Degiro Account export
// from reader, whose header row has already been read and confirmed
// (by ParseAccount, or by Parse's dispatch). Shared so both entry
// points report identical row numbers and error wording.
func parseAccountRows(reader *csv.Reader) ([]ledger.Transaction, []string, error) {
	var out []ledger.Transaction
	var warnings []string
	rowNum := 1 // header was row 1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("degiro: account: reading row %d: %w", rowNum+1, err)
		}
		rowNum++

		tx, ok, warning, err := parseAccountRow(record)
		if err != nil {
			return nil, nil, fmt.Errorf("degiro: account: row %d: %w", rowNum, err)
		}
		if warning != "" {
			warnings = append(warnings, fmt.Sprintf("row %d: %s", rowNum, warning))
			continue
		}
		if !ok {
			continue // not a trade row — skipped silently, not a warning
		}
		out = append(out, tx)
	}

	return out, warnings, nil
}

// parseAccountRow attempts to interpret record as a trade.
//
//   - ok, with a non-empty warning: a corporate-action row. Not
//     imported; the caller surfaces the warning but keeps going.
//   - !ok, empty warning, nil error: an ordinary non-trade row
//     (interest, dividend, fee, ...). Skipped silently.
//   - non-nil error: record looked like a trade but was malformed
//     (bad quantity/ISIN/currency/date) — that's not safe to skip
//     quietly, so it stops the import.
func parseAccountRow(record []string) (tx ledger.Transaction, ok bool, warning string, err error) {
	if len(record) != len(wantAccountHeader) {
		return ledger.Transaction{}, false, "", fmt.Errorf("expected %d columns, got %d", len(wantAccountHeader), len(record))
	}

	description := strings.TrimSpace(record[acctColDescription])

	for _, prefix := range corporateActionPrefixes {
		if strings.HasPrefix(description, prefix) {
			return ledger.Transaction{}, false, fmt.Sprintf("unsupported corporate action %q; not imported automatically, see tasks/plan.md", description), nil
		}
	}

	match := tradeDescription.FindStringSubmatch(description)
	if match == nil {
		return ledger.Transaction{}, false, "", nil
	}

	txType := ledger.TypeBuy
	if match[1] == "Sell" {
		txType = ledger.TypeSell
	}

	quantity, err := decimal.NewFromString(match[2])
	if err != nil {
		return ledger.Transaction{}, false, "", fmt.Errorf("parsing quantity %q: %w", match[2], err)
	}
	if quantity.IsZero() {
		return ledger.Transaction{}, false, "", fmt.Errorf("trade row %q has zero quantity", description)
	}

	isin := strings.TrimSpace(record[acctColISIN])
	if isin == "" {
		return ledger.Transaction{}, false, "", fmt.Errorf("trade row %q has no ISIN", description)
	}

	currency := strings.TrimSpace(record[acctColChangeCurrency])
	if currency == "" {
		return ledger.Transaction{}, false, "", fmt.Errorf("trade row %q has no currency", description)
	}

	change, err := decimal.NewFromString(strings.TrimSpace(record[acctColChange]))
	if err != nil {
		return ledger.Transaction{}, false, "", fmt.Errorf("parsing change amount %q: %w", record[acctColChange], err)
	}

	date, err := time.Parse(dateLayout, strings.TrimSpace(record[acctColDate]))
	if err != nil {
		return ledger.Transaction{}, false, "", fmt.Errorf("parsing date %q: %w", record[acctColDate], err)
	}

	return ledger.Transaction{
		Platform:    ledger.PlatformDegiro,
		Type:        txType,
		Date:        date.UTC(),
		Instrument:  isin,
		Quantity:    quantity,
		Price:       change.Abs().Div(quantity),
		Currency:    currency,
		SourceRef:   strings.TrimSpace(record[acctColOrderID]),
		Description: strings.TrimSpace(record[acctColProduct]),
	}, true, "", nil
}
