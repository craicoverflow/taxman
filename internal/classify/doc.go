// Package classify tracks each Holding's tax classification (CGT asset
// vs. exit-tax fund) via a manual override table. It never guesses: an
// unrecognized holding stays Unclassified until explicitly set.
package classify
