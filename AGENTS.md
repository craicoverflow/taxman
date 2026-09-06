# AGENTS.md

Context for AI coding agents (and humans) working in this repository.
[`SPEC.md`](SPEC.md) is the authoritative contract — this file is an
orientation layer over it, with the emphasis the spec spreads across
nine sections pulled together in one place: **what taxman computes, and
why the ETF classification question is the whole point.**

If anything here disagrees with `SPEC.md`, `SPEC.md` wins.

---

## 1. What this application is

`taxman` is a **single-user, self-hosted tool that turns raw broker CSV
exports into a running Irish investment-tax liability** — Capital Gains
Tax (CGT), fund **exit tax**, and Deposit Interest Retention Tax (DIRT)
— with an audit trail from every figure back to the source
transactions and the versioned tax rule that produced it.

It replaces a hand-maintained spreadsheet. It is built for one person's
affairs (`craicoverflow`), not licensed or hardened as tax software for
anyone else, and it deliberately does **not**:

- perform any tax arithmetic via an LLM or chat layer (an agent may
  *explain* a computed figure by walking its audit record; it must
  never *produce* a number),
- file or submit anything to Revenue / ROS,
- track dividend or distribution income (recorded, not processed, in
  v1),
- guess a holding's tax classification, or guess a ticker symbol.

Single Go binary, embedded SQLite, `html/template` web UI with no build
step. `taxman serve` gives a local dashboard (tax liability) and a
separate `/portfolio` page (live market value — see §6).

Module path: `github.com/craicoverflow/taxman`.

---

## 2. Why ETFs are the hard problem

For an Irish investor, **the single biggest tax question about any
holding is which regime it falls under**, and ETFs are exactly where
that question is hard and expensive to get wrong. taxman models two
mutually exclusive regimes, plus a blocking "don't know" state:

| Classification | Applies to (typical) | Regime taxman applies |
|---|---|---|
| `CGT_ASSET` | Individual shares; most US-domiciled ETFs; RSU shares | **CGT** — 33%, one €1,270 annual exemption **per year across all holdings**, losses net across holdings and carry forward, FIFO lot matching |
| `EXIT_TAX_FUND` | Irish/EU-domiciled UCITS ETFs and offshore funds in equivalent regimes | **Exit tax** — 41% (→ 38% from 1 Jan 2026), **no annual exemption**, **no loss relief per chargeable event** (a losing disposal contributes zero; it never nets against a gaining one), **8-year deemed disposal** |
| `UNCLASSIFIED` | Anything with no manual override on record | **None** — computation refuses to run for that holding and says so. Never defaulted to either regime. |

### What makes the ETF case genuinely different, not just a rate change

- **No €1,270 annual exemption.** CGT's first €1,270 of gains per year
  is exempt; exit tax has none. `internal/engine/exittax.go` never
  reads `AnnualExemption`.
- **No loss relief, no offsetting.** A CGT loss is a real negative
  `Disposal.Gain` that nets against other CGT gains (across holdings, in
  `AggregateCGTYear`) and carries forward. An exit-tax loss is dead —
  `summarizeExitTax` adds only the positive per-disposal gains, so
  `ExitTaxResult.TaxableGain` cannot be reduced by any loss, on this
  holding or another.
- **The 8-year deemed disposal.** Every fund lot is treated as sold and
  reacquired on the 8th anniversary of its acquisition (and every 8
  years after), triggering a tax charge even with no actual sale.
  `NextDeemedDisposalAnniversary` (`internal/engine/deemeddisposal.go`)
  tracks the dates today; **turning a reached anniversary into a
  liability figure, and the later actual-disposal credit/refund, is
  NOT built** — blocked pending a cited Revenue TDM source (task 4.9).
  That code path fails loudly with `"unverified rule: ..."` rather than
  computing a plausible-looking wrong number.
- **Rate transition by event date.** Exit tax is 41% before
  2026-01-01 and 38% on or after (Finance Act 2025). A disposal dated
  2025-12-31 resolves to 41% no matter when the computation runs —
  `taxrules.Lookup(KindExitTax, date)` resolves by the event's date,
  never `time.Now()`. There is a golden fixture for a fund straddling
  the boundary.

### taxman never guesses the classification

