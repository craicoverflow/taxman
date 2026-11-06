package ledger

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

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

func TestStore_ManualTrades_ListsOnlyManualBuysAndSellsMostRecentFirst(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	for _, tx := range []Transaction{
		manualTrade(t, TypeBuy, "VWCE", "2024-01-15", "5", "80"),
		manualTrade(t, TypeSell, "VWCE", "2024-06-01", "2", "90"),
		baseTransaction(t),                                          // imported: never listed
		interestCredit(t, "2024-03-01", "10.00"),                    // manual, but interest
		manualTrade(t, TypeRSUVest, "ACME", "2024-02-01", "1", "1"), // manual, but not a buy or sell
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
		t.Fatalf("ManualTrades = %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Type != TypeSell || got[1].Type != TypeBuy {
		t.Errorf("order = %s, %s; want most recent first (sell, buy)", got[0].Type, got[1].Type)
	}
	if got[0].ID == 0 || got[1].ID == 0 {
		t.Errorf("rows must carry their ids: %+v", got)
	}
}

func TestStore_ManualTrades_ReturnsTheStoredInstrumentKeyNotTheAliasTarget(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	if _, err := store.Insert(manualTrade(t, TypeBuy, "VWCE", "2024-01-15", "5", "80")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO instrument_aliases (alias, canonical, created_at) VALUES ('VWCE', 'IE00BK5BQT80', '2024-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("alias: %v", err)
	}

	got, err := store.ManualTrades()
	if err != nil || len(got) != 1 {
		t.Fatalf("ManualTrades = %v, %v", got, err)
	}
	if got[0].Instrument != "VWCE" {
		t.Errorf("Instrument = %q, want the raw stored key VWCE (the fingerprint depends on it)", got[0].Instrument)
	}
}

func TestStore_UpdateManualTrade_ChangesFieldsAndRecomputesFingerprint(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	if _, err := store.Insert(manualTrade(t, TypeBuy, "VWCE", "2024-01-15", "5", "80")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	before, _ := store.ManualTrades()

	corrected := manualTrade(t, TypeSell, "VWCE", "2024-02-02", "4", "85.5")
	if err := store.UpdateManualTrade(before[0].ID, corrected); err != nil {
		t.Fatalf("UpdateManualTrade: %v", err)
	}

	after, err := store.ManualTrades()
	if err != nil || len(after) != 1 {
		t.Fatalf("ManualTrades = %v, %v", after, err)
	}
	got := after[0]
	if got.Type != TypeSell || !got.Quantity.Equal(decimal.RequireFromString("4")) || !got.Price.Equal(decimal.RequireFromString("85.5")) || !got.Date.Equal(corrected.Date) {
		t.Errorf("row not updated: %+v", got)
	}
	if got.Fingerprint() != corrected.Fingerprint() {
		t.Errorf("fingerprint mismatch")
	}
	// The stored fingerprint must have been recomputed, or re-entering
	// the corrected trade would insert a duplicate instead of deduping.
	if inserted, err := store.Insert(corrected); err != nil || inserted {
		t.Errorf("re-inserting the corrected trade: inserted=%v err=%v, want a no-op", inserted, err)
	}
}

func TestStore_UpdateAndDeleteManualTrade_RefuseEveryOtherRow(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	for _, tx := range []Transaction{
		baseTransaction(t),                       // imported buy: id 1
		interestCredit(t, "2024-03-01", "10.00"), // manual interest: id 2
	} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	for id := int64(1); id <= 2; id++ {
		if err := store.UpdateManualTrade(id, manualTrade(t, TypeBuy, "X", "2024-04-01", "1", "1")); !errors.Is(err, ErrNotManualTrade) {
			t.Errorf("Update id %d: err = %v, want ErrNotManualTrade", id, err)
		}
		if err := store.DeleteManualTrade(id); !errors.Is(err, ErrNotManualTrade) {
			t.Errorf("Delete id %d: err = %v, want ErrNotManualTrade", id, err)
		}
	}
	if err := store.UpdateManualTrade(999, manualTrade(t, TypeBuy, "X", "2024-04-01", "1", "1")); !errors.Is(err, ErrNotManualTrade) {
		t.Errorf("Update missing id: err = %v, want ErrNotManualTrade", err)
	}

	all, err := store.All()
	if err != nil || len(all) != 2 {
		t.Errorf("rows must be untouched, got %d, err %v", len(all), err)
	}
}

func TestStore_UpdateManualTrade_DuplicateTarget_ReturnsErrDuplicate(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	a := manualTrade(t, TypeBuy, "VWCE", "2024-01-01", "1", "10")
	b := manualTrade(t, TypeBuy, "VWCE", "2024-02-02", "2", "20")
	for _, tx := range []Transaction{a, b} {
		if _, err := store.Insert(tx); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	trades, _ := store.ManualTrades()
	var bID int64
	for _, tr := range trades {
		if tr.Date.Equal(b.Date) {
			bID = tr.ID
		}
	}

	if err := store.UpdateManualTrade(bID, a); !errors.Is(err, ErrDuplicateManualTrade) {
		t.Fatalf("err = %v, want ErrDuplicateManualTrade", err)
	}
	after, _ := store.ManualTrades()
	if len(after) != 2 {
		t.Errorf("rows = %d, want 2 after the refused update", len(after))
	}
}

func TestStore_DeleteManualTrade_RemovesRow(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	if _, err := store.Insert(manualTrade(t, TypeBuy, "VWCE", "2024-01-15", "5", "80")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	trades, _ := store.ManualTrades()

	if err := store.DeleteManualTrade(trades[0].ID); err != nil {
		t.Fatalf("DeleteManualTrade: %v", err)
	}
	if after, _ := store.ManualTrades(); len(after) != 0 {
		t.Errorf("rows = %d, want 0", len(after))
	}
}
