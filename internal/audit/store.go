package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Store is a SQLite-backed store for Records.
type Store struct {
	db *sql.DB
}

// NewStore wraps an already-migrated *sql.DB. Callers are responsible
// for having run db.Up beforehand (see internal/db).
func NewStore(conn *sql.DB) *Store {
	return &Store{db: conn}
}

// Insert stores rec.
func (s *Store) Insert(rec Record) error {
	return s.InsertMany([]Record{rec})
}

// InsertMany stores every Record in recs within a single transaction.
func (s *Store) InsertMany(recs []Record) error {
	if len(recs) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("audit: beginning transaction: %w", err)
	}

	for _, rec := range recs {
		sourceTxJSON, err := json.Marshal(rec.SourceTransactions)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("audit: marshaling source transactions: %w", err)
		}
		lotIDsJSON, err := json.Marshal(rec.LotIDs)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("audit: marshaling lot IDs: %w", err)
		}

		_, err = tx.Exec(
			`INSERT INTO audit_records (instrument, kind, liability_amount, source_transactions, lot_ids, disposal_date, rule_effective_from, rule_rate)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.Instrument,
			string(rec.Kind),
			rec.LiabilityAmount.String(),
			string(sourceTxJSON),
			string(lotIDsJSON),
			rec.DisposalDate.UTC().Format(time.RFC3339),
			rec.RuleEffectiveFrom.UTC().Format(time.RFC3339),
			rec.RuleRate.String(),
		)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("audit: inserting record for %s: %w", rec.Instrument, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("audit: committing: %w", err)
	}
	return nil
}

// ForInstrument returns every Record on file for instrument.
func (s *Store) ForInstrument(instrument string) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT instrument, kind, liability_amount, source_transactions, lot_ids, disposal_date, rule_effective_from, rule_rate
		 FROM audit_records WHERE instrument = ?`,
		instrument,
	)
	if err != nil {
		return nil, fmt.Errorf("audit: querying records for %s: %w", instrument, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Record
	for rows.Next() {
		var (
			rec                                   Record
			kind                                  string
			liabilityStr, ruleRateStr             string
			sourceTxJSON, lotIDsJSON              string
			disposalDateStr, ruleEffectiveFromStr string
		)
		if err := rows.Scan(&rec.Instrument, &kind, &liabilityStr, &sourceTxJSON, &lotIDsJSON, &disposalDateStr, &ruleEffectiveFromStr, &ruleRateStr); err != nil {
			return nil, fmt.Errorf("audit: scanning record: %w", err)
		}
		rec.Kind = Kind(kind)

		rec.LiabilityAmount, err = decimal.NewFromString(liabilityStr)
		if err != nil {
			return nil, fmt.Errorf("audit: parsing stored liability_amount %q: %w", liabilityStr, err)
		}
		rec.RuleRate, err = decimal.NewFromString(ruleRateStr)
		if err != nil {
			return nil, fmt.Errorf("audit: parsing stored rule_rate %q: %w", ruleRateStr, err)
		}

		if err := json.Unmarshal([]byte(sourceTxJSON), &rec.SourceTransactions); err != nil {
			return nil, fmt.Errorf("audit: parsing stored source_transactions: %w", err)
		}
		if err := json.Unmarshal([]byte(lotIDsJSON), &rec.LotIDs); err != nil {
			return nil, fmt.Errorf("audit: parsing stored lot_ids: %w", err)
		}

		rec.DisposalDate, err = time.Parse(time.RFC3339, disposalDateStr)
		if err != nil {
			return nil, fmt.Errorf("audit: parsing stored disposal_date %q: %w", disposalDateStr, err)
		}
		rec.RuleEffectiveFrom, err = time.Parse(time.RFC3339, ruleEffectiveFromStr)
		if err != nil {
			return nil, fmt.Errorf("audit: parsing stored rule_effective_from %q: %w", ruleEffectiveFromStr, err)
		}

		out = append(out, rec)
	}
	return out, rows.Err()
}
