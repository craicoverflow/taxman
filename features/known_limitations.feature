Feature: Known limitations — where taxman's figure must not be used as-is

  These scenarios pin behaviour that is deliberately incomplete. They
  exist so the gaps are visible and testable, not hidden. See
  docs/maths.md section 11.

  Scenario: The s.581 four-week rule is not applied — plain FIFO is used
    TCA 1997 s.581 (Revenue TDM Part 19-04-03): where shares are sold and
    shares of the same class are reacquired within four weeks, the sale
    is matched against the reacquired shares (not the older holding), and
    a loss on the sale is allowed ONLY against a later gain on those
    reacquired shares.

    taxman does neither. It matches plain FIFO and lets the loss net
    normally. In this scenario the loss is EUR 400; s.581 would ring-fence
    it. A CGT return covering a sell-and-rebuy inside four weeks must be
    adjusted by hand.

    Given a CGT_ASSET holding "SHARE"
      And a buy of 20 "SHARE" at €100.00 on 2024-01-02
      And a sale of 20 "SHARE" at €80.00 on 2024-02-01
      And a buy of 20 "SHARE" at €82.00 on 2024-02-15
    When CGT is computed for the holding
    Then disposal 1 has a gain of -€400.00
      And the total gain is -€400.00
      And the taxable gain is €0.00
      And the CGT due is €0.00

  Scenario: The 8-year deemed disposal date is tracked, but no liability is computed
    A fund lot acquired on 15 March 2020 has its first deemed-disposal
    anniversary on 15 March 2028, and every 8 years after (Finance Act
    2013; Revenue "Funds"). taxman reports this date so it is not missed;
    it does NOT turn a reached anniversary into an exit-tax charge, nor
    handle the credit given for it against a later actual disposal. Those
    are computed outside taxman.

    Given an EXIT_TAX_FUND holding "IE_ETF"
    When the next deemed-disposal date is computed for a lot acquired on 2020-03-15, as of 2026-09-06
    Then the next deemed-disposal date is 2028-03-15

  Scenario: An unclassified holding blocks its own computation
    taxman never guesses CGT_ASSET vs EXIT_TAX_FUND. Until a human sets
    the classification, computation for that holding is refused (other
    classified holdings in the same run are unaffected).

    Given an UNCLASSIFIED holding "MYSTERY"
      And a buy of 10 "MYSTERY" at €100.00 on 2024-01-01
      And a sale of 10 "MYSTERY" at €300.00 on 2024-06-01
    When tax is computed with the holding left unclassified
    Then computation is blocked for "MYSTERY"
      And the reason given is "set its classification"
