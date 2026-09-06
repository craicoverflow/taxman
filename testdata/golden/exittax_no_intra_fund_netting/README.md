# exittax_no_intra_fund_netting

Golden fixture for finding #3: exit tax has **no loss relief per
chargeable event**. A losing disposal of a fund does not net against a
gaining disposal of the *same fund* in the *same year*.

Synthetic scenario, one EXIT_TAX_FUND holding, one lot of 200 @ €10:

| date | sell | proceeds | FIFO cost | gain |
|---|---|---|---|---|
| 2024-03-01 | 100 @ €20 | €2,000 | €1,000 | +€1,000 |
| 2024-09-01 | 100 @ €3  | €300   | €1,000 | −€700  |

`taxman validate --kind exit_tax`:

- `total_gain` €300 — the raw arithmetic sum, shown for transparency
- `taxable_gain` **€1,000** — only the positive disposal; the €700 loss
  is ignored, not netted
- `tax_due` €1,000 × 41% = **€410** (2024 rate; rounds to whole euro)

The pre-fix code summed to €300 first and taxed that.

Exercised by
`cmd/taxman/validate_test.go::TestRunValidate_ExitTaxNoIntraFundNetting_Passes`
and
`internal/engine/exittax_test.go::TestComputeExitTax_LosingDisposalDoesNotNetAgainstGainingDisposal`.
