// Package valuations stores the market value of a fund holding at an
// 8-year deemed-disposal anniversary.
//
// Under TCA 1997 s.747E(6) a material interest in an offshore fund is
// deemed disposed of at the end of each 8-year period, and the gain is
// "the value of the units at the time less their cost of acquisition"
// (Revenue TDM Part 27-04-01 §4.1.5). That anniversary value is an
// input taxman cannot derive: internal/prices is a live-quote client
// deliberately kept out of internal/engine and internal/audit (SPEC.md
// §4, §9), and its providers serve a last price, not an arbitrary
// historical close.
//
// So the value is user-entered, exactly like a holding's
// classification (internal/classify) or its ticker mapping
// (internal/tickers): there is no heuristic and no fallback. A lot
// whose anniversary has passed with no valuation on record blocks its
// own computation with an engine.MissingValuationError rather than
// being guessed at or silently skipped.
package valuations
