package ledger

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

// manualTrade builds a hand-entered buy or sell, the shape
// manualtrade.NewTrade produces.
func manualTrade(t *testing.T, typ Type, instrument, date, qty, price string) Transaction {
	t.Helper()
	return Transaction{
		Platform:   PlatformManual,
		Type:       typ,
		Date:       mustDate(t, date).UTC(),
		Instrument: instrument,
		Quantity:   decimal.RequireFromString(qty),
		Price:      decimal.RequireFromString(price),
		Currency:   "EUR",
	}
}

func TestStore_ManualTrades_ListsOnlyManualBuysAndSellsNewestFirst(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))
	for _, tx := range []Transaction{
		baseTransaction(t),                       // imported buy: must not be listed
		interestCredit(t, "2024-05-01", "10.00"), // manual interest: must not be listed
		manualTrade(t, TypeBuy, "ACME", "2024-01-10", "10", "5"),
		manualTrade(t, TypeSell, "ACME", "2024-02-10", "4", "7"),
	} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	got, err := store.ManualTrades()
	if err != nil {
		t.Fatalf("ManualTrades: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d manual trades, want 2: %+v", len(got), got)
	}
	if got[0].Type != TypeSell || got[1].Type != TypeBuy {
		t.Errorf("want newest first (sell, buy), got %s, %s", got[0].Type, got[1].Type)
	}
	if got[0].ID == 0 {
		t.Error("rows must carry their id")
	}
}

func TestStore_UpdateManualTrade_ChangesFieldsAndRecomputesFingerprint(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))
	if _, err := store.Insert(manualTrade(t, TypeBuy, "ACME", "2024-01-10", "10", "5")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	before, _ := store.ManualTrades()

	corrected := manualTrade(t, TypeBuy, "ACME", "2024-01-11", "12", "5.5")
	if err := store.UpdateManualTrade(before[0].ID, corrected); err != nil {
		t.Fatalf("UpdateManualTrade: %v", err)
	}

	after, err := store.ManualTrades()
	if err != nil || len(after) != 1 {
		t.Fatalf("after update: %d rows, err %v", len(after), err)
	}
	if !after[0].Quantity.Equal(decimal.NewFromInt(12)) || !after[0].Price.Equal(decimal.RequireFromString("5.5")) {
		t.Errorf("fields not updated: %+v", after[0].Transaction)
	}
	if after[0].Fingerprint() != corrected.Fingerprint() {
		t.Errorf("fingerprint not recomputed: got %q want %q", after[0].Fingerprint(), corrected.Fingerprint())
	}
}

func TestStore_UpdateManualTrade_RefusesImportedRowAndManualInterest(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))
	if _, err := store.Insert(baseTransaction(t)); err != nil { // id 1, imported buy
		t.Fatalf("Insert: %v", err)
	}
	if _, err := store.Insert(interestCredit(t, "2024-05-01", "10.00")); err != nil { // id 2
		t.Fatalf("Insert: %v", err)
	}

	for _, id := range []int64{1, 2, 999} {
		err := store.UpdateManualTrade(id, manualTrade(t, TypeBuy, "ACME", "2024-01-10", "1", "1"))
		if !errors.Is(err, ErrNotManualTrade) {
			t.Errorf("id %d: want ErrNotManualTrade, got %v", id, err)
		}
	}
	all, _ := store.All()
	if len(all) != 2 {
		t.Errorf("rows changed: %+v", all)
	}
}

func TestStore_UpdateManualTrade_Collision_ReturnsErrDuplicateManualTrade(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))
	for _, tx := range []Transaction{
		manualTrade(t, TypeBuy, "ACME", "2024-01-10", "10", "5"),
		manualTrade(t, TypeBuy, "ACME", "2024-01-11", "10", "5"),
	} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	rows, _ := store.ManualTrades() // newest first: [01-11, 01-10]

	err := store.UpdateManualTrade(rows[0].ID, manualTrade(t, TypeBuy, "ACME", "2024-01-10", "10", "5"))
	if !errors.Is(err, ErrDuplicateManualTrade) {
		t.Fatalf("want ErrDuplicateManualTrade, got %v", err)
	}
}

func TestStore_DeleteManualTrade_RemovesRowAndRefusesOthers(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))
	if _, err := store.Insert(baseTransaction(t)); err != nil { // id 1, imported
		t.Fatalf("Insert: %v", err)
	}
	if _, err := store.Insert(interestCredit(t, "2024-05-01", "10.00")); err != nil { // id 2
		t.Fatalf("Insert: %v", err)
	}
	if _, err := store.Insert(manualTrade(t, TypeBuy, "ACME", "2024-01-10", "10", "5")); err != nil { // id 3
		t.Fatalf("Insert: %v", err)
	}

	for _, id := range []int64{1, 2, 999} {
		if err := store.DeleteManualTrade(id); !errors.Is(err, ErrNotManualTrade) {
			t.Errorf("id %d: want ErrNotManualTrade, got %v", id, err)
		}
	}
	if err := store.DeleteManualTrade(3); err != nil {
		t.Fatalf("DeleteManualTrade(3): %v", err)
	}
	all, _ := store.All()
	if len(all) != 2 {
		t.Errorf("want 2 rows left (imported buy, interest), got %d", len(all))
	}
}
