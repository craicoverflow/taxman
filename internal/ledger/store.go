package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ErrNotManualInterest is returned by UpdateManualInterestCredit and
// DeleteManualInterestCredit when the target row id does not exist or
// is not a hand-entered interest credit. Every imported transaction
// stays immutable (SPEC.md §2); only the credits typed in on the
// dashboard can be corrected or removed.
var ErrNotManualInterest = errors.New("ledger: row is not a hand-entered interest credit")

// manualInterestPlatforms are the platforms whose interest credits are
// typed in on the dashboard rather than parsed from a CSV export —
// those savings accounts (N26, Trade Republic) have no clean export.
// They are the only transactions taxman lets the user edit or delete.
var manualInterestPlatforms = []Platform{PlatformN26, PlatformTradeRepublic}

// manualInterestPlatformArgs returns the placeholder fragment and the
// matching args for a `platform IN (...)` clause over
// manualInterestPlatforms.
func manualInterestPlatformArgs() (placeholders string, args []any) {
	parts := make([]string, len(manualInterestPlatforms))
	args = make([]any, len(manualInterestPlatforms))
	for i, p := range manualInterestPlatforms {
		parts[i] = "?"
		args[i] = string(p)
	}
	return strings.Join(parts, ", "), args
}

// ErrDuplicateInterestCredit is returned by UpdateManualInterestCredit
// when the corrected date/amount would collide with another interest
// credit already stored (same fingerprint). Nothing is changed.
var ErrDuplicateInterestCredit = errors.New("ledger: an interest credit with the same date and amount already exists")

// Store is a SQLite-backed, append-only Transaction store. Insert is
// idempotent by Fingerprint: inserting a Transaction that logically
// duplicates one already stored is a no-op, which is what makes
// re-importing an overlapping export safe. See SPEC.md §2.
type Store struct {
	db *sql.DB
}

// NewStore wraps an already-migrated *sql.DB. Callers are responsible
// for having run db.Up beforehand (see internal/db).
func NewStore(conn *sql.DB) *Store {
	return &Store{db: conn}
}

// Insert stores tx if no transaction with the same Fingerprint already
// exists. It reports whether a new row was actually inserted.
func (s *Store) Insert(tx Transaction) (inserted bool, err error) {
	result, err := s.db.Exec(
		`INSERT INTO transactions (fingerprint, platform, type, date, instrument, quantity, price, currency, source_ref, description)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(fingerprint) DO NOTHING`,
		tx.Fingerprint(),
		string(tx.Platform),
		string(tx.Type),
		tx.Date.UTC().Format(time.RFC3339),
		tx.Instrument,
		tx.Quantity.String(),
		tx.Price.String(),
		tx.Currency,
		tx.SourceRef,
		tx.Description,
	)
	if err != nil {
		return false, fmt.Errorf("inserting transaction: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking rows affected: %w", err)
	}
	return rows > 0, nil
}

// StoredInterestCredit is one hand-entered interest credit paired with
// its database row id, so the web layer can offer per-row correction
// (edit the date/amount) or deletion of a mistaken entry. Platform
// identifies which source it came from (see internal/ingest/interest).
type StoredInterestCredit struct {
	ID int64
	Transaction
}

