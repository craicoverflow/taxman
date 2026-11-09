package ingest

import (
	"database/sql"
	"fmt"

	"github.com/craicoverflow/taxman/internal/aliases"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// NearMatches reports, as warnings, each parsed buy or sell that looks
// like a trade already entered by hand. The ledger fingerprint includes
// the platform, so an imported row can never exact-dedupe against a
// ledger.PlatformManual one; without this the same trade would be
// counted twice (SPEC.md §18).
//
// A parsed row matches a manual trade on instrument, type, date and
// quantity. Price is deliberately ignored — the typed and the booked
// price often differ, and an identical trade at a different price is
// exactly the double-count to catch. Both slices must already carry
// alias-resolved instruments (NearMatchWarnings arranges that).
//
// It is a heuristic, not a guarantee, and warns only: nothing is merged,
// skipped or deleted.
func NearMatches(manual, parsed []ledger.Transaction) []string {
	if len(manual) == 0 {
		return nil
	}
	type key struct{ instrument, typ, date, qty string }
	keyOf := func(tx ledger.Transaction) key {
		return key{tx.Instrument, string(tx.Type), tx.Date.UTC().Format("2006-01-02"), tx.Quantity.String()}
	}
	held := map[key]bool{}
	for _, tx := range manual {
		held[keyOf(tx)] = true
	}

	var out []string
	for _, tx := range parsed {
		if tx.Type != ledger.TypeBuy && tx.Type != ledger.TypeSell {
			continue
		}
		if held[keyOf(tx)] {
			out = append(out, fmt.Sprintf(
				"%s %s of %s on %s matches a trade you entered by hand (same instrument, type, date and quantity; price is ignored). "+
					"If it is the same trade, delete your manual entry on the dashboard so it is not counted twice.",
				tx.Platform, tx.Type, tx.Instrument, tx.Date.UTC().Format("2006-01-02")))
		}
	}
	return out
}

// NearMatchWarnings loads the hand-entered trades and the instrument
// aliases from conn, resolves both sides through the aliases, and
// returns NearMatches(parsed). It only reads.
func NearMatchWarnings(conn *sql.DB, parsed []ledger.Transaction) ([]string, error) {
	stored, err := ledger.NewStore(conn).ManualTrades()
	if err != nil {
		return nil, fmt.Errorf("loading manual trades: %w", err)
	}
	if len(stored) == 0 {
		return nil, nil
	}
	aliasMap, err := aliases.NewStore(conn).All()
	if err != nil {
		return nil, fmt.Errorf("loading aliases: %w", err)
	}
	resolve := func(tx ledger.Transaction) ledger.Transaction {
		if canonical, ok := aliasMap[tx.Instrument]; ok {
			tx.Instrument = canonical
		}
		return tx
	}
	manual := make([]ledger.Transaction, 0, len(stored))
	for _, st := range stored {
		manual = append(manual, resolve(st.Transaction))
	}
	resolved := make([]ledger.Transaction, 0, len(parsed))
	for _, tx := range parsed {
		resolved = append(resolved, resolve(tx))
	}
	return NearMatches(manual, resolved), nil
}
