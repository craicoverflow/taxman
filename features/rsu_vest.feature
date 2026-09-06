Feature: RSU vesting as a CGT acquisition

  When Restricted Stock Units vest, the market value of the shares at the
  vesting date is charged to income tax through payroll (out of scope for
  taxman). That same vest-date market value becomes the CGT base cost of
  the shares, so the amount already taxed as income is not taxed again as
  a gain (Revenue, "Restricted Stock Units (RSUs)"; Revenue Share Schemes
  Manual Chapter 2). A later sale is an ordinary CGT disposal.

  taxman builds a lot from an rsu_vest event exactly as it would from a
  buy, using the vest-date fair market value as the lot's cost — never
  zero, never the eventual sale price. Set the holding to CGT_ASSET once
  it appears.

  Scenario: Vest then sell — only the post-vest movement is a gain
    Vest 20 shares at a EUR 40 fair market value (base cost EUR 800; the
    EUR 800 is taxed as income via payroll, not here).
    Sell 20 at EUR 60 (proceeds EUR 1,200). Gain EUR 1,200 - EUR 800 = EUR 400.
    EUR 400 is within the EUR 1,270 annual exemption, so no CGT is due —
    but the disposal is still computed and reported.

    Given a CGT_ASSET holding "ETRADE_CO"
      And an RSU vest of 20 "ETRADE_CO" at €40.00 on 2024-01-15
      And a sale of 20 "ETRADE_CO" at €60.00 on 2024-08-01
    When CGT is computed for the holding
    Then disposal 1 has a cost basis of €800.00
      And disposal 1 has proceeds of €1,200.00
      And disposal 1 has a gain of €400.00
      And the total gain is €400.00
      And the taxable gain is €0.00
      And the CGT due is €0.00
