Feature: Rounding the tax figure down to whole euro

  Only the final tax figure is rounded, and it is rounded DOWN to whole
  euro — the cents fall in the taxpayer's favour, the long-standing
  Revenue self-assessment convention (internal/engine/round.go). Every
  figure upstream — gains, proceeds, cost bases, the exemption, loss
  pools, exchange-rate conversions — keeps full decimal precision, so
  rounding is applied once, at the end, and never compounds.

  Scenario: CGT — 33% of the taxable gain, then floored
    Buy 10 at EUR 100 (cost EUR 1,000); sell 10 at EUR 430 (proceeds EUR 4,300).
    Gain EUR 3,300; less EUR 1,270 exemption = EUR 2,030 taxable.
    EUR 2,030 x 33% = EUR 669.90, rounded down to EUR 669.

    Given a CGT_ASSET holding "AAPL"
      And a buy of 10 "AAPL" at €100.00 on 2024-01-01
      And a sale of 10 "AAPL" at €430.00 on 2024-06-01
    When CGT is computed for the holding
    Then the taxable gain is €2,030.00
      And the CGT due is €669.00

  Scenario: Exit tax — 41% of the taxable gain, then floored
    Buy 100 at EUR 10 (cost EUR 1,000); sell 100 at EUR 19.99 (proceeds EUR 1,999).
    Gain EUR 999 x 41% = EUR 409.59, rounded down to EUR 409.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2024-01-01
      And a sale of 100 "IE_ETF" at €19.99 on 2024-06-01
    When exit tax is computed for the holding
    Then the taxable gain is €999.00
      And the exit tax due is €409.00

  Scenario: DIRT — 33% of the interest, then floored
    One EUR 101 interest credit. EUR 101 x 33% = EUR 33.33, rounded down to EUR 33.

    Given an interest credit of €101.00 on 2024-06-01
    When DIRT is computed
    Then the total interest is €101.00
      And the DIRT due is €33.00
