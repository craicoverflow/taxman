// Package traderepublic builds ledger.Transaction records for Trade
// Republic interest credits.
//
// Trade Republic's app exports a PDF statement, not a clean
// machine-readable interest feed, so — exactly like internal/ingest/n26
// — this package provides manual entry (NewInterestCredit) rather than
// guessing at a format that may change. A Parse(io.Reader) can be added
// here later if a stable CSV export is confirmed, mirroring degiro,
// ibkr, and etrade; this is an incomplete parser surface, not a
// placeholder to discard.
package traderepublic
