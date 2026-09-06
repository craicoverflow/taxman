Feature: FIFO lot matching

  Every acquisition (a buy or an RSU vest) opens a "lot": a quantity, a
  date, and a cost basis in euro. Every sale is matched against the
  oldest open lots first — first in, first out. This is the share
  identification rule Revenue applies to shares of the same class held
  without individual identification (Revenue TDM Part 19-04-03, "the
  asset purchased first is treated as the first asset sold (the first in
  first out (FIFO) rule)"; TCA 1997 s.581). The one statutory exception
  — a sale within four weeks of a purchase — is NOT applied by taxman;
  see known_limitations.feature.

  Disposal figures produced by engine.CGTDisposals.

  Scenario: One sale spanning two lots at different costs
    Lot 1: buy 10 at EUR 100 on 2021-01-04 (cost EUR 1,000).
    Lot 2: buy 10 at EUR 130 on 2022-03-01 (cost EUR 1,300).
    Sell 15 at EUR 200 on 2024-02-01 (proceeds EUR 3,000).
    FIFO: all 10 of lot 1 plus 5 of lot 2 are sold.
    Cost basis = EUR 1,000 + 5 x EUR 130 = EUR 1,650. Gain EUR 3,000 - EUR 1,650 = EUR 1,350.

    Given a CGT_ASSET holding "ACME"
      And a buy of 10 "ACME" at €100.00 on 2021-01-04
      And a buy of 10 "ACME" at €130.00 on 2022-03-01
      And a sale of 15 "ACME" at €200.00 on 2024-02-01
    When the lots are matched FIFO
    Then disposal 1 has a quantity of 15
      And disposal 1 has proceeds of €3,000.00
      And disposal 1 has a cost basis of €1,650.00
      And disposal 1 has a gain of €1,350.00

  Scenario: A later sale consumes the remainder of the second lot
    Continuing the same holding, a second sale of 5 at EUR 210 on 2024-09-01
    is matched against the 5 units left in lot 2.
    Cost basis = 5 x EUR 130 = EUR 650. Gain EUR 1,050 - EUR 650 = EUR 400.

    Given a CGT_ASSET holding "ACME"
      And a buy of 10 "ACME" at €100.00 on 2021-01-04
      And a buy of 10 "ACME" at €130.00 on 2022-03-01
      And a sale of 15 "ACME" at €200.00 on 2024-02-01
      And a sale of 5 "ACME" at €210.00 on 2024-09-01
    When the lots are matched FIFO
    Then disposal 2 has a quantity of 5
      And disposal 2 has a cost basis of €650.00
      And disposal 2 has a gain of €400.00

  Scenario: Selling more units than were ever acquired is an error, not a guess
    Buy 10, then try to sell 15. taxman refuses rather than inventing a lot.

    Given a CGT_ASSET holding "ACME"
      And a buy of 10 "ACME" at €100.00 on 2021-01-04
      And a sale of 15 "ACME" at €200.00 on 2024-02-01
    When the lots are matched FIFO
    Then the lookup is rejected
