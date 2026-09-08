# deemed_disposal_credit_on_actual_disposal

Golden fixture for SPEC.md §5's "deemed disposal followed by an actual
disposal, credit applied" scenario — the mechanics of Revenue TDM
Part 27-04-01 §4.1.4.

Synthetic scenario: buy 100 units @ €10 on 2016-03-15; 8-year deemed
disposal on 2024-03-15 at €18; sell all 100 @ €20 on 2024-09-01.

    deemed:  gain 100 × 18 − 1,000 = €800   tax 800 × 0.41  = €328
    actual:  gain 100 × 20 − 1,000 = €1,000 tax 1,000 × 0.41 = €410
             less credit for the €328 already paid           = €82

The actual disposal's gain is measured against the ORIGINAL €1,000
cost, not an €1,800 deemed-reacquisition uplift: "The original cost of
acquisition should be used in carrying out the calculation of the
taxable gain... where the disposal occurs after the deemed disposal."

Total tax across both events is €410 — exactly the tax on the actual
disposal, which is the cap the same paragraph imposes.

Run with `taxman validate --fixture testdata/golden/deemed_disposal_credit_on_actual_disposal --kind deemed --year 2024`.
