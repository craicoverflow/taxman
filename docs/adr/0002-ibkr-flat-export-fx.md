# ADR 0002 — IBKR flat trades export: convert prices with the file's own FX rate

Status: accepted
Date: 2026-09-05

## Context

IBKR's `All_Transactions.csv` export is a flat Flex-Query-style
"Transactions" report, not the multi-section Activity Statement
`internal/ingest/ibkr` was originally written against. Its columns:

```
ClientAccountID, TradeDate, OrderTime, Symbol, ISIN, ListingExchange,
Exchange, Quantity, TradePrice, TradeMoney, NetCash, FXRateToBase
```

The parser didn't recognize the header (auto-detection failed) and,
forced to `--platform ibkr`, imported zero rows (it scans for a
`Trades` section that isn't there).

Two problems specific to this layout:

1. **No currency column.** ADR 0001's approach — store the native
   currency, convert to EUR in `engine` at the ECB reference rate for
   the transaction date — needs an ISO currency code. This export has
   none. It only gives `FXRateToBase`: the rate from the fill's trade
   currency to the account base currency (EUR) on that date.
2. **Cash / FX rows.** ~60% of the file's rows are `EUR.USD`
   currency conversions with a blank ISIN — not securities trades.

## Decision

Support the flat export as a second recognized IBKR layout
(`Detect` matches either header; `Parse` dispatches on which it sees).

**FX:** restate each price in euro at import time —
`Price = |TradePrice| * FXRateToBase`, `Currency = "EUR"`. This uses
IBKR's own execution rate rather than the ECB rate ADR 0001 prefers,
because:

- there is no currency code to look an ECB rate up by;
- `FXRateToBase` is the rate that actually applied to the fill, so the
  euro cost basis it produces matches what the account did;
- it is currency-agnostic — correct whether the trade was in USD, GBP,
  or anything else — since the rate is always *to base*.

`TradeMoney` / `NetCash` are left unused (fees aren't modelled here,
consistent with every other parser).

**Date:** `TradeDate` (the CGT-relevant date), with the time-of-day
taken from `OrderTime` so same-day fills sort in execution order for
FIFO. `OrderTime`'s own date part (which can be the prior day) is
ignored.

**Cash rows:** rows with a blank ISIN are counted and skipped with a
non-fatal warning (`ibkr.Parse` now returns warnings, like
`degiro.Parse`), never imported as holdings.

## Consequences

- Such an export is mostly `EUR.USD`-style cash-conversion rows; those
  are skipped with a warning and the securities trades imported.
  Re-import is idempotent by fingerprint.
- IBKR figures in the ledger are euro already, so `engine`'s
  ECB-conversion path never touches them — IBKR trades and, say,
  Degiro USD trades are converted by different rate sources. Acceptable
  for a single-user tool; noted here so it isn't a surprise later.
- The unverified Activity Statement path is still there, now clearly
  marked as the un-checked one.
- If a future export includes a currency column, prefer reviving the
  ADR 0001 path for it rather than extending this one.
