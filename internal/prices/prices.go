package prices

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Quote is a last-price-per-share for a market symbol, in the
// instrument's native currency, with the time it was fetched.
//
// Currency is an ISO 4217 code when the provider supplied one (the
// primary provider always does); it is "" when the quote came from a
// fallback that reports a price but no currency. A caller that needs
// euro must treat an empty Currency as "cannot convert" rather than
// assuming EUR.
//
// Stale is true when every provider failed and this is the last value
// from cache — AsOf is then the time that cached value was fetched,
// not now. Stale is never an error: the portfolio page shows the AsOf
// stamp and carries on.
type Quote struct {
	Symbol   string
	Price    decimal.Decimal
	Currency string
	AsOf     time.Time
	Stale    bool
}

// Quoter returns the latest known price per share for a market symbol.
// internal/web depends on this interface, not on the concrete HTTP
// client, so handler tests can inject a fake with no network.
type Quoter interface {
	Quote(ctx context.Context, symbol string) (Quote, error)
}