There is **no heuristic auto-classification** (no "ISIN starts with IE
→ fund"). Each holding is `UNCLASSIFIED` until a human sets it via
`taxman classify --set <isin> CGT_ASSET|EXIT_TAX_FUND` or the
dashboard form. `taxman classify --list-unclassified` surfaces
anything currently blocking computation. This is a hard product
principle ("flag rather than guess"), not a missing feature.

---

## 3. How a liability figure is computed

The pipeline, all in `internal/engine`, operating on immutable
`ledger.Transaction` rows:

1. **Lots.** Every `buy` and `rsu_vest` for an instrument becomes a
   **Lot**: quantity, acquisition date, and cost basis. An RSU vest's
   cost basis is its **vest-date fair market value** (so the vest
   income isn't taxed again as gain). A non-EUR transaction is restated
   in euro at the **ECB reference rate for the transaction's own date**
   (`internal/fx`, ADR 0001; IBKR flat exports use the export's own
   `FXRateToBase`, ADR 0002). An unresolvable rate is an explicit
   error, never a guess.
2. **FIFO matching.** Each `sell` is matched against the oldest open
   lots first (`matchDisposalsFIFO`), producing **Disposals**
   (`Date, Quantity, Proceeds, CostBasis, Gain`). `Gain = Proceeds −
   CostBasis`; it may be negative.
   - **CGT** losses net against gains — across disposals *and across
     holdings* — in the year-level aggregate (step 5), and unused net
     losses carry forward to later years.
   - **Exit tax** has no loss relief *per chargeable event*:
     `summarizeExitTax` sums only the **positive** per-disposal gains,
     so a losing disposal never nets against a gaining one (not even
     same fund, same year).
   - **Not implemented:** TCA **s.581** four-week
     sell-then-repurchase loss restriction. Plain FIFO is used and may
     be wrong in that window; blocked pending a Revenue TDM citation
     (task 4.8), same loud-failure discipline as deemed disposal.
3. **DIRT** is separate — no lots. `ComputeDIRT` sums
   `interest`-type credits (hand-entered savings interest, against an
   account the user names; see `internal/ingest/interest`) and applies
   the DIRT rate at the latest credit's date (33% from 2020; earlier
   years resolve to their own rate — see step 4). Interest must be
   EUR. No bank is modelled: DIRT does not turn on who paid it.
4. **Rate + exemption lookup.** `internal/taxrules` holds every rate as
   an **effective-dated schedule**, never a bare constant in `engine`:

   | Kind | Rate | Notes |
   |---|---|---|
   | `KindCGT` | 33% from 2012-12-06; 30%/25%/22%/20% earlier | `AnnualExemption` €1,270 (constant); pre-2013 entries **UNVERIFIED** |
   | `KindDIRT` | 33% from 2020; 35→41% back through 2014; 33→20% earlier | 2014–2020 step-down held with confidence; pre-2014 **UNVERIFIED** |
   | `KindExitTax` | 41% (2014–2025), **38% from 2026-01-01** (Finance Act 2025); 36%/33%/… earlier | pre-2013 entries **UNVERIFIED** |

   The **UNVERIFIED** historical entries were reconstructed from memory
   so `taxman backfill` has a defensible per-year rate rather than
   taxing every past year at today's rate — confirm against a Revenue
   TDM / Finance Act before trusting a figure for an earlier tax year.
   `taxrules.Lookup(kind, date)` returns the entry in force on `date`,
   or an **error** if the kind is unknown or the date precedes every
   entry — it never returns a zero-as-if-valid rate. A rate (not the
   exemption) can be overridden at runtime with `TAXMAN_CGT_RATE`,
   `TAXMAN_EXIT_TAX_RATE`, `TAXMAN_DIRT_RATE`, each a fraction like
   `0.38`; the override replaces that rate for **all** event dates, so
   it is only correct when every figure being computed falls under the
   new rate.
   Final tax figures are rounded **down to whole euro**
   (`engine/round.go`); everything upstream stays full-precision.
5. **Per-year computation.**
   - **CGT is year-level, not per holding.** `engine.CGTDisposals`
     FIFO-matches one holding's full history; `engine.AggregateCGTYear`
     then nets every CGT_ASSET holding for the selected year — **one**
     €1,270 exemption, gains/losses netted across holdings (TCA s.31),
     brought-forward losses applied only down to the exemption
     (s.601(4)), rate resolved at 31 December. The per-holding row
     shows realised gain only; the charge is reported once.
   - **Exit tax / DIRT** stay per holding: `ComputeExitTaxForYear` /
     `ComputeDIRTForYear`, matching full history but taxing only the
     selected year.
   - `cmd/taxman/report.go` and the dashboard `?year=` selector both
     call these — the split lives in `engine`, not in callers. The
     dashboard's "all years" view sums each year's own CGT charge
     (each keeps its own exemption + carry-forward chain).
6. **Audit.** Every figure `taxman report` (and the golden-fixture
   tests) produce links back through `internal/audit` to the disposals,
   lots, and rule versions behind it. The **only** exception is the
   live dashboard/portfolio summary, which is recomputed per request
   and not persisted (documented in SPEC §7).

---

## 4. Repository map

```
cmd/taxman/            CLI entrypoint; one file per subcommand (flag-based dispatch)
internal/
  ledger/              append-only transaction store; fingerprint dedup (idempotent import)
  ingest/
    route.go           platform auto-detection from CSV header shape
    degiro/ ibkr/ etrade/        one parser per platform (independent formats)
    etrade/                      also NewRSUVest — hand-entered vest (POST /rsu)
    interest/                    hand-entered interest credits, account named by the user
  classify/            CGT_ASSET / EXIT_TAX_FUND / UNCLASSIFIED override table
  taxrules/            effective-dated rate & exemption schedules + env overrides
  engine/              FIFO lot matching, CGT / exit-tax / DIRT, deemed-disposal clock, OpenPositions
  fx/                  ECB reference-rate EUR conversion (embedded eurofxref-hist.csv)
  audit/               links computed figures → transactions + rule versions
  prices/              live market quotes (Finnhub→Yahoo→stooq), in-proc + SQLite cache. LEAF — no engine/audit/ledger import
  web/                 HTTP handlers, dashboard + portfolio templates (inline html/template)
  db/                  embedded SQLite schema + migrations
testdata/golden/       synthetic input + expected-output fixture pairs (one per tax scenario)
testdata/fixtures/     synthetic per-platform sample exports (never real data)
docs/adr/              architecture decision records (FX conversion, IBKR FX)
tasks/plan.md, todo.md task breakdown with acceptance criteria + blocked items
SPEC.md                the contract
```

`taxrules` and `engine` are kept free of ingestion/web/DB dependencies
so their tests run as pure functions over lots and dates — that is what
makes golden-fixture TDD practical.

---

## 5. Commands

```
taxman serve [--port 8080] [--db path]   local dashboard (/) + portfolio (/portfolio)
taxman import <file> [--platform …] [--dry-run]   ingest one CSV export (auto-detects platform)
taxman backfill <dir>                     one-time historical bulk import (audit-distinct from import)
taxman classify [--list-unclassified] [--set <isin> <CGT_ASSET|EXIT_TAX_FUND>]
taxman report --year <YYYY> [--format text|json]   point-in-time liability report + audit refs
taxman validate --fixture <path>         run engine against a golden fixture, diff vs expected
taxman migrate [up|down]                 apply / roll back SQLite migrations
```

Dev (`Makefile`): `make build` · `make test` (`go test -race ./...`,
includes the `features/` BDD suite) · `make lint` (golangci-lint) ·
`make fuzz` (engine lot-matcher, native Go fuzzing) · `make bdd` /
`make regen-features` / `make docs` (the maths spec — §8, SPEC §14) ·
`make ci` (build + test + check-features + lint — run before calling any
change done) · `make dev` (`serve` under `air`, loads gitignored
`.env`).

---

## 6. The portfolio page is NOT tax

`GET /portfolio` (`internal/web` + `internal/prices`) shows current
market value — an allocation pie and cost-vs-market-value bars, in
euro — over currently-held, ticker-mapped positions. It exists to
answer "what is this worth today", which the tax dashboard never does.

- Prices come from public endpoints in order: **Finnhub** (only when
  `TAXMAN_FINNHUB_TOKEN` is set — the one sanctioned credentialed
  provider) → **Yahoo v8 chart** (keyless) → **stooq** (keyless).
- Only the **ticker symbol** (and, if set, the Finnhub token) ever
  leaves the process. Quantities and cost basis are joined to quotes
  **locally, after the fetch**. Ticker mappings
  (`instrument_tickers`, migration 0005) are **user-entered only** —
  never inferred from an instrument name or ISIN.
- Quotes are cached in-process (15 min TTL) and, since migration 0006,
  written through to a durable `price_quotes` table so last-known
  prices survive a restart during provider rate-limiting. A stale quote
  renders with an "as of" stamp; it is never an error that blanks the
  page.
- `internal/prices` must stay a **leaf**: no import of `engine`,
  `audit`, or `ledger`. Quotes are **never** persisted to `audit` and
  **never** feed a tax computation.

---

## 7. Boundaries an agent must respect

From SPEC §6 — the ones most likely to bite:

**Never**
- Commit real CSVs, real account data, or real financial figures.
  Fixtures are fabricated, reproducing a scenario's *structure* (date
  deltas, rate boundaries, matching edges), never real amounts.
