// Package ibkr parses Interactive Brokers CSV trade exports into
// ledger.Transaction records. Two layouts are recognized:
//
//   - the flat trades export (a Flex Query / "Transactions" report):
//     one header row, one row per fill, no currency column — each
//     price is restated in euro using the row's FXRateToBase (see
//     docs/adr/0002). This is the layout confirmed against a real
//     export.
//   - the multi-section Activity Statement: a Trades section
//     interleaved with others; still supported but not verified
//     against a real export.
//
// Dividend and interest sections are out of scope (SPEC.md §1
// Non-goals). Currency-conversion / cash rows in the flat export
// (blank ISIN) are skipped with a warning, not imported as holdings.
package ibkr
