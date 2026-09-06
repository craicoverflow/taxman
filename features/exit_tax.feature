Feature: Exit tax on funds (EXIT_TAX_FUND holdings)

  Irish and EU-domiciled UCITS ETFs and equivalent offshore funds are
  taxed under the fund "gross roll-up" / exit-tax regime, not CGT. taxman
  applies it only to a holding a human has classified EXIT_TAX_FUND; it
  never guesses. Figures produced by engine.ComputeExitTax /
  engine.ComputeExitTaxForYear.

  How this regime differs from CGT (docs/maths.md section 8):
    * Rate 41% for a chargeable event before 1 January 2026; 38% for a
      chargeable event on or after 1 January 2026 (Finance Act 2025;
      gov.ie "Minister Donohoe publishes Finance Bill 2025"). The rate
      is fixed by the event's date, whenever the computation is run.
    * No annual exempt amount — the EUR 1,270 is never applied.
    * No loss relief per chargeable event: a disposal that makes a loss
      contributes zero and never nets against a gaining disposal, not
      even the same fund in the same year (Revenue TDM Part 27-01a-02 /
      Part 27-04-01). "Total gain" is shown for transparency; only the
      positive per-disposal gains are taxable.
    * An 8-year deemed disposal applies (see known_limitations.feature).

  Scenario: A straightforward actual disposal at a gain
    Buy 100 at EUR 10 (cost EUR 1,000); sell 100 at EUR 15 (proceeds EUR 1,500).
    Gain EUR 500, taxed in full at 41% = EUR 205. No exemption is applied.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2024-01-01
      And a sale of 100 "IE_ETF" at €15.00 on 2024-06-01
    When exit tax is computed for the holding
    Then disposal 1 has a gain of €500.00
      And the total gain is €500.00
      And the taxable gain is €500.00
      And the exit tax due is €205.00

  Scenario: A losing disposal does not reduce the tax on a gaining one
    Buy 200 at EUR 10 (cost EUR 2,000).
    Sell 100 at EUR 20 on 2024-03-01: cost EUR 1,000, gain EUR 1,000.
    Sell 100 at EUR 3 on 2024-09-01: cost EUR 1,000, loss EUR 700.
    Arithmetic sum of gains is EUR 300, but the loss is dead: taxable
    gain is the EUR 1,000 positive gain alone, exit tax at 41% = EUR 410.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 200 "IE_ETF" at €10.00 on 2020-01-01
      And a sale of 100 "IE_ETF" at €20.00 on 2024-03-01
      And a sale of 100 "IE_ETF" at €3.00 on 2024-09-01
    When exit tax is computed for the holding
    Then disposal 1 has a gain of €1,000.00
      And disposal 2 has a gain of -€700.00
      And the total gain is €300.00
      And the taxable gain is €1,000.00
      And the exit tax due is €410.00

  Scenario: A disposal dated 31 December 2025 is taxed at 41%, not the later rate
    Buy 100 at EUR 10 in 2020; sell 100 at EUR 20 on 2025-12-31. Gain EUR 1,000.
    The chargeable event is in 2025, so the pre-transition 41% rate applies: EUR 410.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2020-01-01
      And a sale of 100 "IE_ETF" at €20.00 on 2025-12-31
    When exit tax is computed for the 2025 tax year
    Then the taxable gain is €1,000.00
      And the exit tax due is €410.00

  Scenario: The same fund, a disposal dated 1 January 2026 is taxed at 38%
    Buy 100 at EUR 10 in 2020; sell 100 at EUR 30 on 2026-06-15. Gain EUR 2,000.
    The chargeable event is in 2026, so the reduced 38% rate applies: EUR 760.

    Given an EXIT_TAX_FUND holding "IE_ETF"
      And a buy of 100 "IE_ETF" at €10.00 on 2020-01-01
      And a sale of 100 "IE_ETF" at €30.00 on 2026-06-15
    When exit tax is computed for the 2026 tax year
    Then the taxable gain is €2,000.00
      And the exit tax due is €760.00
