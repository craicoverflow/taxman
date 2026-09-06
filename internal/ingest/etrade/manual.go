package etrade

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// NewRSUVest builds a ledger.Transaction for a single RSU vesting
// event entered by hand on the dashboard — for when the E*TRADE
// export on hand doesn't present vests as importable rows (the column
// layout Parse expects is a v1 assumption, not yet verified against a
// real export). It produces the same shape Parse does for a "Vest"
// row: Type rsu_vest, Price is the vest-date fair market value per
// share (internal/engine uses it as the lot's cost basis), Quantity
// the number of shares vested.
//
// symbol and currency must be non-empty; date must be YYYY-MM-DD;
// quantity and fmvPerShare must be positive decimal strings. currency
// is upper-cased (ECB reference-rate codes are upper-case); a non-EUR
// vest is restated to euro at its vest-date rate by the engine, never
// here.
func NewRSUVest(symbol, date, quantity, fmvPerShare, currency string) (ledger.Transaction, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ledger.Transaction{}, fmt.Errorf("etrade: symbol must not be empty")
	}

	parsedDate, err := time.Parse(dateLayout, strings.TrimSpace(date))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("etrade: parsing vest date %q: %w", date, err)
	}

	parsedQty, err := decimal.NewFromString(strings.TrimSpace(quantity))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("etrade: parsing quantity %q: %w", quantity, err)
	}
	if !parsedQty.IsPositive() {
		return ledger.Transaction{}, fmt.Errorf("etrade: quantity %q must be positive", quantity)
	}

	parsedFMV, err := decimal.NewFromString(strings.TrimSpace(fmvPerShare))
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("etrade: parsing FMV per share %q: %w", fmvPerShare, err)
	}
	if !parsedFMV.IsPositive() {
		return ledger.Transaction{}, fmt.Errorf("etrade: FMV per share %q must be positive", fmvPerShare)
	}

	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return ledger.Transaction{}, fmt.Errorf("etrade: currency must not be empty")
	}

	return ledger.Transaction{
		Platform:   ledger.PlatformETRADE,
		Type:       ledger.TypeRSUVest,
		Date:       parsedDate.UTC(),
		Instrument: symbol,
		Quantity:   parsedQty,
		Price:      parsedFMV,
		Currency:   currency,
	}, nil
}
