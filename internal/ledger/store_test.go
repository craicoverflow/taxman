package ledger

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
)

// interestCredit builds a manually-entered-style N26 interest credit,
// matching the shape n26.NewInterestCredit produces (Quantity 1, the
// credited amount in Price).
func interestCredit(t *testing.T, date, amount string) Transaction {
	t.Helper()
	amt, err := decimal.NewFromString(amount)
	if err != nil {
		t.Fatalf("parsing amount %q: %v", amount, err)
	}
	return Transaction{
		Platform:   PlatformN26,
		Type:       TypeInterest,
		Date:       mustDate(t, date).UTC(),
		Instrument: "N26_SAVINGS",
		Quantity:   decimal.NewFromInt(1),
		Price:      amt,
		Currency:   "EUR",
	}
}

func TestStore_ManualInterestCredits_ReturnsN26InterestMostRecentFirstWithIDs(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	if _, err := store.Insert(interestCredit(t, "2024-01-15", "5.00")); err != nil {
		t.Fatalf("Insert credit 1: %v", err)
	}
	if _, err := store.Insert(interestCredit(t, "2024-06-01", "12.34")); err != nil {
		t.Fatalf("Insert credit 2: %v", err)
	}
	if _, err := store.Insert(baseTransaction(t)); err != nil { // a buy — must not appear
		t.Fatalf("Insert buy: %v", err)
	}

	got, err := store.ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 interest credits, got %d", len(got))
	}
	if got[0].Date.Before(got[1].Date) {
		t.Errorf("expected most recent first, got %v then %v", got[0].Date, got[1].Date)
	}
	for _, ic := range got {
		if ic.ID <= 0 {
			t.Errorf("expected a positive row id, got %d", ic.ID)
		}
		if ic.Type != TypeInterest || ic.Platform != PlatformN26 {
			t.Errorf("unexpected row: %+v", ic.Transaction)
		}
	}
}

func TestStore_ManualInterestCredits_SameDay_TieBrokenByRowIDDescending(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	// Three credits on the same day, distinct amounts so they don't
	// dedup by fingerprint. Inserted oldest-id first.
	for _, amt := range []string{"1.00", "2.00", "3.00"} {
		if _, err := store.Insert(interestCredit(t, "2024-05-01", amt)); err != nil {
			t.Fatalf("Insert %s: %v", amt, err)
		}
	}

	got, err := store.ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 credits, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ID <= got[i].ID {
			t.Errorf("expected row ids strictly descending on a same-day tie, got %d then %d", got[i-1].ID, got[i].ID)
		}
	}
}

func TestStore_UpdateManualInterestCredit_ChangesDateAndAmount(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	if _, err := store.Insert(interestCredit(t, "2024-03-01", "10.00")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	before, err := store.ManualInterestCredits()
	if err != nil || len(before) != 1 {
		t.Fatalf("setup: got %d credits, err %v", len(before), err)
	}

	corrected := interestCredit(t, "2024-04-02", "13.37")
	if err := store.UpdateManualInterestCredit(before[0].ID, corrected); err != nil {
		t.Fatalf("UpdateManualInterestCredit: %v", err)
	}

	after, err := store.ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("expected still 1 credit, got %d", len(after))
	}
	if !after[0].Date.Equal(mustDate(t, "2024-04-02").UTC()) {
		t.Errorf("date not updated: got %v", after[0].Date)
	}
	if !after[0].Price.Equal(decimal.RequireFromString("13.37")) {
		t.Errorf("amount not updated: got %s", after[0].Price)
	}
	if after[0].Fingerprint() != corrected.Fingerprint() {
		t.Errorf("fingerprint not recomputed: got %q want %q", after[0].Fingerprint(), corrected.Fingerprint())
	}
}

