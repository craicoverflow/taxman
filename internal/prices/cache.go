package prices

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Cache is a durable last-known-price store. HTTPQuoter writes a quote
// to it after every successful provider fetch and reads from it when
// its in-process cache misses — so a process restart during a spell of
// provider 429s still values the portfolio from the last prices seen,
// instead of going blank until the rate limit clears.
//
// Implementations must be safe for concurrent use. A Load or Store
// error is never fatal to a quote: HTTPQuoter treats the cache as
// best-effort and carries on with the live providers.
type Cache interface {
	// Load returns the stored quote for symbol and the time it was
	// fetched from a provider. ok is false when nothing is stored.
	Load(symbol string) (quote Quote, fetchedAt time.Time, ok bool, err error)
	// Store records symbol's latest quote, overwriting any prior row.
	Store(symbol string, quote Quote, fetchedAt time.Time) error
}

// SQLiteCache is the price_quotes-table implementation of Cache (see
// migration 0006). It holds one row per symbol: the most recent price,
// its currency, and when it was fetched.
type SQLiteCache struct {
	db *sql.DB
}

// NewSQLiteCache wraps an already-migrated *sql.DB.
func NewSQLiteCache(conn *sql.DB) *SQLiteCache {
	return &SQLiteCache{db: conn}
}

// Load reads symbol's row. A missing row is (Quote{}, zero, false, nil).
func (c *SQLiteCache) Load(symbol string) (Quote, time.Time, bool, error) {
	var priceStr, currency, fetchedStr string
	err := c.db.QueryRow(
		`SELECT price, currency, fetched_at FROM price_quotes WHERE symbol = ?`,
		symbol,
	).Scan(&priceStr, &currency, &fetchedStr)
	if err == sql.ErrNoRows {
		return Quote{}, time.Time{}, false, nil
	}
	if err != nil {
		return Quote{}, time.Time{}, false, fmt.Errorf("prices: loading cached %s: %w", symbol, err)
	}

	price, err := decimal.NewFromString(priceStr)
	if err != nil {
		return Quote{}, time.Time{}, false, fmt.Errorf("prices: cached price %q for %s: %w", priceStr, symbol, err)
	}
	fetchedAt, err := time.Parse(time.RFC3339, fetchedStr)
	if err != nil {
		return Quote{}, time.Time{}, false, fmt.Errorf("prices: cached fetched_at %q for %s: %w", fetchedStr, symbol, err)
	}

	return Quote{
		Symbol:   symbol,
		Price:    price,
		Currency: currency,
		AsOf:     fetchedAt,
	}, fetchedAt, true, nil
}

// Store upserts symbol's row.
func (c *SQLiteCache) Store(symbol string, quote Quote, fetchedAt time.Time) error {
	_, err := c.db.Exec(
		`INSERT INTO price_quotes (symbol, price, currency, fetched_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(symbol) DO UPDATE SET
		   price = excluded.price,
		   currency = excluded.currency,
		   fetched_at = excluded.fetched_at`,
		symbol, quote.Price.String(), quote.Currency, fetchedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("prices: caching %s: %w", symbol, err)
	}
	return nil
}
