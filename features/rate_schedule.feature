Feature: Effective-dated tax rates

  Every rate taxman applies is looked up by the date of the event it
  applies to — a disposal, a chargeable event, an interest credit — not
  by the date the computation is run. Rates live as a dated schedule in
  internal/taxrules, never as a constant in the engine. See docs/maths.md
  section 10.

  Scenario: The current CGT rate and annual exemption
    33% has applied to disposals since 6 December 2012 (TCA 1997 s.28;
    Revenue "How to calculate CGT"). The annual exempt amount has been
    EUR 1,270 since the 2002 euro changeover (TCA 1997 s.601).

    When the CGT rate on 2024-06-01 is looked up
    Then the CGT rate is 33%
      And the annual exemption is €1,270.00
      And the rule takes effect from 2012-12-06

  Scenario: The exit-tax rate the day before the 2026 reduction
    A chargeable event on 31 December 2025 is at 41% (Finance Act 2013).

    When the exit tax rate on 2025-12-31 is looked up
    Then the exit tax rate is 41%
      And the rule takes effect from 2014-01-01

  Scenario: The exit-tax rate on and after 1 January 2026
    Finance Act 2025 reduced the rate to 38% for chargeable events on or
    after 1 January 2026 (gov.ie, "Minister Donohoe publishes Finance
    Bill 2025").

    When the exit tax rate on 2026-01-01 is looked up
    Then the exit tax rate is 38%
      And the rule takes effect from 2026-01-01

  Scenario: The current DIRT rate
    33% since 1 January 2020 (Revenue, "What DIRT rate is applicable").

    When the DIRT rate on 2024-06-01 is looked up
    Then the DIRT rate is 33%
      And the rule takes effect from 2020-01-01

  Scenario: A historical DIRT rate is resolved to its own year
    37% applied in 2018 — the 41% rate of 2014-2016 was reduced 2 points
    a year from 2017 (Finance Act 2016). The 2014-2020 entries are held
    with confidence in internal/taxrules; entries before 2014 are marked
    UNVERIFIED there and should be checked against the Finance Act before
    being relied on for a past year.

    When the DIRT rate on 2018-06-01 is looked up
    Then the DIRT rate is 37%
      And the rule takes effect from 2018-01-01
