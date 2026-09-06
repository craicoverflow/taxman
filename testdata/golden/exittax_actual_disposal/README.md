# exittax_actual_disposal

Golden fixture for SPEC.md §5's "Exit-tax fund, actual disposal before
any deemed disposal has occurred" scenario.

Synthetic scenario: buy 100 units @ €10 (cost €1000) on 2024-01-01,
sell all 100 @ €15 (proceeds €1500) on 2024-06-01 — before the
2026-01-01 rate transition, so 41% applies. No annual exemption
(unlike CGT): full €500 gain is taxable. Tax due = 500 * 0.41 = €205.

Exercised directly by
`internal/engine/exittax_test.go::TestComputeExitTax_SimpleFIFO_ActualDisposal`.
See `testdata/golden/cgt_simple_fifo/README.md` for the note on why
these fixture files aren't yet loaded by a runtime harness.