- Send real account data (quantities, cost basis, balances,
  transactions, account IDs, the ledger) to any external service,
  including LLM APIs. The price client's single outbound datum is the
  ticker symbol.
- Let an LLM/chat layer compute a tax number. Explain existing figures
  via their audit record only.
- Build any path that submits to Revenue / ROS.
- Silently guess a holding's classification, or auto-populate a ticker
  mapping.

**Ask first**
- Implementing **s.581** matching or **deemed-disposal credit/refund**
  liability — surface the Revenue TDM citation for confirmation before
  writing the expected side of those fixtures. Until then these paths
  fail loud; keep them that way.
- Any new `go.mod` dependency, especially anything that can make
  network calls (a CDN `<script>` in the UI is not a `go.mod` dep; a
  provider SDK would be).
- A non-backward-compatible DB schema change against ingested history.
- `report --format pdf`, a deemed-disposal deadline timeline, or a
  historical portfolio-value time series — all deferred, not just
  unimplemented.
- A second credentialed price provider, or widening what Finnhub
  receives.
- Committing or pushing to git (standing instruction).

**Always**
- Keep rates in `internal/taxrules` as effective-dated data.
- Add or extend a golden fixture in the same change that touches
  `taxrules` or `engine` logic.
- Produce an `audit` record for every computed liability figure (live
  dashboard summary excepted).
