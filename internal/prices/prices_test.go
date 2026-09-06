package prices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// Captured real response shapes, pinned so an upstream format change
// breaks a test here rather than silently failing in production.
const (
	yahooOKBody    = `{"chart":{"result":[{"meta":{"currency":"USD","symbol":"AAPL","regularMarketPrice":195.89,"regularMarketTime":1704484800}}],"error":null}}`
	yahooErrorBody = `{"chart":{"result":null,"error":{"code":"Not Found","description":"No data found, symbol may be delisted"}}}`
	stooqOKBody    = "Symbol,Date,Time,Open,High,Low,Close,Volume\r\nAAPL.US,2024-01-05,22:00:04,181.99,182.76,180.17,181.18,62303020\r\n"
	stooqNDBody    = "Symbol,Date,Time,Open,High,Low,Close,Volume\r\nBOGUS,N/D,N/D,N/D,N/D,N/D,N/D,N/D\r\n"

	finnhubQuoteBody   = `{"c":195.89,"d":1.23,"dp":0.63,"h":196.5,"l":193.1,"o":194.0,"pc":194.66,"t":1704484800}`
	finnhubProfileBody = `{"country":"US","currency":"USD","name":"Apple Inc","ticker":"AAPL"}`
	finnhubZeroBody    = `{"c":0,"d":null,"dp":null,"h":0,"l":0,"o":0,"pc":0,"t":0}`
)

// finnhubMux routes the two Finnhub paths the client uses.
func finnhubMux(quote, profile func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/stock/profile2"):
			profile(w, r)
		default:
			quote(w, r)
		}
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// providers is a set of stub endpoints with per-endpoint hit counters
// and swappable handlers, standing in for Finnhub, Yahoo and stooq.
type providers struct {
	server       *httptest.Server
	finnhubHits  int64
	primaryHits  int64
	fallbackHits int64
	finnhubFn    func(w http.ResponseWriter, r *http.Request)
	primaryFn    func(w http.ResponseWriter, r *http.Request)
	fallbackFn   func(w http.ResponseWriter, r *http.Request)
}

func newProviders(t *testing.T) *providers {
	t.Helper()
	p := &providers{
		// Sensible defaults so a test only sets the handler it cares
		// about; the rest just fail.
		finnhubFn:  serveString(http.StatusInternalServerError, ""),
		primaryFn:  serveString(http.StatusInternalServerError, ""),
		fallbackFn: serveString(http.StatusInternalServerError, ""),
	}
	mux := http.NewServeMux()
	// Finnhub paths: /finnhub/quote, /finnhub/stock/profile2.
	mux.HandleFunc("/finnhub/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&p.finnhubHits, 1)
		p.finnhubFn(w, r)
	})
	// The v8 chart endpoint takes the symbol as a path segment, so the
	// client requests /yahoo/<symbol> — register a subtree.
	mux.HandleFunc("/yahoo/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&p.primaryHits, 1)
		p.primaryFn(w, r)
	})
	mux.HandleFunc("/stooq", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&p.fallbackHits, 1)
		p.fallbackFn(w, r)
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func serveString(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func (p *providers) quoter(opts ...Option) *HTTPQuoter {
	base := []Option{
		WithYahooURL(p.server.URL + "/yahoo"),
		WithStooqURL(p.server.URL + "/stooq"),
		WithFinnhubURL(p.server.URL + "/finnhub"),
		WithFinnhubToken(""), // hermetic: ignore any real TAXMAN_FINNHUB_TOKEN in the env
	}
	return NewHTTPQuoter(append(base, opts...)...)
}

func TestQuote_PrimarySuccess_ParsesPriceAndCurrency(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooOKBody)
	p.fallbackFn = serveString(http.StatusInternalServerError, "should not be called")

	q, err := p.quoter().Quote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if !q.Price.Equal(decimal.RequireFromString("195.89")) {
		t.Errorf("Price = %s, want 195.89", q.Price)
	}
	if q.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", q.Currency)
	}
	if q.Symbol != "AAPL" || q.Stale {
		t.Errorf("Symbol/Stale = %q/%v, want AAPL/false", q.Symbol, q.Stale)
	}
	if atomic.LoadInt64(&p.fallbackHits) != 0 {
		t.Errorf("fallback was called %d times, want 0", p.fallbackHits)
	}
}

func TestQuote_PrimaryFails_FallbackUsed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		primary func(http.ResponseWriter, *http.Request)
	}{
		{"5xx", serveString(http.StatusBadGateway, "")},
		{"malformed body", serveString(http.StatusOK, "{ this is not json")},
		{"error result", serveString(http.StatusOK, yahooErrorBody)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProviders(t)
			p.primaryFn = tc.primary
			p.fallbackFn = serveString(http.StatusOK, stooqOKBody)

			q, err := p.quoter().Quote(context.Background(), "AAPL")
			if err != nil {
				t.Fatalf("Quote: %v", err)
			}
			if !q.Price.Equal(decimal.RequireFromString("181.18")) {
				t.Errorf("Price = %s, want 181.18 (stooq close)", q.Price)
			}
			if q.Currency != "" {
				t.Errorf("Currency = %q, want \"\" (stooq reports none)", q.Currency)
			}
			if atomic.LoadInt64(&p.fallbackHits) != 1 {
				t.Errorf("fallback hits = %d, want 1", p.fallbackHits)
			}
		})
	}
}

