package prices

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const (
	// Yahoo's chart endpoint takes the symbol as a path segment and,
	// unlike v7/finance/quote, does not require a crumb+cookie
	// handshake — it returns a usable last price and currency for an
	// anonymous request. yahooURL is the prefix; the symbol is
	// appended as "/<symbol>".
	defaultYahooURL   = "https://query1.finance.yahoo.com/v8/finance/chart"
	defaultStooqURL   = "https://stooq.com/q/l/"
	defaultFinnhubURL = "https://finnhub.io/api/v1"

	// finnhubTokenEnv, when set, enables the Finnhub provider — used
	// first, ahead of the keyless endpoints. Finnhub's free tier is
	// reliable where Yahoo/stooq are rate-limited or IP-blocked. Only
	// the ticker symbol and this token leave the process; no ledger
	// data (SPEC.md §6, §9).
	finnhubTokenEnv = "TAXMAN_FINNHUB_TOKEN"

	defaultTTL     = 15 * time.Minute
	defaultTimeout = 10 * time.Second

	// userAgent is sent on every request. Yahoo's keyless chart
	// endpoint returns 429 to bare Go/net-http clients almost
	// immediately; a browser-shaped User-Agent gets ordinary
	// rate-limit headroom. It carries no identifying information.
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
)

// HTTPQuoter is a Quoter backed by public HTTP price endpoints, tried
// in order: Finnhub (only when TAXMAN_FINNHUB_TOKEN is set), then
// Yahoo Finance's keyless chart endpoint, then stooq's keyless CSV.
// Results are cached in-process for a TTL; when every provider fails
// the last cached quote is served with Stale set. With WithCache the
// last quote per symbol is also persisted, so the fallback survives a
// process restart during a spell of provider rate-limiting. Safe for
// concurrent use.
type HTTPQuoter struct {
	client       *http.Client
	yahooURL     string
	stooqURL     string
	finnhubURL   string
	finnhubToken string
	ttl          time.Duration
	now          func() time.Time
	persist      Cache // durable last-known-price store; nil disables it

	mu            sync.Mutex
	cache         map[string]cacheEntry
	currencyCache map[string]string // symbol → listing currency (Finnhub profile), "" once looked up and unknown
}

type cacheEntry struct {
	quote     Quote
	fetchedAt time.Time
}

// Option overrides an HTTPQuoter default. The exported options exist
// mainly so tests can point the client at an httptest.Server and
// control the clock; production code calls NewHTTPQuoter() with none.
type Option func(*HTTPQuoter)

// WithYahooURL overrides the Yahoo chart endpoint prefix.
func WithYahooURL(u string) Option { return func(q *HTTPQuoter) { q.yahooURL = u } }

// WithStooqURL overrides the stooq CSV endpoint.
func WithStooqURL(u string) Option { return func(q *HTTPQuoter) { q.stooqURL = u } }

// WithFinnhubURL overrides the Finnhub API base.
func WithFinnhubURL(u string) Option { return func(q *HTTPQuoter) { q.finnhubURL = u } }

// WithFinnhubToken sets the Finnhub API token, enabling that provider
// ahead of the keyless endpoints. NewHTTPQuoter already reads it from
// TAXMAN_FINNHUB_TOKEN; this is for tests and explicit callers.
func WithFinnhubToken(t string) Option { return func(q *HTTPQuoter) { q.finnhubToken = t } }

// WithClock overrides time.Now, for deterministic cache-expiry tests.
func WithClock(now func() time.Time) Option { return func(q *HTTPQuoter) { q.now = now } }

// WithHTTPClient overrides the *http.Client used for every endpoint.
func WithHTTPClient(c *http.Client) Option { return func(q *HTTPQuoter) { q.client = c } }

// WithCache attaches a durable last-known-price store. Every successful
// fetch is written to it, and an in-process cache miss is served from
// it — as a live quote when the stored row is still inside the TTL, or
// as the Stale fallback when every provider then fails.
func WithCache(c Cache) Option { return func(q *HTTPQuoter) { q.persist = c } }

