package prices

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// historyRange is the fixed lookback asked of the provider. It is a
// constant on purpose: requesting "from my first purchase" would send
// that date outbound, and the only thing this package may send is the
// symbol. Ten years is the longest range Yahoo still serves at daily
// granularity (its "max" range drops to quarterly points); the caller
// trims to the dates it needs locally.
const historyRange = "10y"

// historyTTL is how long a fetched series is reused before refetching.
// Daily closes change once a day, so this is deliberately long — it
// also keeps the trend chart from spending Yahoo's rate-limit headroom
// that the live quotes on the same page need.
const historyTTL = 6 * time.Hour

// Close is one daily closing price, in History.Currency. Date is the
// trading day at midnight UTC.
type Close struct {
	Date  time.Time
	Price decimal.Decimal
}

// History is a symbol's daily closing prices, oldest first, in the
// instrument's native currency. Like Quote, Stale is true when the
// providers failed and this is the last series from cache — AsOf is
// then when that series was fetched.
type History struct {
	Symbol   string
	Currency string
	Closes   []Close
	AsOf     time.Time
	Stale    bool
}

// Historian returns a symbol's price history: daily closes over years
// (History) or intraday prices over the last day or week (Intraday).
// internal/web depends on this interface so handler tests can inject a
// fake with no network.
type Historian interface {
	History(ctx context.Context, symbol string) (History, error)
	Intraday(ctx context.Context, symbol string, span Span) (History, error)
}

// Span is a short intraday lookback for Intraday.
type Span string

const (
	// Span1D covers at least the last 24 hours at 5-minute resolution.
	Span1D Span = "1d"
	// Span1W covers at least the last 7 days at 30-minute resolution.
	Span1W Span = "1w"
)

// intradaySpec is the fixed provider query and cache TTL for a Span.
// Like historyRange, the query is a constant: no ledger-derived time
// ever leaves the process, only the symbol. Each range is wider than
// its span (a 24-hour window can reach back over a weekend into the
// previous session), and the caller trims locally.
var intradaySpec = map[Span]struct {
	query string
	ttl   time.Duration
}{
	Span1D: {"range=5d&interval=5m", 5 * time.Minute},
	Span1W: {"range=1mo&interval=30m", 15 * time.Minute},
}

// HistoryCache is the durable counterpart of Cache for daily series:
// written after every successful fetch, read on an in-process miss, so
// a restart during a spell of provider rate-limiting still draws the
// chart. Errors are never fatal to a History call.
type HistoryCache interface {
	LoadHistory(symbol string) (h History, fetchedAt time.Time, ok bool, err error)
	StoreHistory(symbol string, h History, fetchedAt time.Time) error
}

// WithHistoryCache attaches a durable store for daily price series.
func WithHistoryCache(c HistoryCache) Option { return func(q *HTTPQuoter) { q.historyPersist = c } }

type historyEntry struct {
	history   History
	fetchedAt time.Time
}

// History returns symbol's daily closes over the last historyRange,
// from Yahoo's keyless chart endpoint (the only configured provider
// that serves history without a paid plan). A cache hit within
// historyTTL makes no request; on a failed fetch the last cached
// series is returned with Stale set, or an error when none exists.
func (q *HTTPQuoter) History(ctx context.Context, symbol string) (History, error) {
	now := q.now()

	q.mu.Lock()
	entry, cached := q.historyCache[symbol]
	q.mu.Unlock()
	if cached && now.Sub(entry.fetchedAt) < historyTTL {
		return entry.history, nil
	}

	if !cached && q.historyPersist != nil {
		if h, fetchedAt, ok, err := q.historyPersist.LoadHistory(symbol); err == nil && ok {
			entry, cached = historyEntry{history: h, fetchedAt: fetchedAt}, true
			q.mu.Lock()
			q.historyCache[symbol] = entry
			q.mu.Unlock()
			if now.Sub(fetchedAt) < historyTTL {
				return h, nil
			}
		}
	}

	h, err := q.fetchYahooSeries(ctx, symbol, "range="+historyRange+"&interval=1d", true)
	if err != nil {
		if cached {
			stale := entry.history
			stale.Stale = true
			return stale, nil
		}
		return History{}, fmt.Errorf("prices: history for %s: %w", symbol, err)
	}
	h.Symbol = symbol
	h.AsOf = now

	q.mu.Lock()
	q.historyCache[symbol] = historyEntry{history: h, fetchedAt: now}
	q.mu.Unlock()
	if q.historyPersist != nil {
		_ = q.historyPersist.StoreHistory(symbol, h, now) // best-effort
	}
	return h, nil
}

