# CLAUDE.md

This project keeps its agent guidance in **[AGENTS.md](AGENTS.md)** —
read that. It covers what `taxman` computes, why the ETF
`CGT_ASSET` vs `EXIT_TAX_FUND` classification is the core problem, how a
liability figure is derived (FIFO lots, EUR restatement, effective-dated
rates, per-year split, audit trail), the repo map, the commands, and the
boundaries.

Non-negotiables, repeated here so they are not missed:

- **No LLM tax arithmetic.** You may explain a figure already computed
  by `internal/engine` by walking its `internal/audit` record. You must
  never produce a tax number yourself. The plain-English maths reference
  is [`docs/maths.md`](docs/maths.md) (every rule with its Irish
  citation); its worked examples are the engine-pinned scenarios in
  [`features/`](features/) — after any `engine`/`taxrules` change run
  `make regen-features` (CI enforces it). See SPEC §14.
- **No real financial data leaves the machine or enters git** — not to
  any API, not as a fixture, not in a commit. Fixtures are fabricated
  and reproduce a scenario's structure only.
- **Never guess a holding's tax classification** or a ticker symbol —
  both are user-entered; an unset classification blocks computation by
  design.
- **Blocked tax rules stay blocked.** s.581 (four-week loss
  restriction) and deemed-disposal credit/refund liability fail loudly
  on purpose — do not implement them without a cited Revenue TDM
  source (SPEC §6).
- Rates live in `internal/taxrules` as effective-dated data, never as
  constants in `engine`. Add or extend a golden fixture in any change
  that touches `taxrules` or `engine`.
- Run `make ci` before calling a change done. Ask before committing or
  pushing.

[`SPEC.md`](SPEC.md) is the authoritative contract; AGENTS.md is the
orientation layer; this file is a pointer.