func TestQuote_BothProvidersFail_ServesStaleFromCache(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooOKBody)
	p.fallbackFn = serveString(http.StatusInternalServerError, "")
	clk := &fakeClock{t: time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC)}
	quoter := p.quoter(WithClock(clk.Now))

	first, err := quoter.Quote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("first Quote: %v", err)
	}

	// Both endpoints now down; move past the cache TTL.
	p.primaryFn = serveString(http.StatusInternalServerError, "")
	clk.Advance(20 * time.Minute)

	stale, err := quoter.Quote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("stale Quote returned an error, want last-known value: %v", err)
	}
	if !stale.Stale {
		t.Errorf("Stale = false, want true")
	}
	if !stale.Price.Equal(first.Price) {
		t.Errorf("stale Price = %s, want the cached %s", stale.Price, first.Price)
	}
	if !stale.AsOf.Equal(first.AsOf) {
		t.Errorf("stale AsOf = %s, want the original fetch time %s", stale.AsOf, first.AsOf)
	}
}

func TestQuote_BothProvidersFail_NoCache_ReturnsError(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusInternalServerError, "")
	p.fallbackFn = serveString(http.StatusInternalServerError, "")

	if _, err := p.quoter().Quote(context.Background(), "AAPL"); err == nil {
		t.Fatal("expected an error when both providers fail and nothing is cached")
	}
}

func TestQuote_SecondCallInsideTTL_MakesNoRequest(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusOK, yahooOKBody)
	p.fallbackFn = serveString(http.StatusInternalServerError, "")
	clk := &fakeClock{t: time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC)}
	quoter := p.quoter(WithClock(clk.Now))

	if _, err := quoter.Quote(context.Background(), "AAPL"); err != nil {
		t.Fatalf("first Quote: %v", err)
	}
	clk.Advance(5 * time.Minute) // still well inside the 15-minute TTL

	if _, err := quoter.Quote(context.Background(), "AAPL"); err != nil {
		t.Fatalf("second Quote: %v", err)
	}
	if got := atomic.LoadInt64(&p.primaryHits); got != 1 {
		t.Errorf("primary hits = %d, want 1 (second call should be a cache hit)", got)
	}
}

func TestQuote_FallbackNoDataRow_ReturnsError(t *testing.T) {
	p := newProviders(t)
	p.primaryFn = serveString(http.StatusInternalServerError, "")
	p.fallbackFn = serveString(http.StatusOK, stooqNDBody)

	if _, err := p.quoter().Quote(context.Background(), "BOGUS"); err == nil {
		t.Fatal("expected an error for stooq's N/D (unknown symbol) response")
	}
}

func TestQuote_Finnhub_UsedFirstWhenTokenSet_ParsesPriceAndCurrency(t *testing.T) {
	p := newProviders(t)
	p.finnhubFn = finnhubMux(
		serveString(http.StatusOK, finnhubQuoteBody),
		serveString(http.StatusOK, finnhubProfileBody),
	)
	p.primaryFn = serveString(http.StatusOK, yahooOKBody) // should not be reached

	q, err := p.quoter(WithFinnhubToken("test-token")).Quote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if !q.Price.Equal(decimal.RequireFromString("195.89")) {
		t.Errorf("Price = %s, want 195.89", q.Price)
	}
	if q.Currency != "USD" {
		t.Errorf("Currency = %q, want USD (from /stock/profile2)", q.Currency)
	}
	if atomic.LoadInt64(&p.primaryHits) != 0 {
		t.Errorf("Yahoo was called %d times; Finnhub should have satisfied the request", p.primaryHits)
	}
}

func TestQuote_Finnhub_NoToken_ProviderSkipped(t *testing.T) {
	p := newProviders(t)
	p.finnhubFn = serveString(http.StatusOK, finnhubQuoteBody) // present but must not be hit
	p.primaryFn = serveString(http.StatusOK, yahooOKBody)

	if _, err := p.quoter().Quote(context.Background(), "AAPL"); err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if atomic.LoadInt64(&p.finnhubHits) != 0 {
		t.Errorf("Finnhub was called %d times with no token set, want 0", p.finnhubHits)
	}
}

func TestQuote_Finnhub_ProfileFails_PriceKeptCurrencyEmpty(t *testing.T) {
	p := newProviders(t)
	p.finnhubFn = finnhubMux(
		serveString(http.StatusOK, finnhubQuoteBody),
		serveString(http.StatusInternalServerError, ""),
	)

	q, err := p.quoter(WithFinnhubToken("test-token")).Quote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if !q.Price.Equal(decimal.RequireFromString("195.89")) {
		t.Errorf("Price = %s, want 195.89", q.Price)
	}
	if q.Currency != "" {
		t.Errorf("Currency = %q, want \"\" when the profile call fails", q.Currency)
	}
}

func TestQuote_Finnhub_ZeroPrice_FallsThroughToYahoo(t *testing.T) {
	p := newProviders(t)
	p.finnhubFn = finnhubMux(
		serveString(http.StatusOK, finnhubZeroBody), // unknown symbol → c:0
		serveString(http.StatusOK, finnhubProfileBody),
	)
	p.primaryFn = serveString(http.StatusOK, yahooOKBody)

	q, err := p.quoter(WithFinnhubToken("test-token")).Quote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if !q.Price.Equal(decimal.RequireFromString("195.89")) {
		t.Errorf("Price = %s, want 195.89 (from Yahoo after Finnhub returned c:0)", q.Price)
	}
	if atomic.LoadInt64(&p.primaryHits) == 0 {
		t.Errorf("expected the chain to fall through to Yahoo")
	}
}
