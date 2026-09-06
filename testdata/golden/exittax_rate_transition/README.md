# exittax_rate_transition

Golden fixture for SPEC.md §5's "Exit-tax rate transition (41%->38%)"
scenario: confirms tax rules resolve by the disposal's own event date,
not by whenever the computation happens to run.

Synthetic scenario: buy 100 units @ €10 in 2020, sell all 100 @ €20 on
2025-12-31 (the day before Finance Act 2025's rate cut takes effect).
Gain €1000 * 0.41 = €410 due — the pre-transition rate, even though
this fixture (and the code computing it) will always be run well
after 2026-01-01.

Exercised directly by
`internal/engine/exittax_ratetransition_test.go`, which also covers
the on-boundary (2026-01-01, 38%) case and an explicit
"regardless-of-call-time" regression. See
`testdata/golden/cgt_simple_fifo/README.md` for the note on why these
fixture files aren't yet loaded by a runtime harness.
