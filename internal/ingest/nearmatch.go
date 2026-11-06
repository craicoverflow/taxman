package ingest

import (
	"database/sql"
	"fmt"

	"github.com/craicoverflow/taxman/internal/aliases"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// NearMatches reports imported buys and sells that look like a trade
// the user already logged by hand (SPEC.md §18), one warning per
// imported row.
//
// A hand-entered trade is stored on ledger.PlatformManual, and the
// fingerprint includes the platform, so it can never exact-dedupe
// against the same trade arriving later in a broker export: both would
// be counted. This heuristic is the only protection. A row matches when
// its alias-resolved instrument, type, date and quantity equal a manual
// trade's. Price is deliberately ignored — an identical trade is the
// commonest duplicate, and a differing price is the usual way a retyped
// one drifts from the export.
//
// aliases maps an alias key to its canonical key (SPEC.md §17); both
// sides are resolved through it. The warning is advisory: nothing is
// merged, skipped or deleted. It is a heuristic, not a guarantee — a
// manual trade typed under a ticker will not match an export that keys
// by ISIN unless the user has merged the two.
func NearMatches(manual, imported []ledger.Transaction, aliases map[string]string) []string {
	resolve := func(key string) string {
		if canonical, ok := aliases[key]; ok {
			return canonical
		}
		return key
	}
	isTrade := func(tx ledger.Transaction) bool {
		return tx.Type == ledger.TypeBuy || tx.Type == ledger.TypeSell
	}

	var warnings []string
	for _, imp := range imported {
		if imp.Platform == ledger.PlatformManual || !isTrade(imp) {
			continue
		}
		for _, man := range manual {
			if man.Platform != ledger.PlatformManual || !isTrade(man) {
				continue
			}
			if man.Type == imp.Type &&
				resolve(man.Instrument) == resolve(imp.Instrument) &&
				man.Date.UTC().Format("2006-01-02") == imp.Date.UTC().Format("2006-01-02") &&
				man.Quantity.Equal(imp.Quantity) {
				warnings = append(warnings, fmt.Sprintf(
					"imported %s %s of %s on %s at %s %s (%s) matches a trade you logged by hand (manual, at %s %s): "+
						"if it is the same trade, delete the manual entry on the dashboard so it is not counted twice",
					imp.Platform, imp.Type, imp.Quantity, imp.Date.UTC().Format("2006-01-02"), imp.Price, imp.Currency,
					resolve(imp.Instrument), man.Price, man.Currency))
				break // one warning per imported row
			}
		}
	}
	return warnings
}

// NearMatchWarnings is NearMatches against the hand-entered trades and
// merges already in conn. Shared by `taxman import`, `taxman backfill`
// and the web upload handler so all three warn identically.
func NearMatchWarnings(conn *sql.DB, imported []ledger.Transaction) ([]string, error) {
	stored, err := ledger.NewStore(conn).ManualTrades()
	if err != nil {
		return nil, fmt.Errorf("loading manual trades: %w", err)
	}
	if len(stored) == 0 {
		return nil, nil
	}
	manual := make([]ledger.Transaction, 0, len(stored))
	for _, tr := range stored {
		manual = append(manual, tr.Transaction)
	}
	aliasMap, err := aliases.NewStore(conn).All()
	if err != nil {
		return nil, fmt.Errorf("loading aliases: %w", err)
	}
	return NearMatches(manual, imported, aliasMap), nil
}