// ManualInterestCredits returns every hand-entered interest credit
// (one of manualInterestPlatforms, type interest), most recent first,
// each with its row id. The order is deterministic: by date
// descending, then by row id descending to break ties between credits
// on the same day. These are the only transactions taxman lets the
// user edit or delete — see ErrNotManualInterest.
func (s *Store) ManualInterestCredits() ([]StoredInterestCredit, error) {
	placeholders, args := manualInterestPlatformArgs()
	args = append(args, string(TypeInterest))
	rows, err := s.db.Query(
		`SELECT id, platform, type, date, instrument, quantity, price, currency, source_ref, description
		 FROM transactions
		 WHERE platform IN (`+placeholders+`) AND type = ?
		 ORDER BY date DESC, id DESC`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("querying interest credits: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []StoredInterestCredit
	for rows.Next() {
		var (
			id                                 int64
			platform, typ, dateStr, instrument string
			quantityStr, priceStr, currency    string
			sourceRef, description             string
		)
		if err := rows.Scan(&id, &platform, &typ, &dateStr, &instrument, &quantityStr, &priceStr, &currency, &sourceRef, &description); err != nil {
			return nil, fmt.Errorf("scanning interest credit: %w", err)
		}

		date, err := time.Parse(time.RFC3339, dateStr)
		if err != nil {
			return nil, fmt.Errorf("parsing stored date %q: %w", dateStr, err)
		}
		quantity, err := decimal.NewFromString(quantityStr)
		if err != nil {
			return nil, fmt.Errorf("parsing stored quantity %q: %w", quantityStr, err)
		}
		price, err := decimal.NewFromString(priceStr)
		if err != nil {
			return nil, fmt.Errorf("parsing stored price %q: %w", priceStr, err)
		}

		out = append(out, StoredInterestCredit{
			ID: id,
			Transaction: Transaction{
				Platform:    Platform(platform),
				Type:        Type(typ),
				Date:        date,
				Instrument:  instrument,
				Quantity:    quantity,
				Price:       price,
				Currency:    currency,
				SourceRef:   sourceRef,
				Description: description,
			},
		})
	}
	return out, rows.Err()
}

// UpdateManualInterestCredit replaces the stored row identified by id
// with tx, recomputing its fingerprint. The WHERE clause pins the row
// to a hand-entered interest credit, so an imported transaction can
// never be mutated through this path: a missing row or a non-matching
// type changes nothing and returns ErrNotManualInterest. The row's
// platform and instrument are left untouched; tx must therefore be
// built for the same source (its Fingerprint depends on both), which
// is what the dashboard's edit form does via a hidden source field. If
// the new fingerprint collides with another stored credit,
// ErrDuplicateInterestCredit is returned and nothing changes.
func (s *Store) UpdateManualInterestCredit(id int64, tx Transaction) error {
	placeholders, platformArgs := manualInterestPlatformArgs()
	args := []any{
		tx.Fingerprint(),
		tx.Date.UTC().Format(time.RFC3339),
		tx.Quantity.String(),
		tx.Price.String(),
		tx.Currency,
		id,
	}
	args = append(args, platformArgs...)
	args = append(args, string(TypeInterest))
	result, err := s.db.Exec(
		`UPDATE transactions
		 SET fingerprint = ?, date = ?, quantity = ?, price = ?, currency = ?
		 WHERE id = ? AND platform IN (`+placeholders+`) AND type = ?`,
		args...,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrDuplicateInterestCredit
		}
		return fmt.Errorf("updating interest credit: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotManualInterest
	}
	return nil
}

// DeleteManualInterestCredit removes the hand-entered interest credit
// identified by id. Like UpdateManualInterestCredit it refuses to
// touch anything that isn't a hand-entered interest credit, returning
// ErrNotManualInterest when nothing matched.
func (s *Store) DeleteManualInterestCredit(id int64) error {
	placeholders, platformArgs := manualInterestPlatformArgs()
	args := []any{id}
	args = append(args, platformArgs...)
	args = append(args, string(TypeInterest))
	result, err := s.db.Exec(
		`DELETE FROM transactions
		 WHERE id = ? AND platform IN (`+placeholders+`) AND type = ?`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("deleting interest credit: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotManualInterest
	}
	return nil
}

// All returns every stored Transaction. SourceRef is not
// reconstructed from storage in insertion order beyond what SQLite's
// default row order provides; callers needing a specific order should
// sort the result.
func (s *Store) All() ([]Transaction, error) {
	rows, err := s.db.Query(
		`SELECT platform, type, date, instrument, quantity, price, currency, source_ref, description FROM transactions`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Transaction
	for rows.Next() {
		var (
			platform, typ, dateStr, instrument string
			quantityStr, priceStr, currency    string
			sourceRef, description             string
		)
		if err := rows.Scan(&platform, &typ, &dateStr, &instrument, &quantityStr, &priceStr, &currency, &sourceRef, &description); err != nil {
			return nil, fmt.Errorf("scanning transaction: %w", err)
		}

		date, err := time.Parse(time.RFC3339, dateStr)
		if err != nil {
			return nil, fmt.Errorf("parsing stored date %q: %w", dateStr, err)
		}
		quantity, err := decimal.NewFromString(quantityStr)
		if err != nil {
			return nil, fmt.Errorf("parsing stored quantity %q: %w", quantityStr, err)
		}
		price, err := decimal.NewFromString(priceStr)
		if err != nil {
			return nil, fmt.Errorf("parsing stored price %q: %w", priceStr, err)
		}

		out = append(out, Transaction{
			Platform:    Platform(platform),
			Type:        Type(typ),
			Date:        date,
			Instrument:  instrument,
			Quantity:    quantity,
			Price:       price,
			Currency:    currency,
			SourceRef:   sourceRef,
			Description: description,
		})
	}
	return out, rows.Err()
}
