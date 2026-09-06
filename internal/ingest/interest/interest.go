// Package interest is the registry of hand-entered interest-credit
// sources — savings accounts with no clean CSV export, where the only
// way a credit reaches the ledger is the dashboard's "Log interest
// payment" form (SPEC.md §7).
//
// Each Source names one such account and knows how to build a
// ledger.Transaction for a credit from it. The web layer renders
// Sources as the form's source picker and dispatches a submission back
// through New; every source is EUR-only, matching
// engine.ComputeDIRT's requirement.
package interest

import (
	"fmt"

	"github.com/craicoverflow/taxman/internal/ingest/n26"
	"github.com/craicoverflow/taxman/internal/ingest/traderepublic"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// Source is one hand-entered interest account.
type Source struct {
	// Key is the stable token used on the wire (the form's `source`
	// field, a hidden field on the edit row) and must never change for
	// an existing source — stored credits are matched back to it.
	Key string
	// Label is the human name shown in the source picker.
	Label string
	// Platform is the ledger.Platform every credit from this source
	// carries; it also scopes ledger's editable/deletable query.
	Platform ledger.Platform

	build func(date, amount, currency string) (ledger.Transaction, error)
}

// sources is the registry, in the order the picker should list them.
// N26 stays first: it was the only source before Trade Republic was
// added, so it remains the default when a submission omits `source`.
var sources = []Source{
	{Key: "n26", Label: "N26", Platform: ledger.PlatformN26, build: n26.NewInterestCredit},
	{Key: "traderepublic", Label: "Trade Republic", Platform: ledger.PlatformTradeRepublic, build: traderepublic.NewInterestCredit},
}

// Default is the source assumed when a request carries no `source`
// value — preserves the pre-Trade-Republic behaviour where every
// logged credit was an N26 one.
var Default = sources[0]

// Sources returns the registered interest sources, in picker order.
func Sources() []Source {
	out := make([]Source, len(sources))
	copy(out, sources)
	return out
}

// SourceByKey looks a source up by its wire key. An empty key resolves
// to Default; an unknown non-empty key is an error, never a silent
// fallback.
func SourceByKey(key string) (Source, error) {
	if key == "" {
		return Default, nil
	}
	for _, s := range sources {
		if s.Key == key {
			return s, nil
		}
	}
	return Source{}, fmt.Errorf("interest: unknown source %q", key)
}

// SourceForPlatform returns the source whose credits carry p, so a
// stored credit can be labelled and its edit form can echo the right
// source key back. ok is false for a platform with no manual-interest
// source.
func SourceForPlatform(p ledger.Platform) (Source, bool) {
	for _, s := range sources {
		if s.Platform == p {
			return s, true
		}
	}
	return Source{}, false
}

// NewCredit builds a ledger.Transaction for one hand-entered interest
// credit from src. date must be YYYY-MM-DD and amount a positive
// decimal string; currency is fixed to EUR (every supported savings
// account pays interest in euro, and engine.ComputeDIRT requires it).
func (src Source) NewCredit(date, amount string) (ledger.Transaction, error) {
	return src.build(date, amount, "EUR")
}