func TestStore_UpdateManualInterestCredit_RefusesNonInterestRow(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	if _, err := store.Insert(baseTransaction(t)); err != nil { // the only row — id 1
		t.Fatalf("Insert: %v", err)
	}

	err := store.UpdateManualInterestCredit(1, interestCredit(t, "2024-04-02", "13.37"))
	if !errors.Is(err, ErrNotManualInterest) {
		t.Fatalf("expected ErrNotManualInterest, got %v", err)
	}

	got, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 1 || got[0].Type != TypeBuy {
		t.Errorf("buy row should be untouched, got %+v", got)
	}
}

func TestStore_UpdateManualInterestCredit_MissingID_ReturnsErrNotManualInterest(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	err := store.UpdateManualInterestCredit(999, interestCredit(t, "2024-04-02", "13.37"))
	if !errors.Is(err, ErrNotManualInterest) {
		t.Fatalf("expected ErrNotManualInterest, got %v", err)
	}
}

func TestStore_UpdateManualInterestCredit_DuplicateTarget_ReturnsErrDuplicate(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	if _, err := store.Insert(interestCredit(t, "2024-01-01", "1.00")); err != nil {
		t.Fatalf("Insert A: %v", err)
	}
	if _, err := store.Insert(interestCredit(t, "2024-02-02", "2.00")); err != nil {
		t.Fatalf("Insert B: %v", err)
	}

	credits, err := store.ManualInterestCredits()
	if err != nil || len(credits) != 2 {
		t.Fatalf("setup: got %d credits, err %v", len(credits), err)
	}

	// Find B (2024-02-02) regardless of list order, then edit it to be
	// identical to A.
	var bID int64
	for _, c := range credits {
		if c.Date.Equal(mustDate(t, "2024-02-02").UTC()) {
			bID = c.ID
		}
	}
	if bID == 0 {
		t.Fatal("setup: could not find credit B")
	}

	err = store.UpdateManualInterestCredit(bID, interestCredit(t, "2024-01-01", "1.00"))
	if !errors.Is(err, ErrDuplicateInterestCredit) {
		t.Fatalf("expected ErrDuplicateInterestCredit, got %v", err)
	}

	after, err := store.ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(after) != 2 {
		t.Errorf("no row should have been changed, got %d credits", len(after))
	}
}

func TestStore_DeleteManualInterestCredit_RemovesRow(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	if _, err := store.Insert(interestCredit(t, "2024-03-01", "10.00")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	credits, err := store.ManualInterestCredits()
	if err != nil || len(credits) != 1 {
		t.Fatalf("setup: got %d credits, err %v", len(credits), err)
	}

	if err := store.DeleteManualInterestCredit(credits[0].ID); err != nil {
		t.Fatalf("DeleteManualInterestCredit: %v", err)
	}

	after, err := store.ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected 0 credits after delete, got %d", len(after))
	}
}

// tradeRepublicInterestCredit is the Trade Republic analogue of
// interestCredit — a different manual-interest platform/instrument,
// so the store's editable/deletable query has to match it too.
func tradeRepublicInterestCredit(t *testing.T, date, amount string) Transaction {
	t.Helper()
	amt, err := decimal.NewFromString(amount)
	if err != nil {
		t.Fatalf("parsing amount %q: %v", amount, err)
	}
	return Transaction{
		Platform:   PlatformTradeRepublic,
		Type:       TypeInterest,
		Date:       mustDate(t, date).UTC(),
		Instrument: "TRADE_REPUBLIC_SAVINGS",
		Quantity:   decimal.NewFromInt(1),
		Price:      amt,
		Currency:   "EUR",
	}
}