// Intraday returns symbol's prices at intraday resolution covering at
// least span, oldest first; each Close.Date is the bar's UTC timestamp.
// It is cached in-process only (a short TTL per span — the chart is
// meant to be current); on a failed fetch the last cached series is
// returned with Stale set, or an error when none exists.
func (q *HTTPQuoter) Intraday(ctx context.Context, symbol string, span Span) (History, error) {
	spec, ok := intradaySpec[span]
	if !ok {
		return History{}, fmt.Errorf("prices: unknown intraday span %q", span)
	}
	now := q.now()
	key := symbol + "|" + string(span)

	q.mu.Lock()
	entry, cached := q.historyCache[key]
	q.mu.Unlock()
	if cached && now.Sub(entry.fetchedAt) < spec.ttl {
		return entry.history, nil
	}

	h, err := q.fetchYahooSeries(ctx, symbol, spec.query, false)
	if err != nil {
		if cached {
			stale := entry.history
			stale.Stale = true
			return stale, nil
		}
		return History{}, fmt.Errorf("prices: %s prices for %s: %w", span, symbol, err)
	}
	h.Symbol = symbol
	h.AsOf = now

	q.mu.Lock()
	q.historyCache[key] = historyEntry{history: h, fetchedAt: now}
	q.mu.Unlock()
	return h, nil
}

