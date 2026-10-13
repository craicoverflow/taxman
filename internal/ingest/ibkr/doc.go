// Package ibkr parses Interactive Brokers CSV trade exports into
// ledger.Transaction records. Three layouts are recognized:
//
//   - the flat trades export (a Flex Query / "Transactions" report):
//     one header row, one row per fill, no currency column — each
//     price is restated in euro using the row's FXRateToBase (see
//     docs/adr/0002). This is the layout confirmed against a real
//     export.
//   - the Transaction History report: shares the Activity Statement's
//     first row, with a "Transaction History" section of Buy/Sell rows
//     keyed by ticker Symbol (no ISIN, so Instrument is the ticker —
//     classify it by ticker) and a Price Currency column, so prices
//     stay native and the engine converts at the ECB rate (ADR 0001).
//     Forex legs, adjustments and deposits are skipped with a warning;
//     an unrecognized Transaction Type is an error. Confirmed against a
//     real export.
//   - the multi-section Activity Statement: a Trades section
//     interleaved with others; still supported but not verified
//     against a real export.
//
// Dividend and interest sections are out of scope (SPEC.md §1
// Non-goals). Currency-conversion / cash rows in the flat export
// (blank ISIN) are skipped with a warning, not imported as holdings.
package ibkr
