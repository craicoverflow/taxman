# deemed_disposal_refund_on_later_fall

Golden fixture for SPEC.md §5's "deemed disposal charged, units then
fall, excess repayable" scenario.

Synthetic scenario: buy 100 units @ €10 on 2016-03-15; 8-year deemed
disposal on 2024-03-15 at €18 charges €328; the units then fall and are
sold @ €8 on 2024-09-01, below cost.

    deemed:  gain €800   tax €328
    actual:  gain 100 × 8 − 1,000 = −€200 → no gain arises, tax €0
             credit for the €328 already paid                = −€328

No gain arises on the actual disposal, so the whole €328 comes back:
"Where a gain is treated as nil and tax was chargeable in respect of an
earlier deemed disposal of the material interest, the overpaid amount
is refundable/available for set-off" (TCA Notes for Guidance,
s.747E(2)–(4); TDM Part 27-01A-02 §4.4.5 for the same offset-then-repay
mechanic on the investment-undertaking side).

The €200 real loss itself gets no relief — it does not net against
anything (s.747E(3) & (4)) — which is why `taxable_gain` stays at €800.

Run with `taxman validate --fixture testdata/golden/deemed_disposal_refund_on_later_fall --kind deemed --year 2024`.
