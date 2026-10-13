// Package aliases lets the user declare that two instrument keys are
// one security — typically an ISIN from one platform's export and a
// ticker from another's (SPEC.md §17). Stored transactions are never
// rewritten; ledger.Store.All reads an aliased key as its canonical
// key, so lots from both interleave in one FIFO queue. Entries are
// user-entered only: taxman never infers that two keys match.
package aliases

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrSelf           = errors.New("aliases: an instrument cannot be merged into itself")
	ErrChain          = errors.New("aliases: merge target is itself merged into another instrument, or is the target of other merges")
	ErrClassification = errors.New("aliases: the instruments have different classifications; classify them the same first")
)

// Store is a SQLite-backed instrument_aliases table.
type Store struct {
	db *sql.DB
}

// NewStore wraps an already-migrated *sql.DB.
func NewStore(conn *sql.DB) *Store {
	return &Store{db: conn}
}

// All returns alias → canonical for every entry on record.
func (s *Store) All() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT alias, canonical FROM instrument_aliases`)
	if err != nil {
		return nil, fmt.Errorf("aliases: listing: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var a, c string
		if err := rows.Scan(&a, &c); err != nil {
			return nil, fmt.Errorf("aliases: scanning: %w", err)
		}
		out[a] = c
	}
	return out, rows.Err()
}

// Merge records that alias is read as canonical. It refuses a
// self-merge, a chain (either key already on the other side of an
// entry), and instruments whose recorded classifications disagree —
// the merged holding would otherwise silently take one of them.
// A classification on alias with none on canonical is also refused:
// canonical would become UNCLASSIFIED and block computation, so the
// user classifies canonical first.
func (s *Store) Merge(alias, canonical string) error {
	if alias == canonical {
		return ErrSelf
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("aliases: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM instrument_aliases
		 WHERE alias = ? OR canonical = ? OR (canonical = ? AND alias <> ?)`,
		canonical, alias, alias, alias,
	).Scan(&n); err != nil {
		return fmt.Errorf("aliases: checking chains: %w", err)
	}
	if n > 0 {
		return ErrChain
	}

	var aliasClass, canonClass sql.NullString
	if err := tx.QueryRow(`SELECT classification FROM holding_classifications WHERE instrument = ?`, alias).Scan(&aliasClass); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("aliases: reading classification: %w", err)
	}
	if err := tx.QueryRow(`SELECT classification FROM holding_classifications WHERE instrument = ?`, canonical).Scan(&canonClass); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("aliases: reading classification: %w", err)
	}
	if aliasClass.Valid && aliasClass != canonClass {
		return ErrClassification
	}

	if _, err := tx.Exec(
		`INSERT INTO instrument_aliases (alias, canonical, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(alias) DO UPDATE SET canonical = excluded.canonical`,
		alias, canonical, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("aliases: merging %s: %w", alias, err)
	}
	return tx.Commit()
}

// Unmerge removes alias's entry; its transactions read under their own
// key again. A missing entry is not an error.
func (s *Store) Unmerge(alias string) error {
	if _, err := s.db.Exec(`DELETE FROM instrument_aliases WHERE alias = ?`, alias); err != nil {
		return fmt.Errorf("aliases: unmerging %s: %w", alias, err)
	}
	return nil
}
