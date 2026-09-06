package degiro

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// wantHeader is the column layout of Degiro's English-locale
// "Transactions" CSV export, confirmed against a real export (see
// tasks/plan.md CHECKPOINT 3). Three header cells are blank in
// Degiro's own header row: index 8 and 10 hold the currency for the
// adjacent Price / Local value amounts.
var wantHeader = []string{
	"Date", "Time", "Product", "ISIN", "Reference exchange", "Venue",
	"Quantity", "Price", "", "Local value", "", "Value EUR",
	"Exchange rate", "AutoFX Fee", "Transaction and/or third party fees EUR",
	"Total EUR", "Order ID",
}

const dateLayout = "02-01-2006" // Degiro's DD-MM-YYYY date format

// column indices into wantHeader, named for readability at the call
// site.
const (
	colDate = iota
	colTime
	colProduct
	colISIN
	colRefExchange
	colVenue
	colQuantity
	colPrice
	colPriceCurrency
	colLocalValue
	colLocalValueCurrency
	colValueEUR
	colExchangeRate
	colAutoFXFee
	colFees
	colTotalEUR
	colOrderID
)

// Detect reports whether header matches either of Degiro's export
// layouts this package understands: the Transactions export (buy/sell
// rows with dedicated Quantity/Price columns) or the Account export
// (a cash-ledger where trades appear as free-text Description rows —
// see account.go). Used by platform auto-detection in cmd/taxman and
// internal/ingest.
func Detect(header []string) bool {
	return detectTransactions(header) || DetectAccount(header)
}

// detectTransactions reports whether header matches this parser's
// expected Degiro Transactions column layout.
func detectTransactions(header []string) bool {
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

// Parse reads a Degiro CSV export — either layout Detect recognizes —
// and returns one ledger.Transaction per economic buy/sell event,
// plus any non-fatal warnings (currently: corporate-action rows in an
// Account export that were skipped rather than imported — see
// account.go). It refuses to guess at anything else: an unrecognized
// header shape or a malformed row returns an explicit error rather
// than skipping the row silently.
//
// A Degiro Transactions export does not list one row per trade. A
// single order is frequently booked and counter-booked several times
// (provisional fill, venue correction, ...), all sharing one Order
// ID; and a position moved between listing venues, or renamed by a
// product change, shows up as an equal-and-opposite pair of rows with
// no Order ID at all. Importing those rows verbatim would invent
// buy/sell events that never economically happened. So before
// emitting transactions, Parse consolidates (see consolidate):
//
//   - rows sharing a non-empty Order ID are netted into one
//     transaction (net quantity, cash summed from Local value); an
//     order whose quantities net to zero is dropped entirely;
//   - among rows with no Order ID, an exact +q / -q pair on the same
//     date, instrument, and unit price is treated as a venue
//     transfer and both sides are dropped; whatever is left over is
//     emitted as an ordinary trade.
//
// This is the "Net by Order ID + drop transfer pairs" policy; a
// genuine same-day round trip booked without an Order ID would be
// collapsed by it, which does not occur in practice for the exports
// this has been checked against.
func Parse(r io.Reader) ([]ledger.Transaction, []string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // validated manually for a clearer error message

	header, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("degiro: reading header: %w", err)
	}
	if DetectAccount(header) {
		return parseAccountRows(reader)
	}
	if !detectTransactions(header) {
		return nil, nil, fmt.Errorf("degiro: unrecognized header shape: %v", header)
	}

	var rows []rawRow
	rowNum := 1 // header was row 1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("degiro: reading row %d: %w", rowNum+1, err)
		}
		rowNum++

		rr, err := parseRawRow(record, rowNum)
		if err != nil {
			return nil, nil, fmt.Errorf("degiro: row %d: %w", rowNum, err)
		}
		rows = append(rows, rr)
	}

	txs, err := consolidate(rows)
	if err != nil {
		return nil, nil, err
	}
	return txs, nil, nil
}

