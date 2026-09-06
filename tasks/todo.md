# taxman — task checklist

Full detail (acceptance criteria, dependencies, checkpoints) in `tasks/plan.md`.

## Phase 0 — Foundational plumbing
- [x] 0.1 Repo scaffold, go.mod, Makefile, empty cmd/taxman
- [x] 0.2 migrate command + migration mechanism
- [x] 0.3 CI
- [x] **CHECKPOINT 1** — review before Phase 1

## Phase 1 — Domain model core
- [x] 1.1 internal/ledger: Transaction + Fingerprint + dedup store
- [x] 1.2 internal/taxrules: versioned rate lookup by effective date
- [x] **CHECKPOINT 2** — review before Phase 2

## Phase 2 — First vertical ingestion slice: Degiro
- [x] 2.1 internal/ingest/degiro + `taxman import` end-to-end
- [x] **CHECKPOINT 3** — review before replicating to other platforms

## Phase 3 — internal/classify
- [x] 3.1 Holding classification + manual override table

## Phase 4 — internal/engine
- [x] 4.1 Engine entry point + UNCLASSIFIED guard
- [x] 4.2 CGT disposal, simple FIFO
- [x] 4.3 RSU vest → disposal, cost basis = vest-date FMV
- [x] 4.4 DIRT on interest
- [x] 4.5 Exit-tax fund, actual disposal before deemed disposal
- [x] 4.6 Exit-tax rate transition (41%→38%)
- [x] 4.7 Deemed-disposal 8-year anniversary tracking (date arithmetic only)
- [ ] 4.8 [BLOCKED — ask first] TCA s.581 four-week loss-restriction matching
- [ ] 4.9 [BLOCKED — ask first] Deemed-disposal liability + credit/refund
- [ ] **CHECKPOINT 4** — review before Phase 5

## Phase 5 — internal/audit
- [x] 5.1 Audit record for every computed liability figure

## Phase 6 — Remaining ingestion parsers
- [x] 6.1 internal/ingest/ibkr
- [x] 6.2 internal/ingest/etrade
- [x] 6.3 internal/ingest/n26
- [x] **CHECKPOINT 5** — review before web layer

## Phase 7 — internal/web + dashboard
- [x] 7.1 HTTP server + dashboard (payment-deadline widget deferred, see commit)

## Phase 8 — Remaining CLI wiring
- [x] 8.1 taxman classify CLI
- [x] 8.2 taxman backfill
- [x] 8.3 taxman report (text/json only, no audit-record writing yet, see commit)
- [x] 8.4 taxman validate

## Phase 9 — Dashboard charts & manual interest entry
- [x] 9.1 `POST /interest`: manual N26 interest entry
- [x] 9.2 Liability-by-tax-type chart
- [x] 9.3 P&L-over-time chart
- [ ] **CHECKPOINT 6** — review before Final checkpoint

## Final checkpoint
- [ ] 10.1 Cross-validate against spreadsheet / Tax-Wizard (local only, never committed)

## Phase 11 — Portfolio page (live market value)
SPEC.md §9. PR split: 11.1–11.3 plumbing, 11.4–11.6 page.
- [x] 11.1 engine.OpenPositions — still-held qty + FIFO cost basis (pure engine, unit test, run `make fuzz`)
- [x] 11.2 instrument_tickers migration (0005) + store
- [x] 11.3 GET /portfolio skeleton + POST /portfolio/ticker mapping form (no prices yet)
- [x] **CHECKPOINT 7** — portfolio plumbing; reviewed, approved
- [x] 11.4 internal/prices — keyless Yahoo→stooq quote client, 15-min cache, stale-serve
- [x] 11.5 Wire live prices + EUR into /portfolio; allocation pie + cost-vs-value bars
- [x] 11.6 Dashboard nav link + online/offline smoke + README note
- [ ] **CHECKPOINT 8** — portfolio page complete; review
