// Package engine computes tax liability from the ledger: FIFO (and,
// once verified, s.581) lot matching for CGT assets, deemed-disposal
// tracking and liability for exit-tax funds, and DIRT on interest. It
// never guesses an unverified rule — see internal/taxrules and SPEC.md
// §6 for the "ask first" boundary this package must respect.
package engine
