# taxman — Irish Investment Tax Ledger

## Problem Statement
How might an individual investor replace a manual spreadsheet with a trustworthy, self-hosted, always-on ledger that turns raw exports from Degiro, IBKR, ETRADE, and N26 into running DIRT, CGT, and exit-tax/deemed-disposal figures — with full audit trail — without becoming a part-time tax analyst every January?

## Recommended Direction
Custom-built, self-hosted, single-binary web app (e.g. a Go binary embedding SQLite, serving a localhost UI) built around a continuous, append-only transaction ledger rather than a once-a-year batch report. You upload CSV exports whenever you have them, not just at year-end; the app merges them idempotently into one lot-level ledger spanning all four platforms and all history back to first purchase — "just this year's data" is meaningless once 8-year deemed disposal is in play. A separate, explicit classification step tags each holding as a CGT asset (individual stock, RSU shares) or an exit-tax fund (UCITS/offshore ETF) before any tax math runs, and flags anything it doesn't recognize rather than guessing. Tax rates and rules are stored as versioned data keyed by effective date, not hardcoded, because the regime is already mid-change (41%→38% exit tax from Jan 2026, deemed-disposal abolition under active government consideration). The dashboard shows merged P&L, running liability by tax type, and upcoming deemed-disposal anniversaries per lot — every number traceable back to the source transactions and rule version that produced it. No LLM performs arithmetic; an optional chat layer only explains already-computed numbers by tracing the audit trail.

This differs from a batch calculator (like Tax-Wizard, the closest existing commercial tool — covers DEGIRO/IBKR CGT+exit-tax but not RSU vesting or DIRT) in three ways that matter here: RSU vesting and DIRT are first-class alongside CGT/exit-tax; full financial history never leaves your own machine; and it's built for "check anytime," not "assemble everything once a year."

## Key Assumptions to Validate
- [ ] N26 interest is obtainable in a parseable form (CSV/PDF/API) — check N26's actual export options; may degrade to manual entry, not full automation.
- [ ] Each fund held can be correctly classified (Irish/EU-domiciled fund → exit tax; individual equity → CGT) — a research task before a coding task. Build a small reference table for the holdings in scope, verified against Revenue guidance or each fund's KIID/tax-status documentation.
- [ ] RSU vesting is fully payroll-withheld (income tax/USC/PRSI at vest) so ETRADE only needs CGT tracking from vest date forward — confirm this matches the relevant equity plan; some equity comp requires separate RTSO1 filing.
- [ ] Irish CGT share matching is not pure FIFO — TCA s.581's 4-week same-share reacquisition rule affects matching order and restricts losses on shares rebought within 4 weeks. Verify exact mechanics against Revenue's Tax and Duty Manual before implementing the matcher; don't build this rule from memory.
- [ ] Deemed disposal's credit/refund mechanics (tax paid at deemed disposal vs. tax due on eventual actual disposal) are unverified — needs a Revenue TDM check, not an assumption either way.
- [ ] CGT payment deadlines (Jan–Nov disposals vs. December disposals) and DIRT/Form 11 filing deadlines should be pulled from Revenue's current published dates before hardcoding into any "upcoming deadline" feature.

## MVP Scope
**In:**
- CSV ingestion for Degiro, IBKR, ETRADE (manual entry fallback for N26 if no clean export exists)
- Idempotent merge/dedup across repeated or overlapping uploads (unique transaction fingerprinting)
- One-time full historical backfill flow, separate from ongoing incremental uploads
- Explicit per-holding classification step (CGT asset vs. exit-tax fund), with a manual-override table maintained for actual holdings
- FIFO lot matching for CGT assets (with s.581 four-week rule handled per verified Revenue guidance), 8-year deemed-disposal tracking for fund lots
- Tax rates/rules stored as versioned data by effective date
- Dashboard: merged P&L, running liability by tax type (CGT/exit tax/DIRT), upcoming deemed-disposal anniversaries, relevant payment deadlines
- Full audit trail: every figure traceable to source transactions + rule version applied
- Validation: hand-check outputs against the existing spreadsheet and a parallel run through Tax-Wizard as an independent cross-check

**Out (see Not Doing):** dividend/distribution income tracking, multi-user support, broker API integrations beyond possibly IBKR Flex Query later, ROS e-filing, LLM-performed tax arithmetic.

## Not Doing (and Why)
- **Dividend/distribution income tracking** — real filing obligation (foreign withholding credits, distributing-ETF income) but a separate computation from disposal-based CGT/exit-tax; scoping it out of v1 keeps the ledger's first version focused on trades → disposal tax. UI should state plainly that dividend income isn't covered, so it doesn't read as handled.
- **Adopting Tax-Wizard or another existing calculator instead of building** — doesn't cover RSU vesting or DIRT, and keeping full trade history on a third-party service is a real cost for a personal finance tool holding years of data. Use it as a cross-check, not a replacement.
- **Multi-user / shareable SaaS version** — the moment this computes someone else's tax liability, it's a different liability class (giving tax advice to others). Worth revisiting later; not a v1 concern.
- **Broker API integrations beyond maybe IBKR Flex Query** — Degiro has no official API, ETRADE and N26 exports are infrequent enough that CSV drop-in is simpler than building and maintaining API integrations for marginal benefit.
- **ROS e-filing integration** — high effort, and filing-ready ≠ filing-automated; a human review step before anything goes to Revenue is still wanted.
- **LLM performing tax arithmetic** — any explanatory chat layer traces already-computed, deterministically-derived numbers; it never computes them itself.

## Open Questions
- Can N26 interest data be exported cleanly, or does it need manual entry?
- What does full historical backfill look like for an older Degiro account — how far back does usable export data go?
- Exact Revenue TDM mechanics for: s.581 four-week CGT matching rule, and deemed-disposal tax credit/refund on eventual actual disposal.
- Confirm current CGT payment deadlines and Form 11 filing deadline for the relevant tax year before hardcoding into the "upcoming deadlines" feature.
- Go (single binary) vs. another stack — worth a quick spike before committing, given "self-hosted web app + embedded DB, zero ops" is the deployment model implied.
