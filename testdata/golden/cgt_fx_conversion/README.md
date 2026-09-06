# cgt_fx_conversion

Golden fixture for a CGT disposal on a **non-EUR (USD) holding**, where
each transaction is restated in euro at the ECB reference rate for its
own date before FIFO matching (SPEC.md §2's Lot definition; see
`internal/fx` and `internal/engine/currency.go`).

Synthetic scenario, all USD:

| date | event | qty | price (USD) | ECB USD/EUR that day |
|------|-------|-----|-------------|----------------------|
| 2021-01-25 | buy | 10 | 100 | 1.2152 |
| 2022-06-15 | buy | 10 | 120 | 1.0430 |
| 2024-01-02 | sell | 15 | 200 | 1.0956 |

The sell consumes all of lot 1 and half of lot 2 (FIFO). Cost basis is
fixed in euro at each buy's date; proceeds in euro at the sell's date.
The resulting gain lands just above the 2024 annual exemption (€1,270),
so `tax_due` is non-zero at the 2024 CGT rate (33%): €70.107… taxable →
€23.135… → **€23** after rounding the tax down to whole euro (Revenue
convention; see `internal/engine/round.go`).

Expected figures in `expected.json` are engine-generated and pinned
here; the ECB rates above are the real values from
`internal/fx/eurofxref-hist.csv` and act as the fixture's anchor.

Exercised by
`cmd/taxman/validate_test.go::TestRunValidate_CGTFXConversion_Passes`
and, at the unit level, by
`internal/engine/cgt_test.go::TestComputeCGT_NonEURCurrency_ConvertsAtTransactionDateECBRate`.
