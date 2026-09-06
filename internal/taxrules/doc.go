// Package taxrules holds Irish tax rates and rules (CGT, exit tax,
// DIRT, s.581, deemed disposal) as versioned data keyed by effective
// date, and never hardcodes a rate as a bare constant outside this
// package.
//
// The scheduled rate for a Kind can be overridden at runtime with an
// environment variable — TAXMAN_CGT_RATE, TAXMAN_EXIT_TAX_RATE, or
// TAXMAN_DIRT_RATE — set to a fraction such as "0.38". An override
// replaces that Kind's rate for every Lookup regardless of event
// date, so use it only when every figure being computed falls under
// the new rate (see rateOverride). This is the package's only I/O.
package taxrules