// fetchYahooSeries reads a price series from Yahoo's v8 chart JSON:
//
//	{"chart":{"result":[{"meta":{"currency":"USD","gmtoffset":-18000},
//	  "timestamp":[1704465000,...],
//	  "indicators":{"quote":[{"close":[181.18,null,...]}]}}],"error":null}}
//
// query is a fixed range/interval pair. A null close (a halted or
// not-yet-settled bar) is skipped. With daily set, each timestamp is
// shifted by the exchange's gmtoffset before taking the calendar date,
// so a US session is dated by its own trading day, not the UTC day it
// ends on; otherwise the bar's UTC timestamp is kept. London listings
// quoted in pence ("GBp"/"GBX") are scaled to pounds so the series is
// in an ISO 4217 currency.
func (q *HTTPQuoter) fetchYahooSeries(ctx context.Context, symbol, query string, daily bool) (History, error) {
	u := strings.TrimRight(q.yahooURL, "/") + "/" + url.PathEscape(symbol) + "?" + query
	resp, err := q.get(ctx, u)
	if err != nil {
		return History{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return History{}, fmt.Errorf("yahoo: status %d", resp.StatusCode)
	}

	var body struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Currency  string `json:"currency"`
					GMTOffset int64  `json:"gmtoffset"`
				} `json:"meta"`
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []*json.Number `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return History{}, fmt.Errorf("yahoo: decoding history: %w", err)
	}
	if len(body.Chart.Result) == 0 || len(body.Chart.Result[0].Indicators.Quote) == 0 {
		return History{}, fmt.Errorf("yahoo: no history for %q", symbol)
	}
	res := body.Chart.Result[0]
	closes := res.Indicators.Quote[0].Close
	if len(closes) != len(res.Timestamp) {
		return History{}, fmt.Errorf("yahoo: %d timestamps but %d closes for %q", len(res.Timestamp), len(closes), symbol)
	}

	currency := strings.TrimSpace(res.Meta.Currency)
	scale := decimal.NewFromInt(1)
	switch currency {
	case "GBp", "GBX":
		currency, scale = "GBP", decimal.New(1, -2)
	default:
		currency = strings.ToUpper(currency)
	}
	if currency == "" {
		return History{}, fmt.Errorf("yahoo: history for %q carries no currency", symbol)
	}

	out := History{Currency: currency, Closes: make([]Close, 0, len(closes))}
	var last time.Time
	for i, c := range closes {
		if c == nil {
			continue
		}
		price, err := decimal.NewFromString(c.String())
		if err != nil || !price.IsPositive() {
			continue
		}
		var day time.Time
		if daily {
			t := time.Unix(res.Timestamp[i]+res.Meta.GMTOffset, 0).UTC()
			day = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		} else {
			day = time.Unix(res.Timestamp[i], 0).UTC()
		}
		if !day.After(last) {
			// Yahoo appends the live price as an extra point that can
			// share a date (or bar) with the last settled one; keep the later.
			if day.Equal(last) {
				out.Closes[len(out.Closes)-1].Price = price.Mul(scale)
			}
			continue
		}
		out.Closes = append(out.Closes, Close{Date: day, Price: price.Mul(scale)})
		last = day
	}
	if len(out.Closes) == 0 {
		return History{}, fmt.Errorf("yahoo: empty history for %q", symbol)
	}
	return out, nil
}

// LoadHistory reads symbol's row from price_history (migration 0010).
func (c *SQLiteCache) LoadHistory(symbol string) (History, time.Time, bool, error) {
	var currency, closesJSON, fetchedStr string
	err := c.db.QueryRow(
		`SELECT currency, closes, fetched_at FROM price_history WHERE symbol = ?`, symbol,
	).Scan(&currency, &closesJSON, &fetchedStr)
	if err == sql.ErrNoRows {
		return History{}, time.Time{}, false, nil
	}
	if err != nil {
		return History{}, time.Time{}, false, fmt.Errorf("prices: loading cached history %s: %w", symbol, err)
	}
	fetchedAt, err := time.Parse(time.RFC3339, fetchedStr)
	if err != nil {
		return History{}, time.Time{}, false, fmt.Errorf("prices: cached history fetched_at %q for %s: %w", fetchedStr, symbol, err)
	}
	var pairs [][2]string
	if err := json.Unmarshal([]byte(closesJSON), &pairs); err != nil {
		return History{}, time.Time{}, false, fmt.Errorf("prices: cached history for %s: %w", symbol, err)
	}
	h := History{Symbol: symbol, Currency: currency, AsOf: fetchedAt, Closes: make([]Close, 0, len(pairs))}
	for _, p := range pairs {
		d, err := time.Parse("2006-01-02", p[0])
		if err != nil {
			return History{}, time.Time{}, false, fmt.Errorf("prices: cached history date %q for %s: %w", p[0], symbol, err)
		}
		v, err := decimal.NewFromString(p[1])
		if err != nil {
			return History{}, time.Time{}, false, fmt.Errorf("prices: cached history close %q for %s: %w", p[1], symbol, err)
		}
		h.Closes = append(h.Closes, Close{Date: d, Price: v})
	}
	return h, fetchedAt, true, nil
}

// StoreHistory upserts symbol's row in price_history.
func (c *SQLiteCache) StoreHistory(symbol string, h History, fetchedAt time.Time) error {
	pairs := make([][2]string, len(h.Closes))
	for i, cl := range h.Closes {
		pairs[i] = [2]string{cl.Date.Format("2006-01-02"), cl.Price.String()}
	}
	b, err := json.Marshal(pairs)
	if err != nil {
		return fmt.Errorf("prices: encoding history for %s: %w", symbol, err)
	}
	_, err = c.db.Exec(
		`INSERT INTO price_history (symbol, currency, closes, fetched_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(symbol) DO UPDATE SET
		   currency = excluded.currency,
		   closes = excluded.closes,
		   fetched_at = excluded.fetched_at`,
		symbol, h.Currency, string(b), fetchedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("prices: caching history for %s: %w", symbol, err)
	}
	return nil
}
