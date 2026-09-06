package taxrules

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Kind identifies which tax regime a Rate applies to.
type Kind string

const (
	KindCGT     Kind = "cgt"
	KindExitTax Kind = "exit_tax"
	KindDIRT    Kind = "dirt"
)

// Rate is the set of parameters in effect for a Kind as of a
// particular effective date. Not every field is meaningful for every
// Kind — e.g. AnnualExemption only applies to KindCGT — callers should
// only read the fields relevant to the Kind they looked up.
type Rate struct {
	Rate            decimal.Decimal // e.g. 0.33 for 33%
	AnnualExemption decimal.Decimal // CGT only; zero value for other kinds
	EffectiveFrom   time.Time       // the schedule entry's own effective date, for audit trails (SPEC.md §2's RuleVersion)
}

// ruleVersion is one entry in a Kind's effective-dated rate schedule:
// Rate applies to any event on or after EffectiveFrom, until
// superseded by the next later entry.
type ruleVersion struct {
	effectiveFrom time.Time
	rate          Rate
}

// schedules holds every Kind's rate history, each sorted ascending by
// effective date. New rates are added here, never as bare constants
// elsewhere in the codebase.
//
// HISTORICAL ENTRIES ARE UNVERIFIED. The entries below marked
// "UNVERIFIED" were reconstructed from memory of Irish Budget / Finance
// Act changes to give `taxman backfill` a defensible rate for a
// historical year rather than silently taxing every past year at the
// current rate. Before relying on any figure for a tax year earlier
// than the current one, confirm the applicable rate and its effective
// date against the relevant Revenue Tax and Duty Manual / Finance Act.
// The entries NOT marked UNVERIFIED (CGT 33% from 2012-12-06, DIRT
// 41%→33% over 2014–2020, exit tax 36%/41% from 2013/2014 and the
// 41%→38% cut from 2026-01-01 under Finance Act 2025) are the ones held
// with confidence.
//
// effDate is a small helper to keep the table readable.
var schedules = map[Kind][]ruleVersion{
	KindCGT: {
		// The €1,270 annual exemption has been unchanged since the
		// 2002 euro conversion of the former IEP 1,000, so every entry
		// carries the same AnnualExemption.
		{effectiveFrom: effDate(1900, 1, 1), rate: cgtRate("0.20")},   // UNVERIFIED: post-1998 CGT rate, pre-2008
		{effectiveFrom: effDate(2008, 10, 15), rate: cgtRate("0.22")}, // UNVERIFIED: Budget 2009 (announced 14 Oct 2008)
		{effectiveFrom: effDate(2009, 4, 8), rate: cgtRate("0.25")},   // UNVERIFIED: Supplementary Budget 2009
		{effectiveFrom: effDate(2011, 12, 7), rate: cgtRate("0.30")},  // UNVERIFIED: Budget 2012, disposals from 7 Dec 2011
		{effectiveFrom: effDate(2012, 12, 6), rate: cgtRate("0.33")},  // Budget 2013, disposals from 6 Dec 2012 — current
	},
	KindDIRT: {
		{effectiveFrom: effDate(1900, 1, 1), rate: plainRate("0.20")},  // UNVERIFIED: DIRT standard rate through 2008
		{effectiveFrom: effDate(2009, 1, 1), rate: plainRate("0.25")},  // UNVERIFIED: raised in the 2009 Budgets
		{effectiveFrom: effDate(2011, 1, 1), rate: plainRate("0.27")},  // UNVERIFIED
		{effectiveFrom: effDate(2012, 1, 1), rate: plainRate("0.30")},  // UNVERIFIED
		{effectiveFrom: effDate(2013, 1, 1), rate: plainRate("0.33")},  // UNVERIFIED
		{effectiveFrom: effDate(2014, 1, 1), rate: plainRate("0.41")},  // 2014–2016
		{effectiveFrom: effDate(2017, 1, 1), rate: plainRate("0.39")},  // Finance Act 2016: 2%/yr step-down begins
		{effectiveFrom: effDate(2018, 1, 1), rate: plainRate("0.37")},
		{effectiveFrom: effDate(2019, 1, 1), rate: plainRate("0.35")},
		{effectiveFrom: effDate(2020, 1, 1), rate: plainRate("0.33")}, // current
	},
	KindExitTax: {
		{effectiveFrom: effDate(1900, 1, 1), rate: plainRate("0.23")},  // UNVERIFIED: fund exit tax, pre-2009
		{effectiveFrom: effDate(2009, 1, 1), rate: plainRate("0.26")},  // UNVERIFIED
		{effectiveFrom: effDate(2009, 4, 8), rate: plainRate("0.28")},  // UNVERIFIED: Supplementary Budget 2009
		{effectiveFrom: effDate(2011, 1, 1), rate: plainRate("0.30")},  // UNVERIFIED
		{effectiveFrom: effDate(2012, 1, 1), rate: plainRate("0.33")},  // UNVERIFIED
		{effectiveFrom: effDate(2013, 1, 1), rate: plainRate("0.36")},  // Finance Act 2012
		{effectiveFrom: effDate(2014, 1, 1), rate: plainRate("0.41")},  // Finance Act 2013 — pre-2026 rate
		{
			// Finance Act 2025: exit tax cut from 41% to 38% for
			// chargeable events (including deemed disposals) on or
			// after 1 January 2026.
			effectiveFrom: effDate(2026, 1, 1),
			rate:          plainRate("0.38"),
		},
	},
}

