# cgt_loss_carryforward

Golden fixture for finding #2: an unused net capital loss carries
forward to a later tax year and is set against a future gain, under the
s.601(4) restriction that brought-forward losses are used only down to
the annual exemption (never wasting it).

Synthetic scenario (both CGT_ASSET):

| instrument | buy | sell | gain |
|---|---|---|---|
| OLD | 10 @ €500 (2023-01-01) | 10 @ €100 (2024-03-01) | −€4,000 |
| NEW | 10 @ €100 (2024-01-01) | 10 @ €1,100 (2025-06-01) | +€10,000 |

- **2024**: net −€4,000 → nothing chargeable, €4,000 carried forward.
- **2025** (`taxman validate --kind cgt_year --year 2025`): net gain
  €10,000; brought-forward loss €4,000, all usable (€10,000 − €1,270 =
  €8,730 ≥ €4,000) → after losses €6,000; one exemption €1,270 →
  taxable €4,730; CGT €4,730 × 33% = €1,560.90 → **€1,560**. Nothing
  left to carry forward.

`AggregateCGTYear` derives the carry-forward statelessly by replaying
2024 then 2025, so it needs the full history, not just 2025's rows.

Exercised by
`cmd/taxman/validate_test.go::TestRunValidate_CGTLossCarryforward_Passes`
and
`internal/engine/foryear_test.go::TestAggregateCGTYear_LossCarriedForwardToLaterYear`.
