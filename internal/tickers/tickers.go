package tickers

import (
	"database/sql"
	"fmt"
	"time"
)

// Store is a SQLite-backed table mapping a holding (by instrument
// identifier — ISIN or platform-specific ID) to a market symbol. Like
// internal/classify's override table there is no heuristic: an
// instrument with no entry simply has no price on the portfolio page.
type Store struct {
	db *sql.DB
}

// NewStore wraps an already-migrated *sql.DB. Callers are responsible
// for having run db.Up beforehand (see internal/db).
func NewStore(conn *sql.DB) *Store {
	return &Store{db: conn}
}

// Get returns the symbol mapped to instrument and true, or "" and
// false when no mapping is on record.
func (s *Store) Get(instrument string) (string, bool, error) {
	var symbol string
	err := s.db.QueryRow(
		`SELECT symbol FROM instrument_tickers WHERE instrument = ?`,
		instrument,
	).Scan(&symbol)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("tickers: looking up %s: %w", instrument, err)
	}
	return symbol, true, nil
}

// All returns every instrument to symbol mapping on record.
func (s *Store) All() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT instrument, symbol FROM instrument_tickers`)
	if err != nil {
		return nil, fmt.Errorf("tickers: listing mappings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var instrument, symbol string
		if err := rows.Scan(&instrument, &symbol); err != nil {
			return nil, fmt.Errorf("tickers: scanning mapping: %w", err)
		}
		out[instrument] = symbol
	}
	return out, rows.Err()
}

// Upsert records instrument's symbol, overwriting any existing entry.
// created_at is set on first insert and left untouched on overwrite.
func (s *Store) Upsert(instrument, symbol string) error {
	_, err := s.db.Exec(
		`INSERT INTO instrument_tickers (instrument, symbol, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(instrument) DO UPDATE SET symbol = excluded.symbol`,
		instrument, symbol, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("tickers: setting %s: %w", instrument, err)
	}
	return nil
}