// effDate is a terse UTC midnight constructor for the schedule table.
func effDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// plainRate builds a Rate with only a percentage (DIRT, exit tax).
func plainRate(r string) Rate {
	return Rate{Rate: decimal.RequireFromString(r)}
}

// cgtRate builds a Rate carrying the constant €1,270 CGT annual exemption.
func cgtRate(r string) Rate {
	return Rate{
		Rate:            decimal.RequireFromString(r),
		AnnualExemption: decimal.RequireFromString("1270"),
	}
}

// Lookup returns the Rate in effect for kind on date. It resolves by
// the given date, not by when Lookup is called — a disposal dated
// 2025-12-31 always resolves to the pre-transition rate, regardless of
// when the computation runs. If kind is unrecognized, or if date falls
// before any defined rate for kind, Lookup returns an error and a
// zero-value Rate — it never silently returns a default or
// zero-as-if-valid rate.
func Lookup(kind Kind, date time.Time) (Rate, error) {
	schedule, ok := schedules[kind]
	if !ok {
		return Rate{}, fmt.Errorf("taxrules: unknown kind %q", kind)
	}

	date = date.UTC()

	var best *ruleVersion
	for i := range schedule {
		v := &schedule[i]
		if v.effectiveFrom.After(date) {
			continue
		}
		if best == nil || v.effectiveFrom.After(best.effectiveFrom) {
			best = v
		}
	}

	if best == nil {
		return Rate{}, fmt.Errorf("taxrules: no %s rate defined for %s", kind, date.Format("2006-01-02"))
	}

	rate := best.rate
	rate.EffectiveFrom = best.effectiveFrom

	// An env-var override, when set, replaces the scheduled rate for
	// this Kind — used to correct a rate ahead of a code update
	// without a recompile. It does not touch AnnualExemption or
	// EffectiveFrom: the schedule still supplies those, and
	// EffectiveFrom stays pointed at the schedule entry so an audit
	// trail shows which entry was overridden.
	if override, ok, err := rateOverride(kind); err != nil {
		return Rate{}, err
	} else if ok {
		rate.Rate = override
	}

	return rate, nil
}

// rateOverrideEnv names the environment variable that overrides each
// Kind's scheduled rate. Absent from the map means a Kind cannot be
// overridden this way.
var rateOverrideEnv = map[Kind]string{
	KindCGT:     "TAXMAN_CGT_RATE",
	KindExitTax: "TAXMAN_EXIT_TAX_RATE",
	KindDIRT:    "TAXMAN_DIRT_RATE",
}

// rateOverride reads the env-var override for kind, if any. It
// returns ok=false when the variable is unset or empty. A value that
// isn't a decimal, or that falls outside [0, 1], is a hard error
// rather than a silently ignored misconfiguration — consistent with
// Lookup never returning a rate it isn't sure of. The fraction form
// is deliberate: "0.38", not "38".
func rateOverride(kind Kind) (decimal.Decimal, bool, error) {
	env, ok := rateOverrideEnv[kind]
	if !ok {
		return decimal.Zero, false, nil
	}
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		return decimal.Zero, false, nil
	}

	r, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, false, fmt.Errorf("taxrules: %s=%q is not a valid decimal rate (expected a fraction like 0.38)", env, raw)
	}
	if r.IsNegative() || r.GreaterThan(decimal.NewFromInt(1)) {
		return decimal.Zero, false, fmt.Errorf("taxrules: %s=%q is out of range (expected a fraction between 0 and 1, e.g. 0.38 for 38%%)", env, raw)
	}
	return r, true, nil
}
