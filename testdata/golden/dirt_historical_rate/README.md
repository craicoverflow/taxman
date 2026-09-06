# dirt_historical_rate

Golden fixture for finding #4: `taxrules` resolves the rate in force on
the **event's own date**, so a backfilled interest credit in an earlier
tax year is taxed at that year's DIRT rate, not today's 33%.

Synthetic scenario: a single €500 interest credit dated 2018-06-01.

`taxman validate --kind dirt`:

- 2018 DIRT rate: **37%** (the 41%→33% step-down ran 2%/yr from 2017)
- `tax_due` €500 × 37% = **€185**

> The pre-2020 DIRT rates in `internal/taxrules/rates.go` are marked
> **UNVERIFIED** — reconstructed from memory of the Budget changes.
> Confirm 2018 = 37% against the Revenue DIRT guidance before relying
> on a backfilled pre-2020 figure. This fixture pins the
> resolve-by-date behaviour regardless of the exact rate.

Exercised by
`cmd/taxman/validate_test.go::TestRunValidate_DIRTHistoricalRate_Passes`
and
`internal/taxrules/rates_test.go::TestLookup_HistoricalRates_ResolveByEventDate`.
