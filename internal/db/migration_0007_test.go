package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/ledger"
)

// legacyCredit is a hand-entered interest credit as it was stored
// before 0007: a per-bank platform and a fixed instrument id. The
// fingerprint is the one Fingerprint() would have produced for it.
type legacyCredit struct {
	platform, instrument, date, price string
}

func (c legacyCredit) fingerprintInput() string {
	return c.platform + "|interest|" + c.date + "|" + c.instrument + "|1|" + c.price + "|EUR"
}

// seedLegacyCredits inserts pre-0007 interest credits directly, so
// the migration under test has real rows to move.
func seedLegacyCredits(t *testing.T, conn *sql.DB, credits ...legacyCredit) {
	t.Helper()
	for _, c := range credits {
		var fp string
		if err := conn.QueryRow(`SELECT sha256_hex(?)`, c.fingerprintInput()).Scan(&fp); err != nil {
			t.Fatalf("hashing %+v: %v", c, err)
		}
		if _, err := conn.Exec(
			`INSERT INTO transactions (fingerprint, platform, type, date, instrument, quantity, price, currency, source_ref, description)
			 VALUES (?, ?, 'interest', ?, ?, '1', ?, 'EUR', '', '')`,
			fp, c.platform, c.date, c.instrument, c.price,
		); err != nil {
			t.Fatalf("inserting %+v: %v", c, err)
		}
	}
}

func TestMigrations_0007_MovesBankPlatformsToUserNamedManualCredits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := downTo(conn, 6); err != nil {
		t.Fatalf("down to 6: %v", err)
	}

	n26 := legacyCredit{platform: "n26", instrument: "N26_SAVINGS", date: "2024-06-30T00:00:00Z", price: "142.85"}
	tr := legacyCredit{platform: "traderepublic", instrument: "TRADE_REPUBLIC_SAVINGS", date: "2024-12-31T00:00:00Z", price: "168.40"}
	seedLegacyCredits(t, conn, n26, tr)

	// An imported transaction must be left completely alone.
	if _, err := conn.Exec(
		`INSERT INTO transactions (fingerprint, platform, type, date, instrument, quantity, price, currency, source_ref, description)
		 VALUES ('imported-fp', 'degiro', 'buy', '2024-03-01T00:00:00Z', 'IE00B4L5Y983', '10', '90.00', 'EUR', '', '')`,
	); err != nil {
		t.Fatalf("inserting imported transaction: %v", err)
	}

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("re-up (applies 0007): %v", err)
	}

	// Both credits are now manual, named as the user would name them.
	got := map[string]string{} // instrument -> platform
	rows, err := conn.Query(`SELECT instrument, platform FROM transactions WHERE type = 'interest'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var instrument, platform string
		if err := rows.Scan(&instrument, &platform); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[instrument] = platform
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	want := map[string]string{"N26": "manual", "Trade Republic": "manual"}
	for instrument, platform := range want {
		if got[instrument] != platform {
			t.Errorf("credit %q: platform = %q, want %q (all credits: %v)", instrument, got[instrument], platform, got)
		}
	}

	// The fingerprint must match what the new shape hashes to, or the
	// credit stops deduping against a re-entry of itself.
	assertFingerprintMatches(t, conn, "N26", "manual|interest|2024-06-30T00:00:00Z|N26|1|142.85|EUR")
	assertFingerprintMatches(t, conn, "Trade Republic", "manual|interest|2024-12-31T00:00:00Z|Trade Republic|1|168.40|EUR")

	var importedPlatform, importedFP string
	if err := conn.QueryRow(
		`SELECT platform, fingerprint FROM transactions WHERE type = 'buy'`,
	).Scan(&importedPlatform, &importedFP); err != nil {
		t.Fatalf("querying imported transaction: %v", err)
	}
	if importedPlatform != "degiro" || importedFP != "imported-fp" {
		t.Errorf("imported transaction was touched: platform=%q fingerprint=%q", importedPlatform, importedFP)
	}
}

func TestMigrations_0007_DownRestoresTheBankPlatforms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := downTo(conn, 6); err != nil {
		t.Fatalf("down to 6: %v", err)
	}
	n26 := legacyCredit{platform: "n26", instrument: "N26_SAVINGS", date: "2024-06-30T00:00:00Z", price: "142.85"}
	seedLegacyCredits(t, conn, n26)

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	if err := downTo(conn, 6); err != nil {
		t.Fatalf("down again: %v", err)
	}

	var platform, instrument, fingerprint string
	if err := conn.QueryRow(
		`SELECT platform, instrument, fingerprint FROM transactions WHERE type = 'interest'`,
	).Scan(&platform, &instrument, &fingerprint); err != nil {
		t.Fatalf("querying credit: %v", err)
	}
	if platform != "n26" || instrument != "N26_SAVINGS" {
		t.Errorf("after down: platform=%q instrument=%q, want n26/N26_SAVINGS", platform, instrument)
	}
	var wantFP string
	if err := conn.QueryRow(`SELECT sha256_hex(?)`, n26.fingerprintInput()).Scan(&wantFP); err != nil {
		t.Fatalf("hashing: %v", err)
	}
	if fingerprint != wantFP {
		t.Errorf("after down: fingerprint = %q, want the pre-0007 fingerprint %q", fingerprint, wantFP)
	}
}

func TestMigrations_0007_DownLeavesAUserNamedCreditAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := Up(conn, Migrations); err != nil {
		t.Fatalf("up: %v", err)
	}
	// Logged after 0007, under a name that never had a platform.
	if _, err := conn.Exec(
		`INSERT INTO transactions (fingerprint, platform, type, date, instrument, quantity, price, currency, source_ref, description)
		 VALUES ('rainy-fp', 'manual', 'interest', '2025-01-31T00:00:00Z', 'Rainy day', '1', '9.99', 'EUR', '', '')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := downTo(conn, 6); err != nil {
		t.Fatalf("down: %v", err)
	}

	var platform, instrument string
	if err := conn.QueryRow(
		`SELECT platform, instrument FROM transactions WHERE instrument = 'Rainy day'`,
	).Scan(&platform, &instrument); err != nil {
		t.Fatalf("querying credit: %v", err)
	}
	if platform != "manual" {
		t.Errorf("platform = %q, want manual — down must not invent a bank for a user-named account", platform)
	}
}

