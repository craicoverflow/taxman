# cgt_year_single_exemption

Golden fixture for finding #1: the €1,270 CGT annual exemption is
**personal** — one per tax year, not one per holding.

Synthetic scenario, both holdings CGT_ASSET, both fully disposed in
2024:

| instrument | buy | sell | gain |
|---|---|---|---|
| AAPL | 10 @ €100 (2024-01-01) | 10 @ €300 (2024-06-01) | €2,000 |
| MSFT | 10 @ €100 (2024-01-01) | 10 @ €400 (2024-07-01) | €3,000 |

Aggregate 2024 CGT (`engine.AggregateCGTYear`, via
`taxman validate --kind cgt_year --year 2024`):

- net chargeable gain €5,000 (gains netted across both holdings)
- **one** annual exemption of €1,270
- taxable gain €3,730
- CGT due €3,730 × 33% = €1,230.90 → **€1,230** (rounded down)

The pre-fix per-holding model applied €1,270 twice (taxable €2,460, tax
€811) — this fixture pins the corrected single-exemption behaviour.

Exercised by
`cmd/taxman/validate_test.go::TestRunValidate_CGTYearSingleExemption_Passes`
and, at the unit level, by
`internal/engine/foryear_test.go::TestAggregateCGTYear_SingleExemptionAcrossHoldings`.
