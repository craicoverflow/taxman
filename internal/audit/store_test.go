package audit

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
)

func openMigratedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Up(conn, db.Migrations); err != nil {
		t.Fatalf("running migrations: %v", err)
	}
	return conn
}

func sampleRecord() Record {
	return Record{
		Instrument:         "AAPL",
		Kind:               KindCGT,
		LiabilityAmount:    decimal.RequireFromString("900.90"),
		SourceTransactions: []string{"fp-abc123", "fp-def456"},
		LotIDs:             []string{"lot-1", "lot-2"},
		DisposalDate:       time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		RuleEffectiveFrom:  time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC),
		RuleRate:           decimal.RequireFromString("0.33"),
	}
}

func TestStore_Insert_RoundTrips(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	rec := sampleRecord()

	if err := store.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.ForInstrument("AAPL")
	if err != nil {
		t.Fatalf("ForInstrument: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if !got[0].LiabilityAmount.Equal(rec.LiabilityAmount) {
		t.Errorf("LiabilityAmount = %s, want %s", got[0].LiabilityAmount, rec.LiabilityAmount)
	}
	if len(got[0].SourceTransactions) != 2 {
		t.Errorf("expected 2 source transactions round-tripped, got %d", len(got[0].SourceTransactions))
	}
	if len(got[0].LotIDs) != 2 {
		t.Errorf("expected 2 lot IDs round-tripped, got %d", len(got[0].LotIDs))
	}
	if !got[0].DisposalDate.Equal(rec.DisposalDate) {
		t.Errorf("DisposalDate = %v, want %v", got[0].DisposalDate, rec.DisposalDate)
	}
	if !got[0].RuleRate.Equal(rec.RuleRate) {
		t.Errorf("RuleRate = %s, want %s", got[0].RuleRate, rec.RuleRate)
	}
}

func TestStore_ForInstrument_OnlyReturnsMatchingInstrument(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	aapl := sampleRecord()
	other := sampleRecord()
	other.Instrument = "IE00B4L5Y983"

	if err := store.Insert(aapl); err != nil {
		t.Fatalf("Insert aapl: %v", err)
	}
	if err := store.Insert(other); err != nil {
		t.Fatalf("Insert other: %v", err)
	}

	got, err := store.ForInstrument("AAPL")
	if err != nil {
		t.Fatalf("ForInstrument: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 record for AAPL, got %d", len(got))
	}
}

func TestStore_InsertMany_RoundTrips(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	rec1 := sampleRecord()
	rec2 := sampleRecord()
	rec2.DisposalDate = time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)

	if err := store.InsertMany([]Record{rec1, rec2}); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}

	got, err := store.ForInstrument("AAPL")
	if err != nil {
		t.Fatalf("ForInstrument: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}
}
