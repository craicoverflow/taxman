// Package tickers maps a ledger instrument identifier (ISIN or
// platform-specific ID) to a market symbol a price provider
// understands. Entries are user-entered on the portfolio page and
// never inferred from instrument metadata — a wrong symbol silently
// misprices the position. See SPEC.md §9.
package tickers
