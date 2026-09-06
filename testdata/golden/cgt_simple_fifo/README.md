# cgt_simple_fifo

Golden fixture for SPEC.md §5's "CGT disposal, simple FIFO, no
complications" scenario.

Synthetic scenario: buy 10 units @ €100 on 2024-01-01, sell all 10 @
€150 on 2024-06-01. Single lot, full disposal, gain below the annual
exemption.

Exercised directly by
`internal/engine/cgt_test.go::TestComputeCGT_SimpleFIFO_SingleLotFullDisposal`,
which encodes the same input/expected-output pair in Go rather than
reading these files at runtime (no fixture-loading harness exists yet
— see `taxman validate`, task 8.4). This directory documents the
scenario and its expected result for human review and for a future
`taxman validate --fixture` run to consume once that command exists.
