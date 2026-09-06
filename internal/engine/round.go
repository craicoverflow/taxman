package engine

import "github.com/shopspring/decimal"

// roundTaxToWholeEuro rounds a final tax-due figure down to whole euro.
//
// Revenue's CGT/income-tax returns (Form 11, Form CG1) are completed in
// whole euro, and the long-standing convention for a self-assessed
// liability is to round the tax DOWN to the euro (cents in the
// taxpayer's favour). This is applied ONLY to the final tax figure —
// gains, proceeds, cost bases, the annual exemption and loss pools all
// stay at full decimal precision so that intermediate arithmetic never
// accumulates rounding error.
//
// The input is expected to be non-negative (every Compute* path floors
// its taxable amount at zero before multiplying by the rate); Floor on
// a non-negative value is a plain truncation of the cents.
func roundTaxToWholeEuro(taxDue decimal.Decimal) decimal.Decimal {
	return taxDue.Floor()
}
