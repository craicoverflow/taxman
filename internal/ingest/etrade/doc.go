// Package etrade parses ETRADE CSV exports, including RSU vest
// records, into ledger.Transaction records. It also provides
// NewRSUVest for entering a single vest by hand (manual.go) when the
// export on hand doesn't carry vests in an importable shape.
package etrade
