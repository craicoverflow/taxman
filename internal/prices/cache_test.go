package prices

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
)

// fakeCache is an in-memory Cache with a hit counter, standing in for
// SQLiteCache in HTTPQuoter tests.
type fakeCache struct {
	mu      sync.Mutex
	rows    map[string]cacheEntry
	loads   int64
	stores  int64
	loadErr error
}

func newFakeCache() *fakeCache { return &fakeCache{rows: map[string]cacheEntry{}} }

func (c *fakeCache) Load(symbol string) (Quote, time.Time, bool, error) {
	atomic.AddInt64(&c.loads, 1)
	if c.loadErr != nil {
		return Quote{}, time.Time{}, false, c.loadErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.rows[symbol]
	if !ok {
		return Quote{}, time.Time{}, false, nil
	}
	return e.quote, e.fetchedAt, true, nil
}

func (c *fakeCache) Store(symbol string, q Quote, fetchedAt time.Time) error {
	atomic.AddInt64(&c.stores, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows[symbol] = cacheEntry{quote: q, fetchedAt: fetchedAt}
	return nil
}

func (c *fakeCache) seed(symbol string, q Quote, fetchedAt time.Time) {
	c.rows[symbol] = cacheEntry{quote: q, fetchedAt: fetchedAt}
}

func TestQuote_DurableCacheFresh_ServedWithoutHittingProviders(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusInternalServerError, "provider must not be called")
	p.fallbackFn = serveString(http.StatusInternalServerError, "provider must not be called")

	clk := &fakeClock{t: time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC)}
	fc := newFakeCache()
	fc.seed("AAPL", Quote{
		Symbol: "AAPL", Price: decimal.RequireFromString("195.89"), Currency: "USD",
		AsOf: clk.t.Add(-5 * time.Minute),
	}, clk.t.Add(-5*time.Minute)) // inside the 15-minute TTL

	q, err := p.quoter(WithClock(clk.Now), WithCache(fc)).Quote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if q.Stale {
		t.Errorf("Stale = true; a fresh durable-cache row is a live quote")
	}
	if !q.Price.Equal(decimal.RequireFromString("195.89")) {
		t.Errorf("Price = %s, want 195.89 from the durable cache", q.Price)
	}
	if got := atomic.LoadInt64(&p.primaryHits) + atomic.LoadInt64(&p.fallbackHits); got != 0 {
		t.Errorf("providers hit %d times; a fresh cache row should serve alone", got)
	}
}

func TestQuote_ProvidersFail_ColdProcess_ServesStaleFromDurableCache(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusTooManyRequests, "")
	p.fallbackFn = serveString(http.StatusNotFound, "")

	clk := &fakeClock{t: time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC)}
	fetched := clk.t.Add(-2 * time.Hour) // well past the TTL
	fc := newFakeCache()
	fc.seed("WEBN.DE", Quote{
		Symbol: "WEBN.DE", Price: decimal.RequireFromString("12.94"), Currency: "EUR", AsOf: fetched,
	}, fetched)

	q, err := p.quoter(WithClock(clk.Now), WithCache(fc)).Quote(context.Background(), "WEBN.DE")
	if err != nil {
		t.Fatalf("Quote should fall back to the durable cache, not error: %v", err)
	}
	if !q.Stale {
		t.Errorf("Stale = false, want true for a past-TTL fallback")
	}
	if !q.Price.Equal(decimal.RequireFromString("12.94")) {
		t.Errorf("Price = %s, want 12.94", q.Price)
	}
	if !q.AsOf.Equal(fetched) {
		t.Errorf("AsOf = %s, want the original fetch time %s", q.AsOf, fetched)
	}
}

func TestQuote_SuccessfulFetch_WritesThroughToDurableCache(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooOKBody)
	p.fallbackFn = serveString(http.StatusInternalServerError, "")

	clk := &fakeClock{t: time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC)}
	fc := newFakeCache()

	if _, err := p.quoter(WithClock(clk.Now), WithCache(fc)).Quote(context.Background(), "AAPL"); err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if atomic.LoadInt64(&fc.stores) != 1 {
		t.Fatalf("durable cache stores = %d, want 1", fc.stores)
	}
	got, fetchedAt, ok, _ := fc.Load("AAPL")
	if !ok {
		t.Fatal("nothing written to the durable cache")
	}
	if !got.Price.Equal(decimal.RequireFromString("195.89")) || got.Currency != "USD" {
		t.Errorf("stored quote = %s %s, want 195.89 USD", got.Price, got.Currency)
	}
	if !fetchedAt.Equal(clk.t) {
		t.Errorf("stored fetchedAt = %s, want %s", fetchedAt, clk.t)
	}
}

func TestQuote_DurableCacheLoadError_IgnoredNotFatal(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooOKBody)
	p.fallbackFn = serveString(http.StatusInternalServerError, "")

	fc := newFakeCache()
	fc.loadErr = sql.ErrConnDone

	q, err := p.quoter(WithCache(fc)).Quote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("a cache Load error must not fail the quote: %v", err)
	}
	if !q.Price.Equal(decimal.RequireFromString("195.89")) {
		t.Errorf("Price = %s, want the live 195.89", q.Price)
	}
}

func TestQuote_SendsBrowserUserAgent(t *testing.T) {
	p := newProviders(t)
	var gotUA string
	p.primaryFn = func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		serveString(http.StatusOK, yahooOKBody)(w, r)
	}
	p.fallbackFn = serveString(http.StatusInternalServerError, "")

	if _, err := p.quoter().Quote(context.Background(), "AAPL"); err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if gotUA != userAgent {
		t.Errorf("User-Agent = %q, want the browser-shaped %q", gotUA, userAgent)
	}
}

func TestSQLiteCache_RoundTrip(t *testing.T) {
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

	if _, _, ok, err := cache.Load("AAPL"); err != nil || ok {
		t.Fatalf("Load(empty) = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	fetched := time.Date(2024, 1, 5, 22, 0, 0, 0, time.UTC)
	want := Quote{Symbol: "AAPL", Price: decimal.RequireFromString("195.89"), Currency: "USD"}
	if err := cache.Store("AAPL", want, fetched); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, gotFetched, ok, err := cache.Load("AAPL")
	if err != nil || !ok {
		t.Fatalf("Load after Store = (ok=%v, err=%v)", ok, err)
	}
	if !got.Price.Equal(want.Price) || got.Currency != "USD" || got.Symbol != "AAPL" {
		t.Errorf("Load = %+v, want price 195.89 USD AAPL", got)
	}
	if !gotFetched.Equal(fetched) {
		t.Errorf("fetchedAt = %s, want %s", gotFetched, fetched)
	}
	if !got.AsOf.Equal(fetched) {
		t.Errorf("AsOf = %s, want the fetch time %s", got.AsOf, fetched)
	}

	// Store again — upsert, not a second row or an error.
	newer := fetched.Add(time.Hour)
	if err := cache.Store("AAPL", Quote{Price: decimal.RequireFromString("200"), Currency: "USD"}, newer); err != nil {
		t.Fatalf("Store (upsert): %v", err)
	}
	got, gotFetched, _, _ = cache.Load("AAPL")
	if !got.Price.Equal(decimal.RequireFromString("200")) || !gotFetched.Equal(newer) {
		t.Errorf("after upsert Load = %s @ %s, want 200 @ %s", got.Price, gotFetched, newer)
	}
}
