// Package n26 builds ledger.Transaction records for N26 interest
// credits.
//
// As of task 6.3 (tasks/plan.md), it was unconfirmed whether N26
// offers a clean CSV export, so this package provides manual entry
// (NewInterestCredit) as the safe default rather than guessing at a
// CSV format that might not exist. If a CSV export is confirmed
// later, a Parse(io.Reader) can be added here alongside the manual
// path, mirroring internal/ingest/degiro, ibkr, and etrade — this is
// not a placeholder to be thrown away, just an incomplete parser
// surface.
package n26
