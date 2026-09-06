// Package prices fetches a last-price-per-share for a market symbol
// from public HTTP endpoints, for the portfolio page's live
// market-value view (SPEC.md §9).
//
// Providers are tried in order: Finnhub first when TAXMAN_FINNHUB_TOKEN
// is set (its free tier is reliable where the keyless endpoints are
// rate-limited or IP-blocked), then Yahoo Finance's keyless v8 chart
// endpoint, then stooq's keyless CSV.
//
// It is a leaf utility, like internal/fx: it must not import
// internal/engine, internal/audit, internal/ledger, or internal/web,
// and it never touches the ledger. The only things it sends outbound
// are a ticker symbol the user mapped on the portfolio page and, when
// configured, the Finnhub token — never a quantity, cost basis, or
// account identifier.
//
// A quote is returned in the instrument's native currency. Converting
// to euro is the caller's job (internal/fx). When every provider
// fails, the last cached quote is returned with Stale set rather than
// an error, so the portfolio page degrades to last-known prices
// instead of going blank.
//
// The in-process cache is lost on restart, which is worst exactly
// during a run of provider 429s. HTTPQuoter.WithCache attaches a
// durable Cache (SQLiteCache, backed by the price_quotes table) that
// is written on every successful fetch and read back on an in-process
// miss, so last-known prices survive a restart. database/sql is the
// only dependency this adds; the package stays a leaf.
package prices
