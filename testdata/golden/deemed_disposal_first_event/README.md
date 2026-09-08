# deemed_disposal_first_event

Golden fixture for SPEC.md §5's "8-year deemed disposal, first
anniversary reached, units still held" scenario.

Synthetic scenario: buy 100 units @ €10 (cost €1,000) on 2016-03-15 and
hold. The 8-year deemed disposal falls on 2024-03-15 (TCA 1997
s.747E(6)), when the units are worth €18 each. Nothing was sold — the
charge arises anyway, which is the whole point of the rule.

    gain = 100 × 18 − 1,000 = €800
    tax  = 800 × 0.41       = €328        (2024 rate)

`valuations.csv` is the fixture's stand-in for the user-entered
anniversary value the engine refuses to guess (`internal/valuations`).

Run with `taxman validate --fixture testdata/golden/deemed_disposal_first_event --kind deemed --year 2024`.
