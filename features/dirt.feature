Feature: Deposit Interest Retention Tax (DIRT)

  DIRT is charged on interest from deposit accounts. taxman totals the
  hand-entered interest credits — savings accounts have no clean export,
  so the user types each credit in against an account they name — and
  applies the DIRT rate. Which institution paid the interest changes
  nothing: DIRT is DIRT. No lots, no matching. Figures produced by
  engine.ComputeDIRT / engine.ComputeDIRTForYear.

  Rules pinned here (docs/maths.md section 9):
    * DIRT is "appropriate tax" under TCA 1997 s.256(1); guidance in
      Revenue TDM Part 08-04-01 (ss.256-267).
    * Rate 33% since 1 January 2020 (Revenue, "What DIRT rate is
      applicable"). Earlier years resolve to their own rate: 37% for
      2018.
    * The whole-history figure resolves the rate at the latest credit's
      date; the per-year figure resolves it within that year.
    * Interest must be in euro. The tax is rounded down to whole euro.

  Scenario: DIRT on a single interest credit at the current rate
    One EUR 200 interest credit in 2024. DIRT at 33% = EUR 66.

    Given an interest credit of €200.00 on 2024-06-01
    When DIRT is computed
    Then the total interest is €200.00
      And the DIRT due is €66.00

  Scenario: A pre-2020 credit is charged at that year's DIRT rate
    One EUR 500 interest credit in 2018, when the rate was 37% (the 41%
    rate of 2014-2016 was stepped down 2 points a year from 2017 under
    Finance Act 2016, reaching 33% in 2020).
    DIRT = EUR 500 x 37% = EUR 185.

    Given an interest credit of €500.00 on 2018-06-01
    When DIRT is computed
    Then the total interest is €500.00
      And the DIRT due is €185.00

  Scenario: Several credits in one year are summed, then taxed once
    EUR 120 in March and EUR 80 in October 2025; total EUR 200; DIRT at 33% = EUR 66.

    Given an interest credit of €120.00 on 2025-03-01
      And an interest credit of €80.00 on 2025-10-01
    When DIRT is computed for the 2025 tax year
    Then the total interest is €200.00
      And the DIRT due is €66.00
