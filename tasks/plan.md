# taxman — Implementation Plan

## Context

`SPEC.md` and `docs/ideas/taxman.md` define what taxman is: a self-hosted, single-binary Go + embedded SQLite app that ingests CSV exports from four investment platforms, builds a lot-level ledger, classifies holdings, and computes Irish DIRT/CGT/exit-tax/deemed-disposal liability with a full audit trail. The repo is currently greenfield — no commits, only `README.md`, `SPEC.md`, `docs/ideas/taxman.md`. This plan breaks the build into small, dependency-ordered, vertically-sliced tasks that fit the `agent-skills:build` RED→GREEN→commit loop, per SPEC §5's TDD discipline.

Two implementation decisions resolved with the user before this plan:
- **Module path:** `github.com/craicoverflow/taxman`
- **SQLite driver:** `modernc.org/sqlite` (pure Go, no cgo — keeps the single-static-binary goal intact)

Tasks are sliced vertically (each delivers a complete thin path, not a horizontal layer) and grouped into phases with checkpoints for human review before continuing, per SPEC §6's "ask first" boundaries — particularly around the two explicitly blocked tax-rule mechanics (TCA s.581 four-week rule, deemed-disposal credit/refund) which require a Revenue TDM citation before any real logic is written for them.

Execution model: run `/agent-skills:build` repeatedly, one task at a time, in the order below. Each task = one RED→GREEN→commit cycle (or a small handful for larger tasks). Task list below doubles as `tasks/todo.md` once execution starts — check items off as they land.

---

## Phase 0 — Foundational plumbing

**0.1 — Repo scaffold, go.mod, Makefile, empty `cmd/taxman`**
- `go.mod` as `github.com/craicoverflow/taxman`
- Full `internal/*` skeleton matching SPEC §4 exactly (`ledger`, `ingest/{degiro,ibkr,etrade,n26}`, `classify`, `taxrules`, `engine`, `audit`, `web`, `db`), each an empty package with `doc.go`
- `cmd/taxman/main.go`: `flag`-based dispatch stubbed for all 7 subcommands (`serve`, `import`, `backfill`, `classify`, `report`, `validate`, `migrate`), each prints "not implemented" for now
- `Makefile`: `build`, `test`, `lint`, `fuzz`, `ci` targets
- `.gitignore`: `/data`, `*.local.*`, `*.db`, `.env`
- **Acceptance:** `make build` produces a `taxman` binary; `go vet ./...` clean; every SPEC §4 path exists; a dummy `foo.local.csv`/`bar.db` is confirmed git-ignored.
- Dependencies: none.

**0.2 — `migrate` command + migration mechanism**
- `internal/db`: embedded-SQL migrations via `embed.FS` + a `schema_migrations` version-tracking table (hand-rolled, no new dependency — flag if this turns out to need one)
- `taxman migrate up|down` wired to it. No domain tables yet — mechanism only.
- **Acceptance:** `migrate up` against a temp SQLite file creates `schema_migrations`, idempotent on re-run; `migrate down` reverses cleanly; round-trip test via `t.TempDir()`.
- Dependencies: 0.1.

**0.3 — CI**
- CI workflow running `make ci` (`build` + `go test -race ./...` + `golangci-lint run`) + `.golangci.yml`.
- **Acceptance:** CI green on the scaffold.
- Dependencies: 0.1, 0.2.

**CHECKPOINT 1** — confirm directory layout fidelity to SPEC §4, CI setup, before any domain logic.

---

## Phase 1 — Domain model core (`internal/ledger`, `internal/taxrules`)

**1.1 — `internal/ledger`: `Transaction` + `Fingerprint` + dedup store**
- `Transaction` struct per SPEC §2 (platform, date, ISIN/ticker, quantity, price, currency, type: buy/sell/dividend/rsu_vest/interest)
- `Fingerprint()`: deterministic hash over identifying fields only
- SQLite-backed store, insert-idempotent-by-fingerprint; migration `0001_create_transactions.sql`
- **Acceptance:** identical logical transaction → identical fingerprint regardless of field order; differing non-identifying fields → same fingerprint; differing identifying fields → different fingerprint; double-insert of identical transaction → exactly one stored row (real temp SQLite DB); round-trip test. This covers SPEC §5's "duplicate ingestion" fixture at the ledger level.
- Dependencies: 0.1, 0.2.

