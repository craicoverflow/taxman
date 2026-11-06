# Single-trade entry (stock/ETF)

## Problem Statement

How might we let a user record one buy or sell of a stock or ETF by hand,
with CSV-grade fidelity (instrument, currency, classification), without
double-counting when the broker's CSV is imported later?

## Recommended Direction

One Buy/Sell trade form replacing the E*TRADE-only `/sale` handler (and
giving buys a manual path for the first time). `/rsu` stays separate: a
vest is an acquisition at FMV, a different shape. Rows go in as a batch
grid, reusing the `/interest` all-or-nothing validation pattern, since a
top-up between exports is often several trades.

The instrument is chosen from holdings already in the ledger, or a new one
is entered with an ISIN/ticker **and** an explicit classification — never
guessed, never defaulted. Entries are stored on `PlatformManual`, so the
audit trail plainly shows "typed in, not exported".

CSV import stays first-class and unchanged. Exact-fingerprint dedupe is
kept, but note the fingerprint includes the platform, so a `PlatformManual`
row never exact-matches an imported one. The near-match check is therefore
the only double-count protection: it flags an import row that resembles a
manual entry (same alias-resolved instrument, type, date and quantity,
**price ignored**) so the user resolves it rather than the system guessing.

Every write path also guards against overselling: a sell larger than the
held quantity is a hard engine error that would break the whole dashboard,
so it is refused at entry.

Fees are deliberately out of this feature (see Not Doing). Until they
ship, the form states "fees not included" beside the price field so a
zero fee is never silent.

## Key Assumptions to Validate

- [ ] A `PlatformManual` entry plus a near-match warning is enough to
      prevent double-counting. Test: import a CSV containing a trade
      already entered by hand.
- [ ] Manual ETF buys of real instruments key the same way Degiro/IBKR do
      (ISIN vs platform id vs symbol — IBKR and E*TRADE use the symbol, no
      ISIN). Test: fabricated fixture per platform.
- [ ] Manual buys give `/sale` lots to match, end to end through FIFO and
      the audit record. Test: golden fixture for buy → partial sell.

## MVP Scope

In: Buy and Sell, batch grid, holding picker plus new-instrument with
explicit classification, edit/delete of manual trades (as manual interest
credits have), near-match import warning, golden fixture, `docs/maths.md`
note and a SPEC section.

Out: everything below.

## Not Doing (and Why)

- **Fees / commission** — not modelled anywhere today (no field on
  `ledger.Transaction`, IBKR parser says "Commission is not modelled",
  Degiro's fee column is defined but unused, engine and `maths.md` are
  silent). Adding it is a cross-cutting change: schema migration, all
  parsers, engine basis/proceeds, `taxrules`, golden fixture, and a
  cited Revenue TDM source first. It ships as its own feature, and fixes
  the same understatement in existing imports. Entering fees only on
  manual trades would make one trade compute differently depending on how
  it was entered.
- **Dividends, splits, transfers** — each has its own tax mechanics.
- **Auto-reconcile / "imports win"** — guessing wrong silently misstates tax.
- **Ticker lookup that fills ISIN or classification** — forbidden by the
  never-guess rule.
- **Paste-a-line parser** — adds a mistake-prone surface; revisit later.
- **Replacing `/rsu`** — different semantics (FMV acquisition).

## Open Questions

- Which Revenue TDM section covers allowable acquisition and disposal
  costs for shares and ETFs? (Gate for the separate fees feature.)
- Should the near-match warning appear at import time only, or persist on
  the dashboard until resolved?
- What instrument identifier should a manual ETF buy use when the user's
  broker export keys by ISIN but they only know the ticker? (Must not be
  inferred.)
