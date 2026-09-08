Feature: The 8-year deemed disposal on funds

  A fund is not left to roll up untaxed forever. Under TCA 1997
  s.747E(6) a material interest in a fund is deemed disposed of at the
  end of each 8-year period from acquisition, and taxed as if it had
  been sold — whether or not anything was actually sold. Figures
  produced by engine.ComputeFundTax / engine.ComputeFundTaxForYear.

  The rules taxman applies (docs/maths.md section 8.1), each cited:
    * The gain is "the value of the units at the time less their cost of
      acquisition", matched FIFO where units have moved (Revenue TDM
      Part 27-04-01 section 4.1.5).
    * That anniversary value is user-entered. taxman has no historical
      price source and will not estimate one: an anniversary reached
      with no value on record blocks the holding.
    * On a later chargeable event the gain is measured from the
      ORIGINAL cost, not from a deemed-reacquisition uplift, and the tax
      already paid is credited: "The total tax liability (between deemed
      and actual disposals) should not exceed the tax liability that
      arises on the actual disposal" (TDM Part 27-04-01 section 4.1.4).
    * Where the credit exceeds the later charge, the excess comes back:
      "the overpaid amount is refundable/available for set-off" (Notes
      for Guidance s.747E(2)-(4); TDM Part 27-01A-02 section 4.4.5).
    * Exit tax's own rules still apply to each event: no annual exempt
      amount, and no loss relief (s.747E(3) & (4)).

  Scenario: The anniversary date is 8 years to the day from acquisition
    A lot acquired on 15 March 2020 reaches its first deemed disposal on
    15 March 2028, and another every 8 years it is still held.

    Given an EXIT_TAX_FUND holding "IE_ETF"
    When the next deemed-disposal date is computed for a lot acquired on 2020-03-15, as of 2026-09-06
    Then the next deemed-disposal date is 2028-03-15

  Scenario: Nothing was sold, but the 8-year anniversary is still taxed
    Buy 100 at EUR 10 (cost EUR 1,000) on 2016-03-15 and hold. On
    2016-03-15 + 8 years the units are worth EUR 18 each.
    Gain 1,800 - 1,000 = EUR 800, taxed at the 2024 rate of 41% = EUR 328.
    No units changed hands.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
      And the market value of "IE_ETF" on 2024-03-15 is €18.00 per unit
    When fund tax is computed for the 2024 tax year
    Then deemed disposal 1 has a quantity of 100
      And deemed disposal 1 has a value of €1,800.00
      And deemed disposal 1 has a cost basis of €1,000.00
      And deemed disposal 1 has a gain of €800.00
      And deemed disposal 1 charges €328.00
      And the exit tax due is €328.00

  Scenario: An anniversary that has not arrived yet charges nothing
    The same lot, computed as of the day before its anniversary. The
    charge is not accrued early, and no market value is needed.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
    When fund tax is computed as of 2024-03-14
    Then the exit tax due is €0.00

  Scenario: A later sale credits the tax already paid at the anniversary
    The same holding, then sold at EUR 20 on 2024-09-01.
    The sale's gain is measured from the ORIGINAL EUR 1,000 cost, not
    from the EUR 1,800 the units were valued at: 2,000 - 1,000 = EUR
    1,000, tax EUR 410. The EUR 328 already paid is credited, leaving
    EUR 82. Total across both events is EUR 410 — exactly the tax on the
    actual disposal.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
      And a sale of 100 "IE_ETF" at €20.00 on 2024-09-01
      And the market value of "IE_ETF" on 2024-03-15 is €18.00 per unit
    When fund tax is computed for the 2024 tax year
    Then deemed disposal 1 charges €328.00
      And disposal 1 has a cost basis of €1,000.00
      And disposal 1 has a gain of €1,000.00
      And disposal 1 has a credit of €328.00
      And disposal 1 charges €82.00
      And the exit tax due is €410.00
      And the refund due is €0.00

  Scenario: The units fall before the sale, so part of the charge comes back
    The same EUR 328 charge at the anniversary, but the units are sold
    at EUR 12. The real gain is only 1,200 - 1,000 = EUR 200, tax EUR
    82. The EUR 328 already paid exceeds that, and the EUR 246
    difference is repayable. Net, the taxpayer bears the EUR 82 the
    actual disposal charges.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
      And a sale of 100 "IE_ETF" at €12.00 on 2024-09-01
      And the market value of "IE_ETF" on 2024-03-15 is €18.00 per unit
    When fund tax is computed for the 2024 tax year
    Then disposal 1 has a gain of €200.00
      And disposal 1 has a credit of €328.00
      And disposal 1 charges -€246.00
      And the exit tax due is €328.00
      And the refund due is €246.00

  Scenario: Sold below cost after a deemed disposal, the whole charge comes back
    Sold at EUR 8, under the EUR 10 cost. No gain arises on the actual
    disposal, so the entire EUR 328 is repayable. The EUR 200 real loss
    gets no relief of its own — it is not deductible anywhere — which is
    why the taxable gain still reads EUR 800.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
      And a sale of 100 "IE_ETF" at €8.00 on 2024-09-01
      And the market value of "IE_ETF" on 2024-03-15 is €18.00 per unit
    When fund tax is computed for the 2024 tax year
    Then disposal 1 has a gain of -€200.00
      And disposal 1 charges -€328.00
      And the taxable gain is €800.00
      And the exit tax due is €328.00
      And the refund due is €328.00

  Scenario: The second anniversary measures from the original cost again
    Buy 100 at EUR 10 in 2008. First anniversary 2016-03-15 at EUR 18
    charges EUR 328. Second anniversary 2024-03-15 at EUR 25: the gain
    runs from the ORIGINAL EUR 1,000 cost, not from EUR 1,800, so the
    cumulative tax is 1,500 x 0.41 = EUR 615, less the EUR 328 already
    paid = EUR 287 now.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2008-03-15
      And the market value of "IE_ETF" on 2016-03-15 is €18.00 per unit
      And the market value of "IE_ETF" on 2024-03-15 is €25.00 per unit
    When fund tax is computed as of 2024-12-31
    Then deemed disposal 1 charges €328.00
      And deemed disposal 2 has a cost basis of €1,000.00
      And deemed disposal 2 has a gain of €1,500.00
      And deemed disposal 2 has a credit of €328.00
      And deemed disposal 2 charges €287.00
      And the exit tax due is €615.00

  Scenario: Only the units still held reach the anniversary
    100 bought in 2016, 40 sold in 2020. Four years later only the
    remaining 60 units reach the anniversary: 60 x (18 - 10) = EUR 480,
    taxed at 41% = EUR 196.80.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
      And a sale of 40 "IE_ETF" at €15.00 on 2020-01-01
      And the market value of "IE_ETF" on 2024-03-15 is €18.00 per unit
    When fund tax is computed for the 2024 tax year
    Then deemed disposal 1 has a quantity of 60
      And deemed disposal 1 has a gain of €480.00
      And deemed disposal 1 charges €196.80

  Scenario: A lot sold before its anniversary never reaches one
    Bought in 2016 and sold in full in 2020, four years before the
    anniversary would have fallen. There is nothing left to deem
    disposed, and no market value is needed for 2024.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
      And a sale of 100 "IE_ETF" at €15.00 on 2020-01-01
    When fund tax is computed as of 2024-12-31
    Then the exit tax due is €205.00
      And the refund due is €0.00

  Scenario: A part sale after the anniversary takes its share of the credit
    All 100 units are charged at the anniversary (EUR 328). Selling 40
    of them afterwards carries 40/100 of that credit — EUR 131.20 —
    against the EUR 164 due on its own gain, leaving EUR 32.80. The
    other 60 units keep their share for a later event.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
      And a sale of 40 "IE_ETF" at €20.00 on 2024-09-01
      And the market value of "IE_ETF" on 2024-03-15 is €18.00 per unit
    When fund tax is computed for the 2024 tax year
    Then disposal 1 has a credit of €131.20
      And disposal 1 charges €32.80

  Scenario: An anniversary on or after 1 January 2026 is taxed at 38%
    The chargeable event is the anniversary itself, so its own date
    fixes the rate. A lot acquired 2018-03-15 reaches 8 years on
    2026-03-15, after the Finance Act 2025 cut: 800 x 0.38 = EUR 304.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2018-03-15
      And the market value of "IE_ETF" on 2026-03-15 is €18.00 per unit
    When fund tax is computed for the 2026 tax year
    Then deemed disposal 1 has a gain of €800.00
      And the exit tax due is €304.00

  Scenario: A reached anniversary with no market value blocks the holding
    taxman has no historical price source, and internal/prices is walled
    off from the engine by design. Rather than estimate the value of the
    units on the anniversary, it refuses to compute the holding at all
    and names the lot and date that need a figure.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2016-03-15
    When fund tax is computed for the 2024 tax year
    Then the holding is blocked pending a market value
