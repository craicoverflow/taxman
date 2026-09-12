# ADR 0001 — Foreign-currency conversion via embedded ECB reference rates

Status: accepted; the "no runtime fetch" clause is superseded by
[ADR 0002](0002-fx-runtime-refresh.md) (2026-09-12).
Date: 2026-09-03

## Context

The engine's FIFO matcher (CGT and exit tax) required every transaction
to be EUR-denominated and returned an explicit error otherwise — FX
conversion was deferred because "no FX rate source exists" (SPEC.md §6,
tasks/plan.md 4.2). Real ledgers hit this immediately: a USD-priced
iShares S&P 500 holding can't be computed at all.

SPEC.md §2 already fixes the model: a lot's cost basis and a disposal's
proceeds are each converted to EUR "at transaction-date FX rate". What
was missing was where the rate comes from.

## Decision

**Source:** the European Central Bank euro foreign-exchange reference
rates (`eurofxref-hist.csv`), the full daily history since 1999.
Revenue publishes and accepts these rates for converting foreign
transactions, which makes them more defensible than a broker's own
contract-note rate and avoids per-platform parser work.

**Distribution:** the CSV (~1.6 MB) is committed to the repo and
`//go:embed`-ed into the binary (`internal/fx`). Conversion works fully
offline. This is public reference data, not account data, so it does
not fall under SPEC.md §6's "never commit real financial data" rule.

**No runtime fetch** *(superseded — see ADR 0002)*. There is
deliberately no code path that downloads the file. Refreshing it is a
manual step (re-download the ECB zip, replace the file), keeping the
"ask first before anything network-capable" boundary intact. A
dedicated refresh subcommand can be added later if the manual step
proves annoying — that change would introduce the first network call
and needs its own sign-off.

**Lookup semantics** (`fx.Rate(currency, date)`):
- returns euro-per-unit (the ECB file lists units-per-euro; `fx`
  inverts);
- `EUR`/`""` → 1;
- weekend/holiday dates resolve to the most recent prior publication
  day, bounded to 10 days (covers the Christmas/New-Year closure);
- errors — never a silent guess — on an unknown currency, a date before
  1999, a date more than 7 days past the last published rate (file is
  stale), or a retired currency queried long after its column went
  `N/A`.

**Integration:** `internal/engine/currency.go::toEURPrices` restates
every non-EUR transaction's price in euro at its own transaction-date
rate, up front in the shared matcher, so CGT and exit tax both operate
on euro figures. A transaction whose rate can't be resolved is a hard
error scoped to that holding (the dashboard already isolates per-holding
compute errors).

## Consequences

- Non-EUR holdings now compute end to end.
- Converted figures are irrational-looking decimals (division by the
  rate); golden fixtures pin engine-generated values with the real ECB
  rates as the anchor (`testdata/golden/cgt_fx_conversion`).
- The embedded CSV goes stale. `fx.Source()` reports its last date for
  audit records, and a too-old file surfaces as an explicit error at
  computation time rather than a wrong number.
- DIRT is unchanged — interest credits are EUR in practice and keep
  their own non-EUR guard.
- Not yet done: threading the applied rate + `fx.Source()` into
  `audit.Record` so the audit trail names the FX rate behind each
  figure. Tracked as follow-up; the conversion itself is correct and
  tested now.
