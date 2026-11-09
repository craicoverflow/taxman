package ledger

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotManualTrade is returned by UpdateManualTrade and
// DeleteManualTrade when the target row id does not exist or is not a
// hand-entered buy or sell. Imported rows and manual interest credits
// can never be changed through these paths (SPEC.md §2, §18).
var ErrNotManualTrade = errors.New("ledger: row is not a hand-entered trade")

// ErrDuplicateManualTrade is returned by UpdateManualTrade when the
// corrected values would collide with another stored transaction (same
// fingerprint). Nothing is changed.
var ErrDuplicateManualTrade = errors.New("ledger: a trade with the same values already exists")

// StoredTrade is a hand-entered buy or sell with its row id, so the
// dashboard can target it for edit or delete. Instrument is the stored
// key, not alias-resolved.
type StoredTrade struct {
	ID int64
	Transaction
}

// ManualTrades returns every hand-entered buy or sell (PlatformManual,
// type buy or sell), most recent first.
func (s *Store) ManualTrades() ([]StoredTrade, error) {
	rows, err := s.queryManual(TypeBuy, TypeSell)
	if err != nil {
		return nil, fmt.Errorf("querying manual trades: %w", err)
	}
	out := make([]StoredTrade, 0, len(rows))
	for _, r := range rows {
		out = append(out, StoredTrade(r))
	}
	return out, nil
}

// UpdateManualTrade replaces the stored row identified by id with tx,
// recomputing its fingerprint. The WHERE clause pins the row to a
// hand-entered buy or sell, so an imported transaction or a manual
// interest credit is never mutated: no match returns ErrNotManualTrade.
// A fingerprint collision returns ErrDuplicateManualTrade.
func (s *Store) UpdateManualTrade(id int64, tx Transaction) error {
	result, err := s.db.Exec(
		`UPDATE transactions
		 SET fingerprint = ?, type = ?, date = ?, instrument = ?, quantity = ?, price = ?, currency = ?
		 WHERE id = ? AND platform = ? AND type IN (?, ?)`,
		tx.Fingerprint(),
		string(tx.Type),
		tx.Date.UTC().Format(time.RFC3339),
		tx.Instrument,
		tx.Quantity.String(),
		tx.Price.String(),
		tx.Currency,
		id, string(PlatformManual), string(TypeBuy), string(TypeSell),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrDuplicateManualTrade
		}
		return fmt.Errorf("updating manual trade: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotManualTrade
	}
	return nil
}

// DeleteManualTrade removes the hand-entered buy or sell identified by
// id, returning ErrNotManualTrade when nothing matched.
func (s *Store) DeleteManualTrade(id int64) error {
	result, err := s.db.Exec(
		`DELETE FROM transactions WHERE id = ? AND platform = ? AND type IN (?, ?)`,
		id, string(PlatformManual), string(TypeBuy), string(TypeSell),
	)
	if err != nil {
		return fmt.Errorf("deleting manual trade: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotManualTrade
	}
	return nil
}