- Run `make ci` before considering a change complete.
- Keep `.gitignore` covering real-data paths (`/data`, `*.local.*`,
  `*.db`, `/bin`).

---

## 8. Conventions

- **TDD with golden files.** No `taxrules`/`engine` logic before a
  failing test exists. Fixtures live in `testdata/golden/`, one per tax
  scenario (see SPEC §5 for the built list and the two still blocked).
- **The maths spec** (SPEC §14). `docs/maths.md` describes every engine
  calculation in plain English with its Irish tax-law citation;
  `features/*.feature` (godog) are executable worked examples whose
  `Then`-step figures are engine-produced and pinned by
  `make regen-features`. Any `engine`/`taxrules` change: run
  `make regen-features` and commit the result — `make ci` fails on
  stale figures, the same discipline as extending a golden fixture.
  Never hand-write a tax figure into `docs/maths.md`.
- Table-driven `_test.go` colocated with code. Web tests are
  `httptest`-based, asserting status/redirect/validation/rendered HTML
  — not pixels. `internal/prices` tests run against an `httptest.Server`
  with canned responses — no real network in `make test`.
- Decimal money only — `github.com/shopspring/decimal`, never
  `float64`. Display as `€`, two decimals.
- Per-platform ingest packages; shared detection in
  `ingest/route.go`. Each export format is its own
  independently-changing contract.
- Run `make fuzz` before any change to `internal/engine/`.
- ADRs in `docs/adr/` for cross-cutting decisions.

---

## 9. Current state (see `tasks/todo.md` for the live list)

Built: ledger + fingerprint dedup; all four ingest parsers; manual
classification; effective-dated `taxrules` incl. the 41%→38% exit-tax
transition and (UNVERIFIED) historical CGT/DIRT/exit-tax schedules for
backfilled years; FIFO CGT / exit-tax / DIRT with EUR restatement,
whole-euro rounding, and audit records; **year-level CGT aggregation**
(`AggregateCGTYear`: one exemption/year, cross-holding loss netting,
loss carry-forward) + per-year exit-tax/DIRT + `report`;
deemed-disposal **date** clock; dashboard with charts, hand-entered
interest entry (user-named accounts), hand-entered RSU vests
(`POST /rsu`), a tax-year selector, and a nav "Blur €" privacy
toggle (blurs every money figure, strips chart value-axes/tooltips,
`localStorage`-persisted, reloads on toggle);
`/portfolio` with live prices, per-holding + total P&L %, ticker
remap, and a durable price cache.

Blocked (fail loud, do not implement without a cited source): s.581
four-week loss restriction (task 4.8); deemed-disposal liability and
later actual-disposal credit/refund (task 4.9).
