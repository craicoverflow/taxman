package aliases_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/aliases"
	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/engine"
	"github.com/craicoverflow/taxman/internal/ledger"
)

const (
	isin   = "IE00B5BMR087"
	ticker = "SXR8"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Up(conn, db.Migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

func buy(t *testing.T, instrument, date string, qty, price int64) ledger.Transaction {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Transaction{
		Platform: ledger.PlatformDegiro, Type: ledger.TypeBuy, Date: d, Instrument: instrument,
		Quantity: decimal.NewFromInt(qty), Price: decimal.NewFromInt(price), Currency: "EUR",
	}
}

func TestMerge_LotsFromBothKeysShareOneFIFOQueue(t *testing.T) {
	conn := openDB(t)
	ls := ledger.NewStore(conn)
	for _, tx := range []ledger.Transaction{
		buy(t, isin, "2023-01-10", 10, 100),  // oldest: ISIN-keyed
		buy(t, ticker, "2023-06-10", 5, 120), // later: ticker-keyed
		buy(t, isin, "2024-01-10", 2, 140),   // newest: ISIN-keyed
	} {
		if _, err := ls.Insert(tx); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	if err := aliases.NewStore(conn).Merge(ticker, isin); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	all, err := ls.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, tx := range all {
		if tx.Instrument != isin {
			t.Errorf("transaction read as %q, want %q", tx.Instrument, isin)
		}
	}
	pos, err := engine.OpenPositions(all)
	if err != nil {
		t.Fatalf("OpenPositions: %v", err)
	}
	if !pos.Quantity.Equal(decimal.NewFromInt(17)) {
		t.Errorf("quantity = %s, want 17", pos.Quantity)
	}
	// 10*100 + 5*120 + 2*140 = 1880
	if !pos.CostBasis.Equal(decimal.NewFromInt(1880)) {
		t.Errorf("cost basis = %s, want 1880", pos.CostBasis)
	}

	// Stored rows keep their own key, so re-importing stays idempotent.
	inserted, err := ls.Insert(buy(t, ticker, "2023-06-10", 5, 120))
	if err != nil || inserted {
		t.Errorf("re-insert under original key: inserted=%v err=%v, want false/nil", inserted, err)
	}

	if err := aliases.NewStore(conn).Unmerge(ticker); err != nil {
		t.Fatalf("Unmerge: %v", err)
	}
	all, _ = ls.All()
	seen := map[string]int{}
	for _, tx := range all {
		seen[tx.Instrument]++
	}
	if seen[ticker] != 1 || seen[isin] != 2 {
		t.Errorf("after Unmerge: %v, want 1 under ticker and 2 under ISIN", seen)
	}
}

func TestMerge_Refusals(t *testing.T) {
	conn := openDB(t)
	s := aliases.NewStore(conn)
	cs := classify.NewStore(conn)

	if err := s.Merge("A", "A"); !errors.Is(err, aliases.ErrSelf) {
		t.Errorf("self merge: %v", err)
	}

	if err := s.Merge("A", "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.Merge("B", "C"); !errors.Is(err, aliases.ErrChain) {
		t.Errorf("merging a canonical key away: %v", err)
	}
	if err := s.Merge("C", "A"); !errors.Is(err, aliases.ErrChain) {
		t.Errorf("merging into an alias: %v", err)
	}

	_ = cs.Set("X", classify.CGTAsset)
	_ = cs.Set("Y", classify.ExitTaxFund)
	if err := s.Merge("X", "Y"); !errors.Is(err, aliases.ErrClassification) {
		t.Errorf("mismatched classification: %v", err)
	}
	if err := s.Merge("X", "Z"); !errors.Is(err, aliases.ErrClassification) {
		t.Errorf("classified alias into unclassified canonical: %v", err)
	}
	_ = cs.Set("Z", classify.CGTAsset)
	if err := s.Merge("X", "Z"); err != nil {
		t.Errorf("matching classification: %v", err)
	}
}

func TestListUnclassified_ReadsAliasAsCanonical(t *testing.T) {
	conn := openDB(t)
	ls := ledger.NewStore(conn)
	_, _ = ls.Insert(buy(t, isin, "2023-01-10", 1, 1))
	_, _ = ls.Insert(buy(t, ticker, "2023-02-10", 1, 1))
	if err := classify.NewStore(conn).Set(isin, classify.ExitTaxFund); err != nil {
		t.Fatal(err)
	}
	if err := aliases.NewStore(conn).Merge(ticker, isin); err != nil {
		t.Fatal(err)
	}
	got, err := classify.NewStore(conn).ListUnclassified()
	if err != nil || len(got) != 0 {
		t.Errorf("ListUnclassified = %v, %v; want none", got, err)
	}
}