// rawRow is one parsed-but-not-yet-consolidated Transactions row.
// Quantity keeps its sign (Degiro writes a sell as a negative
// quantity); LocalValue keeps its sign too (a buy is negative cash).
type rawRow struct {
	seq        int // 1-based row number in the file, for stable output ordering
	date       time.Time
	isin       string
	product    string
	signedQty  decimal.Decimal
	price      decimal.Decimal // magnitude, from the Price column
	localValue decimal.Decimal // signed, from the Local value column
	currency   string
	orderID    string
}

func parseRawRow(record []string, seq int) (rawRow, error) {
	if len(record) != len(wantHeader) {
		return rawRow{}, fmt.Errorf("expected %d columns, got %d", len(wantHeader), len(record))
	}

	date, err := time.Parse(dateLayout, strings.TrimSpace(record[colDate]))
	if err != nil {
		return rawRow{}, fmt.Errorf("parsing date %q: %w", record[colDate], err)
	}

	isin := strings.TrimSpace(record[colISIN])
	if isin == "" {
		return rawRow{}, fmt.Errorf("empty ISIN")
	}

	signedQty, err := decimal.NewFromString(strings.TrimSpace(record[colQuantity]))
	if err != nil {
		return rawRow{}, fmt.Errorf("parsing quantity %q: %w", record[colQuantity], err)
	}
	if signedQty.IsZero() {
		return rawRow{}, fmt.Errorf("zero quantity")
	}

	price, err := decimal.NewFromString(strings.TrimSpace(record[colPrice]))
	if err != nil {
		return rawRow{}, fmt.Errorf("parsing price %q: %w", record[colPrice], err)
	}

	currency := strings.TrimSpace(record[colPriceCurrency])
	if currency == "" {
		return rawRow{}, fmt.Errorf("empty currency")
	}

	// Local value is what the consolidation step nets on. Degiro
	// always fills it, but if a row omits it, fall back to the
	// price × quantity implied by this row (a buy is negative cash).
	localValue := signedQty.Mul(price).Neg()
	if raw := strings.TrimSpace(record[colLocalValue]); raw != "" {
		localValue, err = decimal.NewFromString(raw)
		if err != nil {
			return rawRow{}, fmt.Errorf("parsing local value %q: %w", record[colLocalValue], err)
		}
	}

	return rawRow{
		seq:        seq,
		date:       date.UTC(),
		isin:       isin,
		product:    strings.TrimSpace(record[colProduct]),
		signedQty:  signedQty,
		price:      price.Abs(),
		localValue: localValue,
		currency:   currency,
		orderID:    strings.TrimSpace(record[colOrderID]),
	}, nil
}

// consolidate turns parsed rows into economic transactions per the
// policy documented on Parse: net by Order ID, then cancel no-Order-ID
// venue-transfer pairs. Output is ordered by the earliest file row
// that contributed to each transaction, so a file's transactions come
// back in a stable, source-order-ish sequence.
func consolidate(rows []rawRow) ([]ledger.Transaction, error) {
	byOrder := map[string][]rawRow{}
	var orderKeys []string // first-appearance order
	var noID []rawRow
	for _, r := range rows {
		if r.orderID == "" {
			noID = append(noID, r)
			continue
		}
		if _, seen := byOrder[r.orderID]; !seen {
			orderKeys = append(orderKeys, r.orderID)
		}
		byOrder[r.orderID] = append(byOrder[r.orderID], r)
	}

	type emitted struct {
		seq int
		tx  ledger.Transaction
	}
	var out []emitted

	for _, k := range orderKeys {
		tx, seq, ok, err := netOrder(k, byOrder[k])
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, emitted{seq, tx})
		}
	}

	for _, r := range cancelTransferPairs(noID) {
		out = append(out, emitted{r.seq, rawRowToTx(r)})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].seq < out[j].seq })

	txs := make([]ledger.Transaction, len(out))
	for i, e := range out {
		txs[i] = e.tx
	}
	return txs, nil
}

