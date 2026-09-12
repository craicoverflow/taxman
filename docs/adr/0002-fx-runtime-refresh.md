# ADR 0002 — Live refresh of the ECB reference-rate data

Status: accepted
Date: 2026-09-12

## Context

ADR 0001 embedded `eurofxref-hist.csv` in the binary and deliberately
ruled out a runtime fetch, so that refreshing it was a manual step: it
kept the "ask first before anything network-capable" boundary intact,
and noted that a refresh mechanism could be added later "if the manual
step proves annoying — that change would introduce the first network
call and needs its own sign-off."

That sign-off arrived after the manual step actually did prove
annoying: a same-day disposal (2026-09-11) failed against a file that
still ended a week earlier, and the user asked, explicitly and twice,
for the refresh to happen automatically ("the system should auto-refresh
this information"; "i want a dynamic refresh now for this") rather than
requiring a re-download-and-replace each time the file trails behind.

## Decision

`fx.Rate()` now makes one best-effort live fetch of the ECB's history
zip when a lookup date falls beyond what the active dataset covers,
before returning the stale-data error (`internal/fx/refresh.go`). The
downloaded CSV both replaces the in-memory dataset for the rest of the
process and is persisted under `os.UserCacheDir()/taxman/` so a later
process picks it up without another round trip.

This is scoped narrowly, on purpose:

- **Only on a genuine miss.** A date the embedded file already covers
  never triggers a network call — this is a fallback for staleness, not
  a background poller or a fetch-on-every-run.
- **Cooldown, not silence.** Repeated stale lookups in one process (a
  batch of disposals on the same trailing date) share one fetch attempt
  per minute; each attempt (success or failure) is logged to stderr, so
  a live refresh is visible, never a silent influence on a tax figure.
- **`TAXMAN_FX_OFFLINE=1` restores ADR 0001's original behaviour**
  exactly — no fetch, same stale-data error naming the manual-refresh
  steps — for anyone who wants strictly offline, reproducible runs (CI,
  air-gapped machines, or just not trusting the network that day).
- **A failed fetch is not fatal.** Offline, ECB unreachable, or a
  malformed download all fall back to the pre-existing stale-data
  error; nothing about the "hard error over a guessed rate" behaviour
  from ADR 0001 changes.
- **Newer data only.** A fetch that returns data no newer than what's
  already active (e.g. the ECB hasn't published yet) is discarded
  rather than swapped in.

This still respects SPEC.md's "no real financial data leaves the
machine or enters git": the ECB history is public reference data, not
account data — the same reasoning ADR 0001 used to justify committing
it in the first place. No transaction, holding, or account data is
sent anywhere; the outbound request is a plain GET with no parameters.

## Consequences

- A stale-data error should now be rare in normal use — it surfaces
  only when the process is offline (or `TAXMAN_FX_OFFLINE=1`) *and* the
  embedded file doesn't cover the date, which the shipped file already
  covers for anything not extremely recent.
- Two runs of the same command, minutes apart, can now legitimately
  produce different `fx.Source()` audit strings (`embedded` vs. `live
  refresh` vs. `cached live refresh`) even though the historical rate
  each returns for a given already-published date does not change —
  ECB reference rates are never revised once published. Determinism of
  the *tax figure* is preserved; only the provenance label can differ.
- Tests must never perform the real fetch: `internal/fx/refresh_test.go`
  injects a fake `fetch` and a temp cache directory rather than hitting
  `https://www.ecb.europa.eu`. Any future test that exercises a
  stale-date path must do the same, or explicitly set
  `TAXMAN_FX_OFFLINE=1`, so the suite stays hermetic.
- Not yet done: a dedicated `taxman fx refresh` subcommand for an
  explicit, ahead-of-time refresh (useful for scripting a periodic
  update outside of a tax run). The live-refresh-on-miss path above
  covers the reported problem without it; add the subcommand if a
  workflow needs an explicit refresh with no lookup attached.
