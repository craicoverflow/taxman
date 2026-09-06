Feature: Restating non-euro transactions in euro

  A transaction in any currency other than euro is restated in euro at
  the European Central Bank reference rate for that transaction's own
  date, before any matching or gain calculation. A lot's cost and a
  sale's proceeds are each fixed in euro on their own date and never
  re-translated later.

  Irish basis: any non-euro currency is itself an asset for CGT, and a
  gain or loss is "computed by reference to the corresponding euro value
  of the purchase price and the sale proceeds"; foreign consideration is
  converted "using the rate of exchange applicable at the date of" the
  transaction (Revenue, "How to calculate CGT"; Revenue TDM
  Part 19-01-14a; TCA 1997 s.532, s.552). taxman's rates come from the
  ECB euro foreign-exchange reference series embedded in internal/fx
  (eurofxref-hist.csv); a date the series does not cover is an error,
  never an approximation.

  Rates below are euro per one unit of the currency, to six decimal
  places, as engine/internal fx resolves them.

  Scenario: The ECB reference rate is taken on the transaction's own date
    Given a CGT_ASSET holding "US_STOCK"
    When the ECB reference rate for USD on 2021-01-25 is looked up
    Then the euro value of one USD is 0.822910

  Scenario: A weekend transaction resolves to the previous publication day
    2022-06-19 is a Sunday; the ECB last published on Friday 2022-06-17.

    Given a CGT_ASSET holding "US_STOCK"
    When the ECB reference rate for USD on 2022-06-19 is looked up
    Then the euro value of one USD is 0.953652

  Scenario: A date before the ECB series begins is rejected, not guessed
    The embedded series starts in January 1999.

    Given a CGT_ASSET holding "US_STOCK"
    When the ECB reference rate for USD on 1998-01-02 is looked up
    Then the lookup is rejected

  Scenario: A full CGT computation on a USD holding, restated per transaction date
    Buy 10 at USD 100 on 2021-01-25 (ECB 1.2152 USD per EUR).
    Buy 10 at USD 120 on 2022-06-15 (ECB 1.0430 USD per EUR).
    Sell 15 at USD 200 on 2024-01-02 (ECB 1.0956 USD per EUR).
    FIFO: all of lot 1 and half of lot 2. Cost basis and proceeds are each
    fixed in euro on their own date; the euro gain lands just above the
    EUR 1,270 exemption, so a small CGT charge results at 33%, rounded
    down to whole euro. (Full-precision figures are pinned in
    testdata/golden/cgt_fx_conversion; this shows the euro-rounded view.)

    Given a CGT_ASSET holding "US_STOCK"
      And a buy of 10 "US_STOCK" at 100.00 USD on 2021-01-25
      And a buy of 10 "US_STOCK" at 120.00 USD on 2022-06-15
      And a sale of 15 "US_STOCK" at 200.00 USD on 2024-01-02
    When CGT is computed for the holding
    Then disposal 1 has proceeds of €2,738.23
      And disposal 1 has a cost basis of €1,398.12
      And disposal 1 has a gain of €1,340.11
      And the taxable gain is €70.11
      And the CGT due is €23.00