func TestStore_ManualInterestCredits_IncludesTradeRepublicAndSupportsEditDelete(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	if _, err := store.Insert(interestCredit(t, "2024-01-15", "5.00")); err != nil {
		t.Fatalf("Insert N26 credit: %v", err)
	}
	if _, err := store.Insert(tradeRepublicInterestCredit(t, "2024-06-01", "9.99")); err != nil {
		t.Fatalf("Insert Trade Republic credit: %v", err)
	}

	got, err := store.ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both manual-interest platforms listed, got %d", len(got))
	}

	// The most recent is the Trade Republic one; it must be editable
	// and deletable through the same paths as an N26 credit.
	tr := got[0]
	if tr.Platform != PlatformTradeRepublic {
		t.Fatalf("expected the Trade Republic credit first, got %s", tr.Platform)
	}
	if err := store.UpdateManualInterestCredit(tr.ID, tradeRepublicInterestCredit(t, "2024-06-02", "10.01")); err != nil {
		t.Fatalf("UpdateManualInterestCredit (Trade Republic): %v", err)
	}
	if err := store.DeleteManualInterestCredit(tr.ID); err != nil {
		t.Fatalf("DeleteManualInterestCredit (Trade Republic): %v", err)
	}

	after, err := store.ManualInterestCredits()
	if err != nil {
		t.Fatalf("ManualInterestCredits: %v", err)
	}
	if len(after) != 1 || after[0].Platform != PlatformN26 {
		t.Errorf("expected only the N26 credit to remain, got %+v", after)
	}
}

func TestStore_DeleteManualInterestCredit_RefusesNonInterestRow(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	if _, err := store.Insert(baseTransaction(t)); err != nil { // the only row — id 1
		t.Fatalf("Insert: %v", err)
	}

	err := store.DeleteManualInterestCredit(1)
	if !errors.Is(err, ErrNotManualInterest) {
		t.Fatalf("expected ErrNotManualInterest, got %v", err)
	}
	got, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("buy row should be untouched, got %d rows", len(got))
	}
}

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

func TestStore_Insert_RoundTrips(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	tx := baseTransaction(t)

	if _, err := store.Insert(tx); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 stored transaction, got %d", len(got))
	}
	if got[0].Fingerprint() != tx.Fingerprint() {
		t.Errorf("round-tripped transaction has a different fingerprint: got %q, want %q", got[0].Fingerprint(), tx.Fingerprint())
	}
	if !got[0].Quantity.Equal(tx.Quantity) {
		t.Errorf("round-tripped Quantity = %s, want %s", got[0].Quantity, tx.Quantity)
	}
	if !got[0].Price.Equal(tx.Price) {
		t.Errorf("round-tripped Price = %s, want %s", got[0].Price, tx.Price)
	}
	if !got[0].Date.Equal(tx.Date) {
		t.Errorf("round-tripped Date = %v, want %v", got[0].Date, tx.Date)
	}
	if got[0].Description != tx.Description {
		t.Errorf("round-tripped Description = %q, want %q", got[0].Description, tx.Description)
	}
}

func TestStore_Insert_DuplicateFingerprint_IsIdempotent(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	tx := baseTransaction(t)

	if _, err := store.Insert(tx); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	// Same logical transaction, different SourceRef - simulates the
	// same trade appearing in two overlapping exports under a
	// different broker note ID.
	dup := tx
	dup.SourceRef = "a-totally-different-order-id"
	if _, err := store.Insert(dup); err != nil {
		t.Fatalf("duplicate Insert should not error, got: %v", err)
	}

	got, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 stored row after inserting a duplicate, got %d", len(got))
	}
}

func TestStore_Insert_ReturnsWasInserted(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	tx := baseTransaction(t)

	inserted, err := store.Insert(tx)
	if err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	if !inserted {
		t.Error("expected first Insert of a new transaction to report inserted=true")
	}

	inserted, err = store.Insert(tx)
	if err != nil {
		t.Fatalf("second Insert: %v", err)
	}
	if inserted {
		t.Error("expected duplicate Insert to report inserted=false")
	}
}

func TestStore_Insert_DistinctTransactions_BothStored(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	a := baseTransaction(t)
	b := baseTransaction(t)
	b.Instrument = "IE00FIXTURE04"

	if _, err := store.Insert(a); err != nil {
		t.Fatalf("Insert a: %v", err)
	}
	if _, err := store.Insert(b); err != nil {
		t.Fatalf("Insert b: %v", err)
	}

	got, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 distinct stored transactions, got %d", len(got))
	}
}
