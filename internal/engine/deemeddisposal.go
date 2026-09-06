package engine

import "time"

// deemedDisposalCycleYears is the interval at which Irish tax law
// deems a fund lot disposed (and, once verified, taxed — see task
// 4.9) even if it hasn't actually been sold: 8 years, and every 8
// years thereafter for as long as the lot is held.
const deemedDisposalCycleYears = 8

// NextDeemedDisposalAnniversary returns the next date on or after
// asOf at which lot crosses an 8-year deemed-disposal boundary
// (acquisition + 8, +16, +24, ... years). If asOf is exactly on an
// anniversary, that anniversary is treated as already reached and the
// following cycle is returned.
//
// This is pure date arithmetic ONLY, deliberately split from
// liability computation per tasks/plan.md task 4.7: it takes no
// Classifier, performs no internal/taxrules lookup, and returns no
// liability figure or audit record. Computing what's actually owed at
// a reached anniversary is task 4.9, blocked pending a Revenue TDM
// citation on the credit/refund mechanics — see SPEC.md §6.
func NextDeemedDisposalAnniversary(lot Lot, asOf time.Time) time.Time {
	next := lot.AcquiredDate.AddDate(deemedDisposalCycleYears, 0, 0)
	for !next.After(asOf) {
		next = next.AddDate(deemedDisposalCycleYears, 0, 0)
	}
	return next
}
