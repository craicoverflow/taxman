package valuations

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/engine"
)

// Store must satisfy the engine's Valuer: the engine defines that
// interface so it never imports this package, and nothing else keeps
// the two signatures in step.
var _ engine.Valuer = (*Store)(nil)

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

func day(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return parsed
}

func TestGet_NoValuation_ReturnsNotFound(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	v, ok, err := store.Get("IE00B4L5Y983", day(t, "2024-03-15"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Errorf("Get(no valuation) = (%+v, true), want not found", v)
	}
}

func TestUpsert_ThenGet_RoundTrips(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	if err := store.Upsert(Valuation{
		Instrument:   "IE00B4L5Y983",
		Date:         day(t, "2024-03-15"),
		ValuePerUnit: decimal.RequireFromString("102.47"),
		Currency:     "EUR",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	v, ok, err := store.Get("IE00B4L5Y983", day(t, "2024-03-15"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected the valuation to be found")
	}
	if !v.ValuePerUnit.Equal(decimal.RequireFromString("102.47")) {
		t.Errorf("ValuePerUnit = %s, want 102.47", v.ValuePerUnit)
	}
	if v.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", v.Currency)
	}
}

func TestGet_IgnoresTimeOfDay(t *testing.T) {
	// An anniversary is a calendar day: a lookup with a wall-clock
	// time on the same day must still find the value, or the engine
	// would report a missing valuation for a value that is on record.
	store := NewStore(openMigratedTestDB(t))

	if err := store.Upsert(Valuation{
		Instrument:   "IE00B4L5Y983",
		Date:         day(t, "2024-03-15"),
		ValuePerUnit: decimal.RequireFromString("100"),
		Currency:     "EUR",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	noon := time.Date(2024, 3, 15, 12, 30, 0, 0, time.UTC)
	if _, ok, err := store.Get("IE00B4L5Y983", noon); err != nil || !ok {
		t.Fatalf("Get(midday on the same date) = (ok %v, err %v), want found", ok, err)
	}
}

func TestUpsert_Twice_CorrectsRatherThanDuplicates(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	for _, value := range []string{"100", "105.25"} {
		if err := store.Upsert(Valuation{
			Instrument:   "IE00B4L5Y983",
			Date:         day(t, "2024-03-15"),
			ValuePerUnit: decimal.RequireFromString(value),
			Currency:     "EUR",
		}); err != nil {
			t.Fatalf("Upsert(%s): %v", value, err)
		}
	}

	all, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 valuation after a correction, got %d", len(all))
	}
	if !all[0].ValuePerUnit.Equal(decimal.RequireFromString("105.25")) {
		t.Errorf("ValuePerUnit = %s, want the corrected 105.25", all[0].ValuePerUnit)
	}
}

func TestUpsert_RejectsIncompleteOrNonPositiveValues(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	cases := map[string]Valuation{
		"no instrument": {Date: day(t, "2024-03-15"), ValuePerUnit: decimal.NewFromInt(10), Currency: "EUR"},
		"no currency":   {Instrument: "X", Date: day(t, "2024-03-15"), ValuePerUnit: decimal.NewFromInt(10)},
		"zero value":    {Instrument: "X", Date: day(t, "2024-03-15"), ValuePerUnit: decimal.Zero, Currency: "EUR"},
		"negative":      {Instrument: "X", Date: day(t, "2024-03-15"), ValuePerUnit: decimal.NewFromInt(-1), Currency: "EUR"},
	}
	for name, v := range cases {
		if err := store.Upsert(v); err == nil {
			t.Errorf("Upsert(%s) = nil, want an error", name)
		}
	}
}

func TestAll_OrdersByInstrumentThenDate(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	entries := []Valuation{
		{Instrument: "B_FUND", Date: day(t, "2024-01-01"), ValuePerUnit: decimal.NewFromInt(3), Currency: "EUR"},
		{Instrument: "A_FUND", Date: day(t, "2026-01-01"), ValuePerUnit: decimal.NewFromInt(2), Currency: "EUR"},
		{Instrument: "A_FUND", Date: day(t, "2018-01-01"), ValuePerUnit: decimal.NewFromInt(1), Currency: "EUR"},
	}
	for _, v := range entries {
		if err := store.Upsert(v); err != nil {
			t.Fatalf("Upsert(%+v): %v", v, err)
		}
	}

	all, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	want := []string{"A_FUND@2018-01-01", "A_FUND@2026-01-01", "B_FUND@2024-01-01"}
	for i, w := range want {
		got := all[i].Instrument + "@" + all[i].Date.Format("2006-01-02")
		if got != w {
			t.Errorf("All()[%d] = %s, want %s", i, got, w)
		}
	}
}

func TestValuePerUnit_MatchesTheEngineInterface(t *testing.T) {
	store := NewStore(openMigratedTestDB(t))

	if err := store.Upsert(Valuation{
		Instrument:   "IE00B4L5Y983",
		Date:         day(t, "2024-03-15"),
		ValuePerUnit: decimal.RequireFromString("18"),
		Currency:     "USD",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	value, currency, ok, err := store.ValuePerUnit("IE00B4L5Y983", day(t, "2024-03-15"))
	if err != nil || !ok {
		t.Fatalf("ValuePerUnit = (ok %v, err %v), want found", ok, err)
	}
	if !value.Equal(decimal.NewFromInt(18)) || currency != "USD" {
		t.Errorf("ValuePerUnit = (%s, %q), want (18, USD)", value, currency)
	}

	if _, _, ok, err := store.ValuePerUnit("IE00B4L5Y983", day(t, "2024-03-16")); err != nil || ok {
		t.Errorf("ValuePerUnit(other date) = (ok %v, err %v), want not found", ok, err)
	}
}
