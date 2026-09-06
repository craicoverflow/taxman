Feature: Capital Gains Tax for a whole tax year, aggregated across every holding

  This is how an individual's CGT is actually assessed for a year, and
  it cannot be done one holding at a time. See docs/maths.md section 7.

  The Irish rules this feature pins:

    * Chargeable gains for the year are the aggregate of every gain and
      every loss on CGT_ASSET holdings, netted together — a loss on one
      holding reduces a gain on another (TCA 1997 s.31; Revenue TDM
      Part 19-02-05).
    * Unused capital losses from earlier years are carried forward, but
      are set against a later year's gain only so far as to leave the
      annual exempt amount un-wasted — brought-forward losses cannot
      reduce the year's gains below EUR 1,270 (TCA 1997 s.601(3)-(4);
      Revenue TDM Part 19-07-01). Current-year losses are not so
      protected — they net in full.
    * One personal annual exempt amount of EUR 1,270 is then deducted,
      once for the year, never once per holding, and it is not
      transferable between spouses (TCA 1997 s.601; Revenue TDM
      Part 19-07-01, reviewed December 2024).
    * CGT is charged on the balance at the rate in force at 31 December
      of that year (33% since 6 December 2012 — TCA 1997 s.28) and the
      tax is rounded down to whole euro (Revenue self-assessment
      convention; internal/engine/round.go).

  Every figure below is produced by engine.AggregateCGTYear over
  engine.CGTDisposals per holding. Regenerate with `make regen-features`.

  Scenario: One annual exemption is shared across two holdings sold in the same year
    AAPL: buy 10 at EUR 100 (cost EUR 1,000); sell 10 at EUR 300 (proceeds EUR 3,000); gain EUR 2,000.
    MSFT: buy 10 at EUR 100 (cost EUR 1,000); sell 10 at EUR 400 (proceeds EUR 4,000); gain EUR 3,000.
    Net chargeable gain EUR 2,000 + EUR 3,000 = EUR 5,000.
    Less one EUR 1,270 annual exemption = EUR 3,730 taxable.
    CGT at 33% = EUR 1,230.90, rounded down to EUR 1,230.
    (A per-holding model would wrongly apply EUR 1,270 twice: taxable EUR 2,460, tax EUR 811.)

    Given a CGT_ASSET holding "AAPL"
      And a buy of 10 "AAPL" at €100.00 on 2024-01-01
      And a sale of 10 "AAPL" at €300.00 on 2024-06-01
      And a CGT_ASSET holding "MSFT"
      And a buy of 10 "MSFT" at €100.00 on 2024-01-01
      And a sale of 10 "MSFT" at €400.00 on 2024-07-01
    When CGT is aggregated for the 2024 tax year
    Then holding "AAPL" has a realised gain of €2,000.00
      And holding "MSFT" has a realised gain of €3,000.00
      And the net chargeable gain is €5,000.00
      And the loss brought forward is €0.00
      And the annual exemption applied is €1,270.00
      And the taxable gain is €3,730.00
      And the CGT rate is 33%
      And the CGT due is €1,230.00

  Scenario: A net loss in one year carries forward and is set against a later year's gain
    2023-2024 on "OLD": buy 10 at EUR 500 (cost EUR 5,000); sell 10 at EUR 100 (proceeds EUR 1,000); loss EUR 4,000.
    2024-2025 on "NEW": buy 10 at EUR 100 (cost EUR 1,000); sell 10 at EUR 1,100 (proceeds EUR 11,000); gain EUR 10,000.
    2024 nets to a EUR 4,000 loss: nothing chargeable, EUR 4,000 carried forward.
    2025: net gain EUR 10,000; usable brought-forward loss = EUR 10,000 - EUR 1,270 = EUR 8,730, so all EUR 4,000 is used.
    After losses EUR 6,000; less one EUR 1,270 exemption = EUR 4,730 taxable.
    CGT at 33% = EUR 1,560.90, rounded down to EUR 1,560. Nothing left to carry forward.

    Given a CGT_ASSET holding "OLD"
      And a buy of 10 "OLD" at €500.00 on 2023-01-01
      And a sale of 10 "OLD" at €100.00 on 2024-03-01
      And a CGT_ASSET holding "NEW"
      And a buy of 10 "NEW" at €100.00 on 2024-01-01
      And a sale of 10 "NEW" at €1100.00 on 2025-06-01
    When CGT is aggregated for the 2025 tax year
    Then the net chargeable gain is €10,000.00
      And the loss brought forward is €4,000.00
      And the loss brought forward used is €4,000.00
      And the annual exemption applied is €1,270.00
      And the taxable gain is €4,730.00
      And the CGT due is €1,560.00
      And the loss carried forward is €0.00

  Scenario: Brought-forward losses are not used to a point that wastes the annual exemption
    "OLD": buy 10 at EUR 1,100 (cost EUR 11,000); sell 10 at EUR 100 (proceeds EUR 1,000) in 2024; loss EUR 10,000 carried forward.
    "NEW": buy 10 at EUR 100 (cost EUR 1,000); sell 10 at EUR 300 (proceeds EUR 3,000) in 2025; gain EUR 2,000.
    2025 net gain EUR 2,000; usable brought-forward loss = max(EUR 0, EUR 2,000 - EUR 1,270) = EUR 730.
    Only EUR 730 of the EUR 10,000 pool is used; the EUR 1,270 exemption still absorbs the rest.
    Taxable gain EUR 0, CGT EUR 0, and EUR 9,270 continues to carry forward.

    Given a CGT_ASSET holding "OLD"
      And a buy of 10 "OLD" at €1100.00 on 2023-01-01
      And a sale of 10 "OLD" at €100.00 on 2024-03-01
      And a CGT_ASSET holding "NEW"
      And a buy of 10 "NEW" at €100.00 on 2024-01-01
      And a sale of 10 "NEW" at €300.00 on 2025-06-01
    When CGT is aggregated for the 2025 tax year
    Then the net chargeable gain is €2,000.00
      And the loss brought forward is €10,000.00
      And the loss brought forward used is €730.00
      And the annual exemption applied is €1,270.00
      And the taxable gain is €0.00
      And the CGT due is €0.00
      And the loss carried forward is €9,270.00
