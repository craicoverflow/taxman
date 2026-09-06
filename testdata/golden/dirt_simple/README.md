# dirt_simple

Golden fixture for SPEC.md §5's "DIRT on N26 interest, straightforward"
scenario.

Synthetic scenario: a single €200 interest credit on 2024-06-01.
DIRT 33% * 200 = €66.

Exercised directly by
`internal/engine/dirt_test.go::TestComputeDIRT_SingleCredit`. See
`testdata/golden/cgt_simple_fifo/README.md` for the note on why these
fixture files aren't yet loaded by a runtime harness.