// NewHTTPQuoter builds an HTTPQuoter with the production defaults,
// applying any options in order. The Finnhub token is read from
// TAXMAN_FINNHUB_TOKEN unless a WithFinnhubToken option overrides it.
func NewHTTPQuoter(opts ...Option) *HTTPQuoter {
	q := &HTTPQuoter{
		client:        &http.Client{Timeout: defaultTimeout},
		yahooURL:      defaultYahooURL,
		stooqURL:      defaultStooqURL,
		finnhubURL:    defaultFinnhubURL,
		finnhubToken:  strings.TrimSpace(os.Getenv(finnhubTokenEnv)),
		ttl:           defaultTTL,
		now:           time.Now,
		cache:         map[string]cacheEntry{},
		currencyCache: map[string]string{},
	}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

// Quote returns the latest price for symbol. A cache hit inside the
// TTL makes no HTTP request. On a miss it tries each provider in turn
// (Finnhub if configured, then Yahoo, then stooq); if all fail it
// returns the last cached quote with Stale set, or an error when
// nothing is cached.
//
// With a Cache attached (WithCache), an in-process miss first consults
// the durable store: a row still inside the TTL is served straight
// back, and an older row is held as the Stale fallback. Every
// successful fetch is written through to the store.
func (q *HTTPQuoter) Quote(ctx context.Context, symbol string) (Quote, error) {
	now := q.now()

	q.mu.Lock()
	entry, cached := q.cache[symbol]
	q.mu.Unlock()

	if cached && now.Sub(entry.fetchedAt) < q.ttl {
		return entry.quote, nil
	}

	// In-process miss (typically a fresh process). Seed from the
	// durable cache: a still-fresh row needs no HTTP call at all, and a
	// stale one gives fetch a fallback to serve if the providers fail.
	if !cached && q.persist != nil {
		if pq, fetchedAt, ok, loadErr := q.persist.Load(symbol); loadErr == nil && ok {
			entry, cached = cacheEntry{quote: pq, fetchedAt: fetchedAt}, true
			q.mu.Lock()
			q.cache[symbol] = entry
			q.mu.Unlock()
			if now.Sub(fetchedAt) < q.ttl {
				return pq, nil
			}
		}
	}

	quote, err := q.fetch(ctx, symbol)
	if err != nil {
		if cached {
			stale := entry.quote
			stale.Stale = true
			return stale, nil
		}
		return Quote{}, fmt.Errorf("prices: %s: %w", symbol, err)
	}

	quote.Symbol = symbol
	quote.AsOf = now
	quote.Stale = false

	q.mu.Lock()
	q.cache[symbol] = cacheEntry{quote: quote, fetchedAt: now}
	q.mu.Unlock()

	if q.persist != nil {
		// Best-effort: a cache write failure must not fail the quote.
		_ = q.persist.Store(symbol, quote, now)
	}

	return quote, nil
}

type namedFetcher struct {
	name string
	fn   func(ctx context.Context, symbol string) (Quote, error)
}

// providers is the ordered fetch chain: Finnhub first when a token is
// set (reliable, credentialed), then the two keyless endpoints.
func (q *HTTPQuoter) providers() []namedFetcher {
	var fs []namedFetcher
	if q.finnhubToken != "" {
		fs = append(fs, namedFetcher{"finnhub", q.fetchFinnhub})
	}
	return append(fs,
		namedFetcher{"yahoo", q.fetchYahoo},
		namedFetcher{"stooq", q.fetchStooq},
	)
}

// fetch tries each provider in order, returning the first success or,
// if none answered, every provider's error joined together.
func (q *HTTPQuoter) fetch(ctx context.Context, symbol string) (Quote, error) {
	var errs []string
	for _, p := range q.providers() {
		quote, err := p.fn(ctx, symbol)
		if err == nil {
			return quote, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", p.name, err))
	}
	return Quote{}, errors.New(strings.Join(errs, "; "))
}

// fetchFinnhub reads Finnhub's /quote ("c" is the current price) and,
// best-effort, /stock/profile2 for the listing currency (cached — a
// listing's currency never changes). A missing profile leaves
// Currency "", same as the stooq path: the caller then treats a
// non-EUR price as unconvertible rather than guessing.
func (q *HTTPQuoter) fetchFinnhub(ctx context.Context, symbol string) (Quote, error) {
	base := strings.TrimRight(q.finnhubURL, "/")
	u := base + "/quote?symbol=" + url.QueryEscape(symbol) + "&token=" + url.QueryEscape(q.finnhubToken)
	resp, err := q.get(ctx, u)
	if err != nil {
		return Quote{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var body struct {
		C json.Number `json:"c"` // current price
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Quote{}, fmt.Errorf("decoding /quote: %w", err)
	}
	price, err := decimal.NewFromString(body.C.String())
	if err != nil || !price.IsPositive() {
		// Finnhub returns {"c":0} for an unknown or unsupported symbol.
		return Quote{}, fmt.Errorf("no price for %q", symbol)
	}

	return Quote{Price: price, Currency: q.finnhubCurrency(ctx, base, symbol)}, nil
}

// finnhubCurrency returns symbol's listing currency from the cached
// company profile, fetching it once. "" when the profile call fails
// or omits a currency.
func (q *HTTPQuoter) finnhubCurrency(ctx context.Context, base, symbol string) string {
	q.mu.Lock()
	cur, looked := q.currencyCache[symbol]
	q.mu.Unlock()
	if looked {
		return cur
	}

	cur = ""
	u := base + "/stock/profile2?symbol=" + url.QueryEscape(symbol) + "&token=" + url.QueryEscape(q.finnhubToken)
	if resp, err := q.get(ctx, u); err == nil {
		func() {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return
			}
			var body struct {
				Currency string `json:"currency"`
			}
			if json.NewDecoder(resp.Body).Decode(&body) == nil {
				cur = strings.ToUpper(strings.TrimSpace(body.Currency))
			}
		}()
	}

	q.mu.Lock()
	q.currencyCache[symbol] = cur
	q.mu.Unlock()
	return cur
}

// fetchYahoo reads Yahoo Finance's v8 chart JSON, using only the meta
// block:
//
//	{"chart":{"result":[{"meta":{"symbol":"AAPL",
//	  "regularMarketPrice":195.89,"currency":"USD"}}],"error":null}}
func (q *HTTPQuoter) fetchYahoo(ctx context.Context, symbol string) (Quote, error) {
	u := strings.TrimRight(q.yahooURL, "/") + "/" + url.PathEscape(symbol)
	resp, err := q.get(ctx, u)
	if err != nil {
		return Quote{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var body struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol             string      `json:"symbol"`
					RegularMarketPrice json.Number `json:"regularMarketPrice"`
					Currency           string      `json:"currency"`
				} `json:"meta"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		} `json:"chart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Quote{}, fmt.Errorf("decoding response: %w", err)
	}
	if len(body.Chart.Result) == 0 {
		return Quote{}, fmt.Errorf("no quote for %q", symbol)
	}

	meta := body.Chart.Result[0].Meta
	price, err := decimal.NewFromString(meta.RegularMarketPrice.String())
	if err != nil {
		return Quote{}, fmt.Errorf("price %q not a number: %w", meta.RegularMarketPrice.String(), err)
	}
	if !price.IsPositive() {
		return Quote{}, fmt.Errorf("non-positive price for %q", symbol)
	}

	return Quote{Price: price, Currency: strings.ToUpper(strings.TrimSpace(meta.Currency))}, nil
}

// fetchStooq reads stooq's light CSV quote:
//
//	Symbol,Date,Time,Open,High,Low,Close,Volume
//	AAPL.US,2024-01-05,22:00:04,181.99,182.76,180.17,181.18,62303020
//
// stooq's light endpoint carries no currency, so the returned Quote's
// Currency is "" — a caller needing euro must skip it rather than
// assume.
func (q *HTTPQuoter) fetchStooq(ctx context.Context, symbol string) (Quote, error) {
	u := q.stooqURL + "?s=" + url.QueryEscape(strings.ToLower(symbol)) + "&f=sd2t2ohlcv&e=csv"
	resp, err := q.get(ctx, u)
	if err != nil {
		return Quote{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	records, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		return Quote{}, fmt.Errorf("parsing csv: %w", err)
	}
	if len(records) < 2 {
		return Quote{}, fmt.Errorf("no data row for %q", symbol)
	}

	header, row := records[0], records[1]
	closeIdx := -1
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), "close") {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 || closeIdx >= len(row) {
		return Quote{}, fmt.Errorf("no close column for %q", symbol)
	}

	closeStr := strings.TrimSpace(row[closeIdx])
	price, err := decimal.NewFromString(closeStr)
	if err != nil {
		return Quote{}, fmt.Errorf("close %q not a number for %q: %w", closeStr, symbol, err)
	}
	if !price.IsPositive() {
		return Quote{}, fmt.Errorf("non-positive close for %q", symbol)
	}

	return Quote{Price: price, Currency: ""}, nil
}

func (q *HTTPQuoter) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json,text/csv,*/*")
	return q.client.Do(req)
}
