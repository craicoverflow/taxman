package valuations

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Valuation is one user-entered market value for a holding on a given
// date: the per-unit value used to compute the gain on a deemed
// disposal falling on that date.
type Valuation struct {
	Instrument   string
	Date         time.Time       // the anniversary date the value was taken at, UTC midnight
	ValuePerUnit decimal.Decimal // in Currency, not yet restated to EUR
	Currency     string          // ISO 4217
}

// Store is a SQLite-backed table of anniversary valuations. Like
// internal/classify's override table and internal/tickers' mappings,
// it holds only what a human entered — an instrument/date with no
// entry has no value, and nothing infers one.
type Store struct {
	db *sql.DB
}

// NewStore wraps an already-migrated *sql.DB. Callers are responsible
// for having run db.Up beforehand (see internal/db).
func NewStore(conn *sql.DB) *Store {
	return &Store{db: conn}
}

// Day normalizes t to UTC midnight. Valuations are keyed by calendar
// day — an anniversary is a date, not an instant — so both writes and
// lookups go through this to keep the stored key canonical.
func Day(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

// Get returns the valuation recorded for instrument on the given day
// and true, or the zero Valuation and false when none is on record.
func (s *Store) Get(instrument string, on time.Time) (Valuation, bool, error) {
	day := Day(on)

	var valueStr, currency string
	err := s.db.QueryRow(
		`SELECT value_per_unit, currency FROM deemed_disposal_valuations
		 WHERE instrument = ? AND valuation_date = ?`,
		instrument, day.Format(time.RFC3339),
	).Scan(&valueStr, &currency)
	if err == sql.ErrNoRows {
		return Valuation{}, false, nil
	}
	if err != nil {
		return Valuation{}, false, fmt.Errorf("valuations: looking up %s on %s: %w", instrument, day.Format("2006-01-02"), err)
	}

	value, err := decimal.NewFromString(valueStr)
	if err != nil {
		return Valuation{}, false, fmt.Errorf("valuations: parsing stored value %q for %s on %s: %w", valueStr, instrument, day.Format("2006-01-02"), err)
	}

	return Valuation{
		Instrument:   instrument,
		Date:         day,
		ValuePerUnit: value,
		Currency:     currency,
	}, true, nil
}

// All returns every valuation on record, ordered by instrument then
// date, for the dashboard's entry form and for `taxman report`.
func (s *Store) All() ([]Valuation, error) {
	rows, err := s.db.Query(
		`SELECT instrument, valuation_date, value_per_unit, currency FROM deemed_disposal_valuations
		 ORDER BY instrument, valuation_date`,
	)
	if err != nil {
		return nil, fmt.Errorf("valuations: listing valuations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Valuation
	for rows.Next() {
		var instrument, dateStr, valueStr, currency string
		if err := rows.Scan(&instrument, &dateStr, &valueStr, &currency); err != nil {
			return nil, fmt.Errorf("valuations: scanning valuation: %w", err)
		}

		date, err := time.Parse(time.RFC3339, dateStr)
		if err != nil {
			return nil, fmt.Errorf("valuations: parsing stored date %q for %s: %w", dateStr, instrument, err)
		}
		value, err := decimal.NewFromString(valueStr)
		if err != nil {
			return nil, fmt.Errorf("valuations: parsing stored value %q for %s: %w", valueStr, instrument, err)
		}

		out = append(out, Valuation{
			Instrument:   instrument,
			Date:         Day(date),
			ValuePerUnit: value,
			Currency:     currency,
		})
	}
	return out, rows.Err()
}

// Upsert records v, overwriting any existing valuation for the same
// instrument and day. created_at is set on first insert and left
// untouched on overwrite, mirroring internal/tickers.
//
// A correction is a plain overwrite rather than a new row: the
// anniversary value is a single fact about a single date, and keeping
// two contradictory values for it would leave the engine picking one.
func (s *Store) Upsert(v Valuation) error {
	if v.Instrument == "" {
		return fmt.Errorf("valuations: instrument is required")
	}
	if v.Currency == "" {
		return fmt.Errorf("valuations: currency is required for %s on %s", v.Instrument, Day(v.Date).Format("2006-01-02"))
	}
	if !v.ValuePerUnit.IsPositive() {
		return fmt.Errorf("valuations: value per unit for %s on %s must be positive, got %s", v.Instrument, Day(v.Date).Format("2006-01-02"), v.ValuePerUnit)
	}

	_, err := s.db.Exec(
		`INSERT INTO deemed_disposal_valuations (instrument, valuation_date, value_per_unit, currency, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(instrument, valuation_date) DO UPDATE SET
		     value_per_unit = excluded.value_per_unit,
		     currency       = excluded.currency`,
		v.Instrument,
		Day(v.Date).Format(time.RFC3339),
		v.ValuePerUnit.String(),
		v.Currency,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("valuations: setting %s on %s: %w", v.Instrument, Day(v.Date).Format("2006-01-02"), err)
	}
	return nil
}

// ValuePerUnit satisfies engine.Valuer: it returns the value and its
// currency without the surrounding Valuation, which is all the engine
// needs to compute a deemed disposal.
func (s *Store) ValuePerUnit(instrument string, on time.Time) (decimal.Decimal, string, bool, error) {
	v, ok, err := s.Get(instrument, on)
	if err != nil || !ok {
		return decimal.Zero, "", false, err
	}
	return v.ValuePerUnit, v.Currency, true, nil
}
