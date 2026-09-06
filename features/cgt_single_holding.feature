Feature: Capital Gains Tax on a single holding

  The single-holding mechanic: FIFO-match one holding's sales against its
  own buys, sum the gains, take off the annual exemption, apply the rate,
  round the tax down. The real annual charge nets every holding together
  (see cgt_year_aggregation.feature and docs/maths.md section 7); this is
  the building block. Figures produced by engine.ComputeCGT.

  Rules pinned here:
    * Gain on a disposal = proceeds - cost basis (TCA 1997 s.545/s.552).
    * One annual exempt amount of EUR 1,270 per year (TCA 1997 s.601).
    * Rate 33% since 6 December 2012 (TCA 1997 s.28; Revenue "How to
      calculate CGT").
    * The tax figure is rounded DOWN to whole euro; the gain is not
      (internal/engine/round.go; Revenue self-assessment convention).

  Scenario: A single disposal whose whole gain is inside the annual exemption
    Buy 10 at EUR 100 (cost EUR 1,000); sell 10 at EUR 150 (proceeds EUR 1,500).
    Gain EUR 500, which is below the EUR 1,270 exemption, so nothing is taxable.

    Given a CGT_ASSET holding "AAPL"
      And a buy of 10 "AAPL" at €100.00 on 2024-01-01
      And a sale of 10 "AAPL" at €150.00 on 2024-06-01
    When CGT is computed for the holding
    Then disposal 1 has a quantity of 10
      And disposal 1 has proceeds of €1,500.00
      And disposal 1 has a cost basis of €1,000.00
      And disposal 1 has a gain of €500.00
      And the total gain is €500.00
      And the taxable gain is €0.00
      And the CGT due is €0.00

  Scenario: A single disposal whose gain exceeds the annual exemption
    Buy 10 at EUR 100 (cost EUR 1,000); sell 10 at EUR 400 (proceeds EUR 4,000).
    Gain EUR 3,000; less EUR 1,270 exemption = EUR 1,730 taxable.
    CGT at 33% = EUR 570.90, rounded down to EUR 570.

    Given a CGT_ASSET holding "AAPL"
      And a buy of 10 "AAPL" at €100.00 on 2024-01-01
      And a sale of 10 "AAPL" at €400.00 on 2024-06-01
    When CGT is computed for the holding
    Then the total gain is €3,000.00
      And the taxable gain is €1,730.00
      And the CGT due is €570.00
