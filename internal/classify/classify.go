package classify

import (
	"database/sql"
	"fmt"
)

// Classification is a holding's tax treatment: whether disposals are
// taxed under the CGT regime or the exit-tax/deemed-disposal regime.
// See SPEC.md §2.
type Classification string

const (
	// Unclassified is the default for any holding with no manual
	// override on record. Callers must never treat Unclassified as
	// equivalent to either real classification — see internal/engine's
	// guard, which blocks computation for unclassified holdings rather
	// than guessing.
	Unclassified Classification = "UNCLASSIFIED"
	CGTAsset     Classification = "CGT_ASSET"
	ExitTaxFund  Classification = "EXIT_TAX_FUND"
)

// ValidClassifications is the set of Classification values settable
// via Set — i.e. everything except Unclassified, which is the
// absence of an override, not something chosen explicitly. Shared by
// the CLI (`taxman classify --set`) and the web dashboard's
// classification form so both accept exactly the same strings.
var ValidClassifications = map[string]Classification{
	"CGT_ASSET":     CGTAsset,
	"EXIT_TAX_FUND": ExitTaxFund,
}

// Store is a SQLite-backed manual override table mapping a holding
// (by instrument identifier — ISIN or platform-specific ID) to its
// Classification. There is no heuristic auto-classification in v1:
// an instrument with no entry is Unclassified, full stop. See
// SPEC.md §2 and the idea doc's "flag rather than guess" principle.
type Store struct {
	db *sql.DB
}

// NewStore wraps an already-migrated *sql.DB. Callers are responsible
// for having run db.Up beforehand (see internal/db).
func NewStore(conn *sql.DB) *Store {
	return &Store{db: conn}
}

// Classify returns the Classification on record for instrument, or
// Unclassified if no override has been set.
func (s *Store) Classify(instrument string) (Classification, error) {
	var classification string
	err := s.db.QueryRow(
		`SELECT classification FROM holding_classifications WHERE instrument = ?`,
		instrument,
	).Scan(&classification)
	if err == sql.ErrNoRows {
		return Unclassified, nil
	}
	if err != nil {
		return "", fmt.Errorf("classify: looking up %s: %w", instrument, err)
	}
	return Classification(classification), nil
}

// Set records instrument's Classification, overwriting any existing
// entry.
func (s *Store) Set(instrument string, classification Classification) error {
	_, err := s.db.Exec(
		`INSERT INTO holding_classifications (instrument, classification) VALUES (?, ?)
		 ON CONFLICT(instrument) DO UPDATE SET classification = excluded.classification`,
		instrument, string(classification),
	)
	if err != nil {
		return fmt.Errorf("classify: setting %s: %w", instrument, err)
	}
	return nil
}

// ListUnclassified returns every instrument that appears in the
// ledger's transactions but has no classification override on
// record.
func (s *Store) ListUnclassified() ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT t.instrument
		FROM transactions t
		LEFT JOIN holding_classifications hc ON hc.instrument = t.instrument
		WHERE hc.instrument IS NULL
		ORDER BY t.instrument
	`)
	if err != nil {
		return nil, fmt.Errorf("classify: listing unclassified holdings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var instrument string
		if err := rows.Scan(&instrument); err != nil {
			return nil, fmt.Errorf("classify: scanning unclassified holding: %w", err)
		}
		out = append(out, instrument)
	}
	return out, rows.Err()
}
