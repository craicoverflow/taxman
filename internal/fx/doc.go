// Package fx converts foreign-currency amounts to euro using the
// European Central Bank's euro foreign-exchange reference rates.
//
// The full daily history (eurofxref-hist.csv, published by the ECB) is
// embedded in the binary, so conversion works entirely offline. The
// file lists, for each TARGET business day since 1999, how many units
// of each currency one euro bought that day; fx inverts that to "euro
// per unit" for callers.
//
// Revenue accepts the ECB reference rate for converting foreign
// transactions on a CGT/exit-tax computation, which is why this is the
// source rather than a broker's own contract-note rate.
//
// Refreshing the data: re-download
// https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.zip and
// replace eurofxref-hist.csv. There is deliberately no code path that
// fetches it at runtime — see docs/adr/0001-fx-conversion.md.
package fx
