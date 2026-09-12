// Package fx converts foreign-currency amounts to euro using the
// European Central Bank's euro foreign-exchange reference rates.
//
// The full daily history (eurofxref-hist.csv, published by the ECB) is
// embedded in the binary, so conversion works entirely offline as long
// as the embedded file covers the date being converted. The file
// lists, for each TARGET business day since 1999, how many units of
// each currency one euro bought that day; fx inverts that to "euro per
// unit" for callers.
//
// Revenue accepts the ECB reference rate for converting foreign
// transactions on a CGT/exit-tax computation, which is why this is the
// source rather than a broker's own contract-note rate.
//
// Refreshing the data: a date past what the embedded file covers
// triggers one best-effort live download of
// https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist.zip at
// lookup time (see refresh.go), and the result is cached under
// os.UserCacheDir()/taxman so later runs don't refetch it. Set
// TAXMAN_FX_OFFLINE=1 to disable this and get the original
// offline-only behaviour, in which case refreshing the file is a
// manual step: re-download the zip above and replace
// eurofxref-hist.csv. See docs/adr/0001-fx-conversion.md and
// docs/adr/0002-fx-runtime-refresh.md.
package fx
