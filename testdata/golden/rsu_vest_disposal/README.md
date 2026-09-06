# rsu_vest_disposal

Golden fixture for SPEC.md §5's "RSU vest followed by later disposal,
cost basis = vest date FMV" scenario.

Synthetic scenario: 20 shares vest on 2024-01-15 at FMV €40/share
(cost basis €800), then all 20 are sold on 2024-08-01 at €60/share.
Confirms the lot's cost basis is the vest-date FMV — not zero, and not
the eventual sale price.

Exercised directly by
`internal/engine/rsu_test.go::TestComputeCGT_RSUVest_CostBasisIsVestDateFMV`.
See `testdata/golden/cgt_simple_fifo/README.md` for the note on why
these fixture files aren't yet loaded by a runtime harness.
