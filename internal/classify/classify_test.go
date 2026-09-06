package classify

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/ledger"
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

func TestClassify_NoOverride_ReturnsUnclassified(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	got, err := store.Classify("IE00B4L5Y983")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != Unclassified {
		t.Errorf("Classify(no override) = %q, want %q", got, Unclassified)
	}
}

func TestClassify_SetThenLookup_ReturnsSetValue(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	if err := store.Set("IE00B4L5Y983", ExitTaxFund); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Classify("IE00B4L5Y983")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != ExitTaxFund {
		t.Errorf("Classify(after Set) = %q, want %q", got, ExitTaxFund)
	}
}

func TestClassify_SetCGTAsset_ReturnsSetValue(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	if err := store.Set("AAPL", CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Classify("AAPL")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != CGTAsset {
		t.Errorf("Classify(after Set) = %q, want %q", got, CGTAsset)
	}
}

func TestSet_Overwrite_ReplacesPreviousValue(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)

	if err := store.Set("AAPL", ExitTaxFund); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := store.Set("AAPL", CGTAsset); err != nil {
		t.Fatalf("second Set: %v", err)
	}

	got, err := store.Classify("AAPL")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != CGTAsset {
		t.Errorf("Classify after overwrite = %q, want %q", got, CGTAsset)
	}
}

func TestSet_PersistsAcrossReconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	conn1, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Up(conn1, db.Migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := NewStore(conn1).Set("AAPL", CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := conn1.Close(); err != nil {
		t.Fatalf("closing conn1: %v", err)
	}

	conn2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	got, err := NewStore(conn2).Classify("AAPL")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got != CGTAsset {
		t.Errorf("Classify after reconnect = %q, want %q", got, CGTAsset)
	}
}

func TestListUnclassified_ReturnsOnlyHoldingsWithoutOverride(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	ledgerStore := ledger.NewStore(conn)

	// Two distinct holdings in the ledger: one gets classified, one
	// doesn't.
	txClassified := baseTransaction(t, "IE00B4L5Y983")
	txUnclassified := baseTransaction(t, "AAPL")
	if _, err := ledgerStore.Insert(txClassified); err != nil {
		t.Fatalf("inserting classified holding's transaction: %v", err)
	}
	if _, err := ledgerStore.Insert(txUnclassified); err != nil {
		t.Fatalf("inserting unclassified holding's transaction: %v", err)
	}
	if err := store.Set("IE00B4L5Y983", ExitTaxFund); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.ListUnclassified()
	if err != nil {
		t.Fatalf("ListUnclassified: %v", err)
	}
	if len(got) != 1 || got[0] != "AAPL" {
		t.Errorf("ListUnclassified() = %v, want [AAPL]", got)
	}
}

func TestListUnclassified_AllClassified_ReturnsEmpty(t *testing.T) {
	conn := openMigratedTestDB(t)
	store := NewStore(conn)
	ledgerStore := ledger.NewStore(conn)

	tx := baseTransaction(t, "IE00B4L5Y983")
	if _, err := ledgerStore.Insert(tx); err != nil {
		t.Fatalf("inserting transaction: %v", err)
	}
	if err := store.Set("IE00B4L5Y983", CGTAsset); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.ListUnclassified()
	if err != nil {
		t.Fatalf("ListUnclassified: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no unclassified holdings, got %v", got)
	}
}