func assertFingerprintMatches(t *testing.T, conn *sql.DB, instrument, fingerprintInput string) {
	t.Helper()
	var stored, want string
	if err := conn.QueryRow(
		`SELECT fingerprint FROM transactions WHERE instrument = ?`, instrument,
	).Scan(&stored); err != nil {
		t.Fatalf("querying %q: %v", instrument, err)
	}
	if err := conn.QueryRow(`SELECT sha256_hex(?)`, fingerprintInput).Scan(&want); err != nil {
		t.Fatalf("hashing: %v", err)
	}
	if stored != want {
		t.Errorf("%q: fingerprint = %q, want %q (recomputed from the migrated row)", instrument, stored, want)
	}
}

// The 0007 fingerprint expression is a hand-written copy of
// ledger.Transaction.Fingerprint's field order and separator. Pin the
// two together so a change to one is a failing test, not a silent
// dedupe hole in a migrated ledger.
func TestMigrations_0007_FingerprintExpressionMatchesLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tx := ledger.Transaction{
		Platform:   ledger.PlatformManual,
		Type:       ledger.TypeInterest,
		Date:       time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
		Instrument: "Rainy day",
		Quantity:   decimal.NewFromInt(1),
		Price:      decimal.RequireFromString("142.85"),
		Currency:   "EUR",
	}
	input := string(tx.Platform) + "|" + string(tx.Type) + "|" +
		tx.Date.UTC().Format(time.RFC3339) + "|" + tx.Instrument + "|" +
		tx.Quantity.String() + "|" + tx.Price.String() + "|" + tx.Currency

	var got string
	if err := conn.QueryRow(`SELECT sha256_hex(?)`, input).Scan(&got); err != nil {
		t.Fatalf("hashing: %v", err)
	}
	if got != tx.Fingerprint() {
		t.Errorf("sha256_hex over the migration's field order = %q, want ledger's %q", got, tx.Fingerprint())
	}
}