**1.2 — `internal/taxrules`: versioned rate lookup by effective date**
- `RuleVersion`/`Rate` types (CGT rate + €1,270 exemption, exit-tax rate incl. 41%→38% transition on 1 Jan 2026, DIRT rate)
- Pure function `Lookup(kind, date) (Rate, error)` over embedded declarative Go data (not a DB table — rates change rarely, and staying DB-free keeps engine's golden-fixture tests pure)
- **Acceptance:** 2025-12-31 → 41%, 2026-01-01 → 38%; lookup for an undefined date returns an explicit error, never a zero value; no I/O in the lookup path.
- Dependencies: 0.1.

**CHECKPOINT 2** — review `Transaction`/`Fingerprint` fields and taxrules' initial rate dataset before any parser depends on them.

---

## Phase 2 — First vertical ingestion slice: Degiro

Degiro chosen first: flat single-section CSV (one row per buy/sell), no multi-section statement structure (unlike IBKR) and no RSU-specific fields (unlike ETRADE). N26 excluded from "first parser" — its export format is itself an open question per the idea doc.

**2.1 — `internal/ingest/degiro` + `taxman import` end-to-end**
- Parser mapping Degiro CSV rows (Date, Product, ISIN, Quantity, Price, Local value, Value, Exchange rate, fees) to `ledger.Transaction`
- Synthetic fixture CSV under `testdata/fixtures/degiro/`
- `taxman import <file> --platform degiro [--dry-run]` wired end-to-end
- Platform auto-detection stub (identifies Degiro by header shape; designed to extend, not hardcoded to always-match)
- **Acceptance:** buy/sell rows parse correctly; malformed/unrecognized rows produce an explicit parse error, never a silent skip; re-running `import` on the identical file yields zero net new rows; `--dry-run` reports without writing; integration test against a temp SQLite DB.
- Dependencies: 1.1, 0.2.

**CHECKPOINT 3** — review the working `import` path end-to-end; sanity-check the synthetic Degiro fixture's columns against the real export layout (without committing real data) before replicating this shape for the other three platforms.

---

## Phase 3 — `internal/classify`

**3.1 — Holding classification + manual override table**
- `Holding` type (ISIN/platform-ID keyed) with `Classification` (`CGT_ASSET` / `EXIT_TAX_FUND` / `UNCLASSIFIED`)
- DB-backed override/reference table; `Classify(holding) Classification` — checks the table, falls back to `UNCLASSIFIED`. **No heuristic auto-classification in v1**, per the idea doc's "flag rather than guess." CLI wiring (`taxman classify`) deferred to Phase 8 — this task delivers the package API only.
- **Acceptance:** set-then-lookup returns the set classification; no entry → `UNCLASSIFIED`; list-unclassified returns exactly the ledger holdings with no override; overrides persist across restart; migration added.
- Dependencies: 1.1, 2.1.

---

## Phase 4 — `internal/engine`, sliced per SPEC §5 golden fixture

**4.1 — Engine entry point + `UNCLASSIFIED` guard**
- Compute entrypoint checks each holding's classification before touching its lots; blocks *that holding's* computation on `UNCLASSIFIED` with a typed, identifying error — other classified holdings in the same run still compute.
- **Acceptance:** fixture with one unclassified + one classified holding blocks only the former; the latter computes normally.
- Dependencies: 3.1, 1.1.

**4.2 — CGT disposal, simple FIFO**
- `Lot` type per §2; FIFO matching for `CGT_ASSET` holdings; EUR conversion at transaction-date FX; CGT rate + exemption via taxrules by disposal date.
- **Acceptance:** SPEC §5 "CGT disposal, simple FIFO" fixture passes; unit tests for 3+ lot FIFO ordering and partial-lot consumption.
- Dependencies: 4.1, 1.2.

**4.3 — RSU vest → disposal, cost basis = vest-date FMV**
- `rsu_vest` transaction handling creates a lot with cost basis = vest-date FMV, then reuses 4.2's FIFO path.
- **Acceptance:** SPEC §5 RSU fixture passes; explicit test asserting vest-date FMV as basis (not $0, not eventual sale price).
- Dependencies: 4.2.

**4.4 — DIRT on interest**
- `interest` transaction handling: no lot matching, `amount × DIRT rate(date)`. Built against directly-constructed interest transactions, independent of the not-yet-built N26 parser.
- **Acceptance:** SPEC §5 DIRT fixture passes.
- Dependencies: 4.1, 1.2.

**4.5 — Exit-tax fund, actual disposal before any deemed disposal**
- `EXIT_TAX_FUND` disposal path, separate from CGT matching. Flag if exit-tax matching order turns out not to be plain FIFO under Revenue rules — light verification pass even though this isn't one of the two named blockers.
- **Acceptance:** SPEC §5 fixture passes.
- Dependencies: 4.1, 1.2.

**4.6 — Exit-tax rate transition (41%→38%)**
- Confirms taxrules resolves by *event date*, not processing date, across 1 Jan 2026.
- **Acceptance:** fixture passes; regression test: a disposal processed in 2026 but dated Dec 2025 still resolves 41%.
- Dependencies: 4.5, 1.2.

**4.7 — Deemed-disposal 8-year anniversary tracking (date arithmetic only, unblocked)**
- Deliberate split from liability computation: pure date-arithmetic clock for each fund lot's next anniversary (acquisition + 8/16/24 years) — no liability figure, no rule lookup, no audit record.
- **Acceptance:** anniversary math correct across leap years and multiple cycles; explicit test this path never touches `taxrules` or produces a liability number.
- Dependencies: 1.1.

**4.8 — [BLOCKED — ask first] TCA s.581 four-week loss-restriction matching**
- No implementation yet. Per SPEC §6: obtain and present the exact Revenue TDM citation for the four-week same-share reacquisition rule before writing expected output for this fixture or any matching code.
- Buildable now: engine detects a repurchase within 4 weeks of a CGT disposal and raises an explicit `"unverified rule: s.581"` error instead of silently applying plain FIFO.
- Dependencies: 4.2.

**4.9 — [BLOCKED — ask first] Deemed-disposal liability + credit/refund on subsequent actual disposal**
- No implementation yet. Per SPEC §6: obtain and present the Revenue TDM citation for credit/refund mechanics before writing expected output.
- Buildable now: any fund lot past its 4.7 anniversary causes engine to raise an explicit, lot-identifying `"unverified rule: deemed disposal"` error.
- Dependencies: 4.7, 1.2.

**CHECKPOINT 4** — review all six unblocked fixtures' expected outputs (hand-checked locally against the spreadsheet — never committed); confirm per-holding (not whole-run) `UNCLASSIFIED` blocking matches intent; reconfirm 4.8/4.9 stay blocked pending TDM citations.

---

## Phase 5 — `internal/audit`

**5.1 — Audit record for every computed liability figure**
- `AuditRecord` per §2 generated for every figure from Tasks 4.2–4.6, linking to source transaction ID(s), lot ID(s), disposal event, and the exact `taxrules.RuleVersion` used.
- **Acceptance:** each of the 5 unblocked engine fixtures asserts the correct `AuditRecord` exists; invariant test — no liability figure lacks an audit record; records survive DB round-trip.
- Dependencies: 4.2, 4.3, 4.4, 4.5, 4.6.

---

## Phase 6 — Remaining ingestion parsers

**6.1 — `internal/ingest/ibkr`**
- Parser for IBKR's CSV Activity Statement (multi-section — isolate Trades at minimum), CSV only (not Flex Query API — new network-capable dependency needs ask-first per §6).
- **Acceptance:** mirrors 2.1; auto-detection distinguishes IBKR from Degiro; SPEC §5's named "Degiro + IBKR overlapping the same ISIN" integration test dedups correctly.
- Dependencies: 1.1, 2.1.

**6.2 — `internal/ingest/etrade`**
- Parser incl. RSU vest rows (vest date, quantity, FMV → feeds 4.3) and sale rows.
- **Acceptance:** mirrors 2.1; vest rows map to `rsu_vest` with correct FMV; confirms or flags a mismatch with the idea doc's "fully payroll-withheld" assumption.
- Dependencies: 1.1, 2.1.

**6.3 — `internal/ingest/n26`**
- First resolves the open question: clean CSV export vs. manual entry. Builds whichever path applies, feeding `interest` transactions to 4.4.
- **Acceptance:** fingerprint-based dedup holds regardless of path chosen (no double-counted interest).
- Dependencies: 1.1, 2.1, resolution of the N26 export-format question.

**CHECKPOINT 5** — with all four parsers and audit-linked engine paths in place, run a combined dry-run import across all four platforms' synthetic fixtures; confirm no cross-platform fingerprint collisions or classification gaps before building the UI.

---

## Phase 7 — `internal/web` + dashboard

**7.1 — HTTP server + dashboard**
- `internal/web` handlers + embedded assets (`embed.FS`); `taxman serve [--port][--db]`
- Dashboard: merged P&L, running liability by tax type, upcoming deemed-disposal anniversaries (4.7's clock, labeled "anniversary reached, liability computation pending" where 4.9 is still blocked), payment deadlines (pulled from currently-published Revenue dates — confirm-first sub-step, not a hardcoded guess).
- **Acceptance:** `taxman serve` starts; dashboard route returns 200 against a fixture-seeded temp DB; every rendered liability figure links to its audit record; unclassified holdings are visibly flagged, never hidden; `httptest` coverage of main dashboard data assertions.
- Dependencies: Phase 5, Phase 4, Phase 6.

---

## Phase 8 — Remaining CLI wiring

- **8.1** `taxman classify` CLI (`--list-unclassified`, `--set`) — depends on 3.1.
- **8.2** `taxman backfill <dir>` — mixed-platform directory ingest, auto-detected per file, logged distinctly from `import`, idempotent re-run — depends on 6.1, 6.2, 6.3, 2.1.
- **8.3** `taxman report --year --format text|json|pdf` — text/json tested; `pdf` flagged as a candidate to descope if it needs a new dependency (ask-first) — depends on Phase 5, Phase 4.
- **8.4** `taxman validate --fixture <path>` — CLI-invocable golden-fixture diff, exits non-zero on mismatch — depends on Phase 4, Phase 5.

---

## Phase 9 — Dashboard charts & manual interest entry

Per SPEC.md §7 (confirmed via interview-me, 2026-09-05). Vertical slices, each leaving `internal/web` in a working, tested state; no `internal/engine` changes in this phase.

**9.1 — `POST /interest`: manual N26 interest entry**
- Wires the existing-but-unused `internal/ingest/n26.NewInterestCredit` to a new HTTP handler (`date`, `amount` form fields, currency fixed `EUR`) and a form on the dashboard, mirroring `handleClassify`'s pattern (parse form → validate → store → redirect 303 to `/`; 400 on validation failure; 405 on non-POST).
- **Acceptance:** submitting a valid date+amount inserts an `interest` transaction and the dashboard's DIRT figures reflect it after redirect; an invalid date or non-positive amount returns 400 and writes nothing; re-submitting the identical entry does not double-count (fingerprint dedup, same as every other ingestion path).
- **Verify:** `go test ./internal/web/...`; manual check — `make dev`, submit the form, confirm DIRT total updates.
- Dependencies: existing `n26.NewInterestCredit` (6.3), `ledger.Store.Insert` (1.1).
- Files: `internal/web/server.go`, `internal/web/server_test.go`.
- Size: S.

**9.2 — Liability-by-tax-type chart**
- Replace (or supplement) the existing "Running liability by tax type" table with a Chart.js bar/pie chart, fed by the dashboard's existing `dashboardSummary` (CGT/exit-tax/DIRT tax due) marshaled to a `<script type="application/json">` block; Chart.js loaded via a pinned-version `<script>` tag from `cdn.jsdelivr.net`. No new engine computation — purely a template/rendering change.
- **Acceptance:** dashboard response embeds a JSON block whose values match `dashboardSummary`'s fields exactly; page includes the Chart.js `<script src>` tag and a `<canvas>` the inline JS initializes from that block; existing table-based assertions in `server_test.go` still pass (or are updated to assert against the new markup, if the table is replaced rather than kept alongside).
- **Verify:** `go test ./internal/web/...`; manual check — load `/` in a browser, confirm the chart renders with correct values for a fixture-seeded DB.
- Dependencies: none beyond 7.1 (existing `dashboardSummary`).
- Files: `internal/web/server.go`, `internal/web/server_test.go`.
- Size: S.

**9.3 — P&L-over-time chart**
- `handleDashboard` currently discards `CGTResult.Disposals` / `ExitTaxResult.Disposals` after summing into `dashboardSummary`; this task collects every `Disposal` (all holdings, both kinds) into one slice, sorts by `Date`, and computes a cumulative-gain series, marshaled the same way as 9.2 into the dashboard's JSON block for a second Chart.js line chart.
- **Acceptance:** for a fixture ledger with disposals across two holdings on different dates, the emitted series is sorted by date with correctly accumulating gain values; a holding with a `ComputeError` (unclassified, unresolvable FX) is excluded from the series without breaking the rest of the page, consistent with how `handleDashboard` already isolates per-holding compute errors.
- **Verify:** `go test ./internal/web/...`; manual check — dashboard line chart trends match hand-summed disposal gains for the fixture DB.
- Dependencies: 9.2 (chart JS/CDN wiring already in place).
- Files: `internal/web/server.go`, `internal/web/server_test.go`.
- Size: M (touches `handleDashboard`'s existing per-holding loop, not just additive markup).

**CHECKPOINT 6** — load the dashboard against a fixture-seeded DB covering CGT, exit-tax, DIRT, and an unclassified holding; confirm both charts render correct values, the interest-entry form round-trips into the DIRT figure, and `make ci` passes before considering §7 done.

---

## Final checkpoint — cross-validation (local only, never committed)

**10.1 — Cross-validate against spreadsheet / Tax-Wizard**
- Delivers only a `docs/adr/` entry mapping which golden fixture stands in for which real-world scenario. No real data or real figures enter the repo at any point.
- **Acceptance:** user runs `backfill`/`validate` against real, gitignored, local-only exports; dashboard/report numbers reconcile against the spreadsheet and (where feasible) an independent Tax-Wizard run; discrepancies become new golden fixtures (if a real bug) or documented known differences; explicit `git status`/diff review before any commit to confirm nothing real leaked in.
- Dependencies: all prior phases. This is the actual point of the tool — human-in-the-loop by design.

---

## Verification approach throughout

- Every task follows RED → GREEN → `make ci` → commit, per `agent-skills:build`.
- `make fuzz` run before any change to `internal/engine` (not required in normal CI).
- No task in Phase 4 marked complete without its SPEC §5 golden fixture passing.
- Tasks 4.8 and 4.9 are not "TODO later" — they are explicitly blocked pending Revenue TDM citations; `/agent-skills:build auto` must stop and ask rather than invent an implementation for either.

---

## Phase 11 — Portfolio page (live market value)

Per SPEC.md §9 (confirmed via interview-me, 2026-09-05). A **separate** page `GET /portfolio` with two Chart.js visuals over currently-held, ticker-mapped positions, all in euro: an allocation pie (live market value) and cost-vs-value bars (cost basis vs market value, gap = unrealised P&L). Vertically sliced, risk-first: the one `internal/engine` change (task 11.1) lands first and alone. No change to `internal/taxrules` or `internal/audit`; quotes are never persisted and never feed tax computation.

Two implementation decisions resolved with the user before this phase:
- **Cost basis of open positions** comes from a single new read-only engine function `engine.OpenPositions` (SPEC §9) — not re-derived in `internal/web`, not an average-cost approximation.
- **Price provider is keyless**: Yahoo public quote endpoint primary, stooq fallback, standard library only, no `go.mod` addition, no credential storage.

Suggested PR split: **11.1–11.3** = "portfolio plumbing" (no network, no user-visible charts), **11.4–11.6** = "portfolio page" (prices + charts + nav).

---

**11.1 — `engine.OpenPositions`: still-held quantity + FIFO cost basis**
- New exported function in `internal/engine` (`openposition.go`, or alongside `matchDisposalsFIFO` in `cgt.go` if the shared lot loop factors cleanly): `func OpenPositions(txs []ledger.Transaction) (OpenPosition, error)` with `type OpenPosition struct { Quantity, CostBasis decimal.Decimal }`. Runs the same FIFO lot-building and consumption as `matchDisposalsFIFO` but returns the surviving lots' summed `Remaining` quantity and their summed remaining cost basis, instead of the disposals. Non-EUR lots restated in euro at transaction-date ECB rate via the existing `toEURPrices` path, same as `ComputeCGT`. An oversell returns the same explicit error `matchFIFO` already produces. No rate/exemption lookup, no tax-rule logic.
- **Acceptance:**
  - [ ] All-open (buys/vests, no sells) → `Quantity` and `CostBasis` equal the totals acquired.
  - [ ] Partial FIFO consumption (buys at different prices, one later sell) → surviving quantity correct and `CostBasis` = sum of the lots FIFO leaves untouched (oldest consumed first), matching what `ComputeCGT` would leave.
  - [ ] Fully disposed → zero `Quantity`, zero `CostBasis`; oversell → explicit error, no panic.
  - [ ] A non-EUR buy → `CostBasis` restated in euro.
- **Verify:**
  - [ ] `go test ./internal/engine/...` (new table-driven `TestOpenPositions`); existing golden fixtures + `ComputeCGT` tests still green.
  - [ ] `make fuzz` on `internal/engine` before commit (SPEC §5 boundary).
  - [ ] `make ci`.
- **Dependencies:** None.
- **Files likely touched:** `internal/engine/openposition.go` (+ maybe small refactor in `internal/engine/cgt.go`), `internal/engine/openposition_test.go`.
- **Estimated scope:** S.

---

**11.2 — `instrument_tickers` migration + store**
- Migration pair `internal/db/migrations/0005_create_instrument_tickers.{up,down}.sql`. Table: `instrument TEXT PRIMARY KEY`, `symbol TEXT NOT NULL`, `created_at TEXT NOT NULL`. Additive only — no change to `transactions` or any existing table, backward-compatible with all ingested history.
- A small store in the style of `internal/classify.Store`: `Get(instrument) (string, bool, error)`, `All() (map[string]string, error)`, `Upsert(instrument, symbol string) error`. Lives in `internal/web` (inline, like other dashboard helpers) or a new `internal/tickers` package — pick whichever matches how `classify.Store` is wired into `Server`; do not add a package if an unexported helper suffices.
- **Acceptance:**
  - [ ] `taxman migrate up` then `down` then `up` is clean on a scratch DB; `migrate_test.go` picks the pair up via the embed FS walk with no code change.
  - [ ] `Upsert` inserts a new row and overwrites an existing instrument's symbol (last write wins); `Get` / `All` round-trip.
- **Verify:**
  - [ ] `go test ./internal/db/... ./internal/web/...` (or the store's package); `make ci`.
- **Dependencies:** None.
- **Files likely touched:** `internal/db/migrations/0005_create_instrument_tickers.up.sql`, `.down.sql`, one store file + its `_test.go`.
- **Estimated scope:** S.

---

**11.3 — `/portfolio` skeleton + ticker mapping form (no prices yet)**
- `handlePortfolio` (`GET /portfolio`) in `internal/web/server.go`: load transactions, group by instrument (reuse the dashboard's derivation), call `engine.OpenPositions` per instrument, keep those with `Quantity > 0`. Render an inline `html/template` (`portfolioTemplate`, same style as `dashboardTemplate`) listing each held position with quantity and euro cost basis as plain text — **no charts, no live prices in this task**. Read `?year=` and ignore it (assert in test).
- For each held instrument with no `instrument_tickers` row: render a `POST /portfolio/ticker` form (`instrument` hidden field + `symbol` text input). `handlePortfolioTicker`: method guard → `ParseForm` → non-empty `instrument` and `symbol` → `Upsert` → `303` to `/portfolio`; `400` on empty, `405` on non-POST. Mirrors `handleClassify`.
- Register both routes in `Server.Handler()`.
- **Acceptance:**
  - [ ] `GET /portfolio` returns 200 against a fixture-seeded DB; shows held positions (qty > 0) with quantity + euro cost basis; fully-disposed instruments absent.
  - [ ] Unmapped held instruments show a mapping form; already-mapped ones show their symbol instead.
  - [ ] `POST /portfolio/ticker` with valid fields → 303, row present via the store; empty `symbol` → 400, nothing written; `GET` on it → 405.
  - [ ] `GET /portfolio?year=2024` renders identically to `GET /portfolio`.
- **Verify:**
  - [ ] `go test ./internal/web/...` (new `handlePortfolio` / `handlePortfolioTicker` cases, table-driven, following `handleClassify` test shape).
  - [ ] Manual: `make dev`, open `/portfolio`, submit a mapping, confirm it persists across reload.
  - [ ] `make ci`.
- **Dependencies:** 11.1, 11.2.
- **Files likely touched:** `internal/web/server.go`, `internal/web/server_test.go`.
- **Estimated scope:** M.

---

### CHECKPOINT 7 — portfolio plumbing
- [ ] `make ci` green; existing dashboard (`/`) output byte-for-byte unchanged (diff `server.go` — additions only).
- [ ] `engine.OpenPositions` FIFO cost basis reconciled by hand against one fixture holding that also appears in a CGT golden fixture.
- [ ] `/portfolio` lists the right held positions; ticker mapping round-trips into SQLite.
- [ ] Review with human before building the price client.

---

**11.4 — `internal/prices`: keyless quote client with fallback + cache**
- New leaf package `internal/prices`. `type Quoter interface { Quote(ctx context.Context, symbol string) (Quote, error) }`; `type Quote struct { Symbol string; Price decimal.Decimal; Currency string; AsOf time.Time; Stale bool }`.
- `httpQuoter` implementing it: primary = Yahoo `query1.finance.yahoo.com/v7/finance/quote?symbols=…` (JSON: `regularMarketPrice`, `currency`); fallback = stooq `stooq.com/q/l/?s=<sym>&f=sd2t2ohlcv&e=csv` (CSV) on primary error/non-200/parse failure. In-process cache `map[string]entry` (price + fetchedAt) guarded by a mutex, TTL ≈ 15 min — a hit inside TTL issues no request. On **every** provider failing: return the last cached `Quote` with `Stale=true` and `AsOf` = its fetch time; with no cached value, return an error the caller renders as "unpriced". `context` deadline respected. `net/http` + `encoding/json` + `encoding/csv` only — **no `go.mod` addition**.
- Import hygiene: `internal/prices` must not import `engine`, `audit`, `ledger`, or `web`.
- **Acceptance:**
  - [ ] Primary success → `Quote` with price + currency parsed.
  - [ ] Primary 5xx / malformed body → fallback used, `Quote` returned.
  - [ ] Both providers fail + a cached value exists → `Stale=true` `Quote` with the old `AsOf`, no error.
  - [ ] Both fail + no cache → error (caller's "unpriced" path).
  - [ ] Second call within TTL → zero HTTP requests (assert via `httptest` hit count).
- **Verify:**
  - [ ] `go test ./internal/prices/...` against an `httptest.Server` with canned primary/fallback bodies — no real network in `go test`. Parser cases pinned against a captured real response sample so an upstream shape change fails loudly.
  - [ ] `make lint` (import graph clean); `make ci`.
- **Dependencies:** None (slotted here so 11.5 can consume it).
- **Files likely touched:** `internal/prices/prices.go`, `internal/prices/http.go`, `internal/prices/prices_test.go`, `internal/prices/doc.go`.
- **Estimated scope:** M.

---

**11.5 — Wire live prices + EUR into `/portfolio`; add pie + bars**
- `Server` gains a `quoter prices.Quoter` field; `NewServer` constructs the real `httpQuoter`; every `server_test.go` case injects a fake `Quoter` (no network in tests).
- `handlePortfolio`: for each held, mapped instrument, call `quoter.Quote`, convert native → EUR via `fx.Rate(quote.Currency, time.Now())`, compute market value = EUR price × held quantity. Assemble: (a) pie data — `{label, valueEUR}` per instrument + total; (b) bar data — `{label, costBasisEUR, marketValueEUR}` per instrument, sorted by market value desc. Marshal to a JSON block the inline JS reads; add the Chart.js CDN `<script>` (pinned version, same as dashboard) + two `<canvas>` elements.
- Degradation, all surfaced not hidden: a `quote.Stale` position renders with a marker and the page shows a "prices as of HH:MM" stamp (oldest `AsOf` across shown positions); a `fx.Rate` error OR a no-cache quote error → that instrument moves to a visible "couldn't value" list and is excluded from pie, bars, and total; if any conversion failed, one page-level banner. Never a 500, never a blank page.
- **Acceptance:**
  - [ ] Mapped holdings (fake `Quoter`) → pie + bar JSON present, values EUR-converted with a stubbed rate, bars sorted by market value desc.
  - [ ] Unmapped held instrument → in the mapping-form list, absent from pie/bars/total.
  - [ ] Stale quote → staleness marker + "prices as of" stamp in rendered HTML.
  - [ ] `fx.Rate` failure for one instrument → it appears in "couldn't value", the rest of the page still renders, banner shown.
  - [ ] No `server_test.go` case performs a real network call.
- **Verify:**
  - [ ] `go test ./internal/web/...` (fake `Quoter`, stubbed rate); `make ci`.
  - [ ] Manual: `make dev`, map a real holding, confirm pie + bars render with plausible live numbers.
- **Dependencies:** 11.3, 11.4.
- **Files likely touched:** `internal/web/server.go`, `internal/web/server_test.go`.
- **Estimated scope:** M.

---

**11.6 — Dashboard nav link + online/offline smoke + docs**
- Add a link to `/portfolio` from the dashboard (`/`) and back, in the existing nav style.
- `README.md`: a short "Portfolio page" note — what it shows, that prices are best-effort from public endpoints with no API key, that it degrades to last-known prices offline, and that mappings are entered on the page.
- Manual smoke: (1) online — map one holding, load `/portfolio`, confirm pie + bars; (2) offline (kill network / airplane mode) — reload, confirm last-known prices with a staleness stamp and **no blank page or 500**.
- **Acceptance:**
  - [ ] Dashboard ↔ portfolio nav links present and working.
  - [ ] README section added.
  - [ ] Both smoke paths pass and are noted in the PR description.
- **Verify:**
  - [ ] `make ci` green.
  - [ ] `git diff` on `internal/web/server.go` for the whole phase shows additions + the `Server` struct/`NewServer` change only — no change to `handleDashboard`'s logic.
- **Dependencies:** 11.5.
- **Files likely touched:** `internal/web/server.go`, `README.md`.
- **Estimated scope:** S.

---

### CHECKPOINT 8 — portfolio page complete
- [ ] `make ci` green; `make fuzz` on `internal/engine` clean (run once for 11.1).
- [ ] Against a fixture-seeded DB + a fake quoter: pie values sum to the headline total; each bar's (value − cost) equals the hand-computed unrealised P&L; unmapped and unpriced holdings are visible and excluded from totals.
- [ ] Offline smoke: `/portfolio` serves stale prices with a timestamp, never a blank page.
- [ ] SPEC §9 acceptance criteria all met; existing dashboard behaviour unchanged.
- [ ] Review with human.

---

## Risks and mitigations — Phase 11

| Risk | Impact | Mitigation |
|---|---|---|
| `internal/fx/eurofxref-hist.csv` is stale beyond `staleGrace` → `fx.Rate("USD", today)` errors → USD/GBP positions can't be valued | Med | Handler treats an `fx.Rate` error exactly like an unpriced holding: exclude from totals, list it under "couldn't value" with the reason, one page banner. Never a 500. Documented in 11.5 acceptance. |
| Yahoo quote endpoint is unofficial — response shape changes, rate-limits, or geo-blocks | Med | stooq fallback in the design; total failure serves stale from cache; `prices_test` pins the parser against a captured sample so a shape change breaks a test, not production silently. |
| `OpenPositions` refactor regresses `ComputeCGT` FIFO matching | High | Touch the shared lot loop only if the diff is genuinely small; otherwise a parallel function. Existing golden fixtures + `ComputeCGT` tests must stay green; `make fuzz` before commit (task 11.1). |
| A real network call leaks into `go test` via `NewServer`'s real `httpQuoter` | Med | `Quoter` is an injected interface; every web test uses the fake. CI has no network dependency. Checked in 11.5 acceptance. |
| Wrong ticker symbol silently misprices the portfolio | Low | No auto-inference (SPEC §6); the page shows the mapped symbol next to each position; re-submitting the form overwrites. |
| Scope creep into a historical value time-series | Low | Explicitly out of scope in SPEC §9; no schema for it in 11.2; flagged as "ask first" in SPEC §6. |

## Open questions — Phase 11

- None blocking. Provider precedence (Yahoo → stooq), cache TTL (~15 min), and the keyless constraint are all resolved in SPEC §9.