// netOrder collapses every row of a single Order ID into one
// transaction. ok is false (with no error) when the rows net to zero
// quantity — a booking that was fully reversed, with nothing to
// import. seq is the earliest contributing row number.
func netOrder(orderID string, grp []rawRow) (tx ledger.Transaction, seq int, ok bool, err error) {
	if len(grp) == 1 {
		return rawRowToTx(grp[0]), grp[0].seq, true, nil
	}

	netQty := decimal.Zero
	netValue := decimal.Zero
	first := grp[0]
	seq = first.seq
	minDate := first.date
	product := ""
	for _, r := range grp {
		if r.currency != first.currency {
			return ledger.Transaction{}, 0, false, fmt.Errorf("degiro: order %s: mixed currencies %q and %q", orderID, first.currency, r.currency)
		}
		if r.isin != first.isin {
			return ledger.Transaction{}, 0, false, fmt.Errorf("degiro: order %s: mixed instruments %q and %q", orderID, first.isin, r.isin)
		}
		netQty = netQty.Add(r.signedQty)
		netValue = netValue.Add(r.localValue)
		if r.date.Before(minDate) {
			minDate = r.date
		}
		if r.seq < seq {
			seq = r.seq
		}
		if product == "" {
			product = r.product
		}
	}

	if netQty.IsZero() {
		return ledger.Transaction{}, 0, false, nil
	}

	txType := ledger.TypeBuy
	if netQty.IsNegative() {
		txType = ledger.TypeSell
	}
	// No single row's Price is representative of a multi-booking
	// order, so derive the unit price from the netted cash and
	// quantity, rounded to Degiro's own 4-decimal price precision.
	price := netValue.Abs().Div(netQty.Abs()).Round(4)

	return ledger.Transaction{
		Platform:    ledger.PlatformDegiro,
		Type:        txType,
		Date:        minDate.UTC(),
		Instrument:  first.isin,
		Quantity:    netQty.Abs(),
		Price:       price,
		Currency:    first.currency,
		SourceRef:   orderID,
		Description: product,
	}, seq, true, nil
}

// cancelTransferPairs drops equal-and-opposite row pairs that share a
// date, instrument, and unit price — Degiro's shape for a holding
// moved between listing venues or renamed by a product change, which
// is not a taxable buy or sell. Rows left unpaired are returned as-is,
// in their original file order.
func cancelTransferPairs(rows []rawRow) []rawRow {
	type key struct{ date, isin, qty, price string }
	buckets := map[key][]rawRow{}
	var order []key
	for _, r := range rows {
		k := key{
			date:  r.date.Format("2006-01-02"),
			isin:  r.isin,
			qty:   r.signedQty.Abs().String(),
			price: r.price.String(),
		}
		if _, ok := buckets[k]; !ok {
			order = append(order, k)
		}
		buckets[k] = append(buckets[k], r)
	}

	var kept []rawRow
	for _, k := range order {
		var pos, neg []rawRow
		for _, r := range buckets[k] {
			if r.signedQty.IsNegative() {
				neg = append(neg, r)
			} else {
				pos = append(pos, r)
			}
		}
		n := len(pos)
		if len(neg) < n {
			n = len(neg)
		}
		kept = append(kept, pos[n:]...)
		kept = append(kept, neg[n:]...)
	}

	sort.SliceStable(kept, func(i, j int) bool { return kept[i].seq < kept[j].seq })
	return kept
}

func rawRowToTx(r rawRow) ledger.Transaction {
	txType := ledger.TypeBuy
	if r.signedQty.IsNegative() {
		txType = ledger.TypeSell
	}
	return ledger.Transaction{
		Platform:    ledger.PlatformDegiro,
		Type:        txType,
		Date:        r.date.UTC(),
		Instrument:  r.isin,
		Quantity:    r.signedQty.Abs(), // magnitude only; direction lives in Type
		Price:       r.price.Abs(),
		Currency:    r.currency,
		SourceRef:   r.orderID,
		Description: r.product,
	}
}
