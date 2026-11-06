// Package manualtrade builds buy and sell transactions typed in on the
// dashboard rather than imported from a broker export. See SPEC.md §18.
package manualtrade

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

const dateLayout = "2006-01-02"

// NewTrade builds a ledger.Transaction for one stock or ETF buy or
// sell entered by hand. It is stored on ledger.PlatformManual —
// "nobody exported this; it was typed in" — so the audit trail says so.
//
// kind must be "buy" or "sell" (an RSU vest has its own entry path,
// etrade.NewRSUVest). instrument is the user's own key for the
// holding, an ISIN or a ticker, trimmed and otherwise taken as typed:
// it is never looked up or inferred. date must be YYYY-MM-DD; quantity
// and price (per unit, in currency) must be positive decimal strings.
// currency is required, upper-cased, and never defaulted. A non-EUR
// trade is restated to euro at its trade-date rate by the engine,
// never here. Fees are not modelled (SPEC.md §18).
func NewTrade(kind, instrument, date, quantity, price, currency string) (ledger.Transaction, error) {
	var typ ledger.Type
	switch strings.TrimSpace(kind) {
	case "buy":
		typ = ledger.TypeBuy
	case "sell":
		typ = ledger.TypeSell
	default:
		return ledger.Transaction{}, fmt.Errorf("manualtrade: kind %q must be buy or sell", kind)
	}

	instrument = strings.TrimSpace(instrument)
	if instrument == "" {
		return ledger.Transaction{}, fmt.Errorf("manualtrade: instrument must not be empty")
	}

	parsedDate, err := time.Parse(dateLayout, strings.TrimSpace(date))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("manualtrade: parsing trade date %q: %w", date, err)
	}

	parsedQty, err := decimal.NewFromString(strings.TrimSpace(quantity))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("manualtrade: parsing quantity %q: %w", quantity, err)
	}
	if !parsedQty.IsPositive() {
		return ledger.Transaction{}, fmt.Errorf("manualtrade: quantity %q must be positive", quantity)
	}

	parsedPrice, err := decimal.NewFromString(strings.TrimSpace(price))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("manualtrade: parsing price %q: %w", price, err)
	}
	if !parsedPrice.IsPositive() {
		return ledger.Transaction{}, fmt.Errorf("manualtrade: price %q must be positive", price)
	}

	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return ledger.Transaction{}, fmt.Errorf("manualtrade: currency must not be empty")
	}

	return ledger.Transaction{
		Platform:   ledger.PlatformManual,
		Type:       typ,
		Date:       parsedDate.UTC(),
		Instrument: instrument,
		Quantity:   parsedQty,
		Price:      parsedPrice,
		Currency:   currency,
	}, nil
}

// CheckNoOversell reports an error if, once added is in the ledger
// alongside existing, any sell would exceed the quantity held at its
// date. The engine treats that as a hard error that breaks computation
// for the whole dashboard, so entry refuses it up front.
//
// It mirrors the engine's FIFO matching on quantity alone: every
// transaction of an instrument is ordered by date (stable, so same-day
// rows keep their order, existing before added), a buy or RSU vest adds
// to the held quantity, a sell takes from it. Quantity alone is enough
// — FIFO runs short exactly when the running total does — and it needs
// no exchange rate, so an unrelated FX gap can't block an entry.
func CheckNoOversell(existing, added []ledger.Transaction) error {
	// Only the instruments being written are checked: an existing
	// problem in an unrelated holding must not block this entry.
	touched := map[string]bool{}
	for _, tx := range added {
		touched[tx.Instrument] = true
	}
	instruments := make([]string, 0, len(touched))
	for k := range touched {
		instruments = append(instruments, k)
	}
	sort.Strings(instruments) // a fixed order keeps the error deterministic

	all := make([]ledger.Transaction, 0, len(existing)+len(added))
	all = append(all, existing...)
	all = append(all, added...)
	for _, instrument := range instruments {
		if err := CheckHolding(all, instrument); err != nil {
			return err
		}
	}
	return nil
}

// CheckHolding reports an error if any sell of instrument in txs
// exceeds the quantity held at its date. Other instruments in txs are
// ignored. It is what a delete or an edit needs: after removing or
// replacing a row, does the holding still balance?
func CheckHolding(txs []ledger.Transaction, instrument string) error {
	var mine []ledger.Transaction
	for _, tx := range txs {
		if tx.Instrument == instrument {
			mine = append(mine, tx)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].Date.Before(mine[j].Date) })

	held := decimal.Zero
	for _, tx := range mine {
		switch tx.Type {
		case ledger.TypeBuy, ledger.TypeRSUVest:
			held = held.Add(tx.Quantity)
		case ledger.TypeSell:
			held = held.Sub(tx.Quantity)
			if held.IsNegative() {
				return fmt.Errorf("manualtrade: sell of %s %s on %s exceeds the quantity held by %s",
					tx.Quantity, instrument, tx.Date.Format(dateLayout), held.Neg())
			}
		}
	}
	return nil
}
