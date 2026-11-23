package prices

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/craicoverflow/taxman/internal/db"
)

// yahooHistoryBody is the shape of Yahoo's v8 chart response with
// range=10y&interval=1d, cut to four sessions. The timestamps are US
// session opens (14:30 UTC) shifted by gmtoffset -18000; the null is a
// day with no settled close, and the last point repeats the previous
// session's date the way Yahoo appends the live session.
const yahooHistoryBody = `{"chart":{"result":[{"meta":{"currency":"USD","symbol":"AAPL","gmtoffset":-18000},
"timestamp":[1704205800,1704292200,1704378600,1704465000,1704475000],
"indicators":{"quote":[{"close":[185.64,null,181.91,181.18,181.50]}],"adjclose":[{"adjclose":[1,1,1,1,1]}]}}],"error":null}}`

const yahooHistoryPenceBody = `{"chart":{"result":[{"meta":{"currency":"GBp","symbol":"VWRL.L","gmtoffset":0},
"timestamp":[1704186000],
"indicators":{"quote":[{"close":[9512.5]}]}}],"error":null}}`

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestHistory_ParsesDailyClosesInExchangeDates(t *testing.T) {
	p := newProviders(t)
	var gotQuery string
	p.primaryFn = func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(yahooHistoryBody))
	}

	h, err := p.quoter().History(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// Only the symbol (path) and a fixed range leave the process — never
	// a date derived from the ledger.
	if gotQuery != "range=10y&interval=1d" {
		t.Errorf("query = %q, want the fixed range only", gotQuery)
	}
	if h.Currency != "USD" || h.Symbol != "AAPL" || h.Stale {
		t.Errorf("Currency/Symbol/Stale = %q/%q/%v", h.Currency, h.Symbol, h.Stale)
	}
	want := []Close{
		{day("2024-01-02"), decimal.RequireFromString("185.64")},
		{day("2024-01-04"), decimal.RequireFromString("181.91")},
		{day("2024-01-05"), decimal.RequireFromString("181.5")}, // later same-day point wins
	}
	if len(h.Closes) != len(want) {
		t.Fatalf("got %d closes, want %d: %+v", len(h.Closes), len(want), h.Closes)
	}
	for i, w := range want {
		if !h.Closes[i].Date.Equal(w.Date) || !h.Closes[i].Price.Equal(w.Price) {
			t.Errorf("close[%d] = %s %s, want %s %s", i, h.Closes[i].Date.Format("2006-01-02"), h.Closes[i].Price, w.Date.Format("2006-01-02"), w.Price)
		}
	}
}

func TestHistory_PenceListingScaledToPounds(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooHistoryPenceBody)

	h, err := p.quoter().History(context.Background(), "VWRL.L")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if h.Currency != "GBP" {
		t.Errorf("Currency = %q, want GBP", h.Currency)
	}
	if !h.Closes[0].Price.Equal(decimal.RequireFromString("95.125")) {
		t.Errorf("close = %s, want 95.125", h.Closes[0].Price)
	}
}

func TestHistory_CachedWithinTTL_ThenStaleOnFailure(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooHistoryBody)
	clock := &fakeClock{t: time.Date(2024, 1, 6, 12, 0, 0, 0, time.UTC)}
	q := p.quoter(WithClock(clock.Now))

	if _, err := q.History(context.Background(), "AAPL"); err != nil {
		t.Fatalf("first History: %v", err)
	}
	clock.Advance(time.Hour)
	if _, err := q.History(context.Background(), "AAPL"); err != nil {
		t.Fatalf("second History: %v", err)
	}
	if hits := atomic.LoadInt64(&p.primaryHits); hits != 1 {
		t.Errorf("provider hits = %d, want 1 (second call inside TTL)", hits)
	}

	clock.Advance(historyTTL)
	p.primaryFn = serveString(http.StatusTooManyRequests, "")
	h, err := q.History(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("History after TTL with provider down: %v", err)
	}
	if !h.Stale || len(h.Closes) != 3 {
		t.Errorf("Stale/len = %v/%d, want true/3", h.Stale, len(h.Closes))
	}
}

func TestHistory_ProviderFails_NothingCached_Errors(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooErrorBody)
	if _, err := p.quoter().History(context.Background(), "BOGUS"); err == nil {
		t.Fatal("History: want an error for a symbol with no data")
	}
}

func TestHistory_DurableCache_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Up(conn, db.Migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cache := NewSQLiteCache(conn)

	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooHistoryBody)
	if _, err := p.quoter(WithHistoryCache(cache)).History(context.Background(), "AAPL"); err != nil {
		t.Fatalf("History: %v", err)
	}

	// A fresh process, providers down: the series comes back from SQLite.
	p.primaryFn = serveString(http.StatusInternalServerError, "")
	clock := &fakeClock{t: time.Now().Add(historyTTL + time.Hour)}
	h, err := p.quoter(WithHistoryCache(cache), WithClock(clock.Now)).History(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("History from durable cache: %v", err)
	}
	if !h.Stale || h.Currency != "USD" || len(h.Closes) != 3 {
		t.Fatalf("got Stale=%v Currency=%q len=%d", h.Stale, h.Currency, len(h.Closes))
	}
	if !h.Closes[2].Date.Equal(day("2024-01-05")) || !h.Closes[2].Price.Equal(decimal.RequireFromString("181.5")) {
		t.Errorf("last close = %+v", h.Closes[2])
	}
}
