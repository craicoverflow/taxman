// Package ingest routes a source file/upload to the correct
// platform-specific parser, auto-detecting the platform from its CSV
// header when one isn't given explicitly. It refuses to guess: an
// unrecognized header is an error, never a silent default. See
// SPEC.md §3.
//
// This is the logic shared by `taxman import`, `taxman backfill`, and
// the web dashboard's upload form — each drives it from a different
// source (a file path, a directory walk, an HTTP multipart upload)
// but all three must dedupe/detect/parse identically.
package ingest

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"os"

	"github.com/craicoverflow/taxman/internal/ingest/degiro"
	"github.com/craicoverflow/taxman/internal/ingest/etrade"
	"github.com/craicoverflow/taxman/internal/ingest/ibkr"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// Platforms lists the platforms with an automatic parser, in
// detection-attempt order. Hand-entered interest credits have no
// entry here by design: no bank is modelled, and a credit reaches the
// ledger only through the dashboard (internal/ingest/interest), so it
// never appears in a header auto-detection or an upload/import
// platform picker.
var Platforms = []string{"degiro", "ibkr", "etrade"}

var detectors = map[string]func(header []string) bool{
	"degiro": degiro.Detect,
	"ibkr":   ibkr.Detect,
	"etrade": etrade.Detect,
}

// parseFunc is the shape every platform's Parse function is adapted
// to: transactions, plus any non-fatal warnings about rows that were
// skipped rather than imported (degiro.Parse for corporate-action
// rows — see internal/ingest/degiro/account.go — and ibkr.Parse for
// cash/FX rows in a flat export). etrade is wrapped below since it
// has nothing to warn about yet.
type parseFunc func(io.Reader) ([]ledger.Transaction, []string, error)

var parsers = map[string]parseFunc{
	"degiro": degiro.Parse,
	"ibkr":   ibkr.Parse,
	"etrade": withNoWarnings(etrade.Parse),
}

// withNoWarnings adapts a platform parser that has no concept of
// warnings yet to parseFunc's shape.
func withNoWarnings(parse func(io.Reader) ([]ledger.Transaction, error)) parseFunc {
	return func(r io.Reader) ([]ledger.Transaction, []string, error) {
		txs, err := parse(r)
		return txs, nil, err
	}
}

// ParseFile opens path and delegates to ParseReader.
func ParseFile(path, platform string) (resolvedPlatform string, txs []ledger.Transaction, warnings []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	return ParseReader(f, path, platform)
}

// ParseReader resolves r's platform (using platform if non-empty,
// otherwise auto-detecting from its CSV header) and parses it into
// transactions, plus any non-fatal warnings about rows that were
// skipped rather than imported. source is used only for error
// messages (a file path or an uploaded filename). r is read into
// memory in full, since detection and parsing each need their own
// pass over the header.
func ParseReader(r io.Reader, source, platform string) (resolvedPlatform string, txs []ledger.Transaction, warnings []string, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, nil, fmt.Errorf("reading %s: %w", source, err)
	}

	resolvedPlatform = platform
	if resolvedPlatform == "" {
		resolvedPlatform, err = detectPlatform(data, source)
		if err != nil {
			return "", nil, nil, err
		}
	}

	parse, ok := parsers[resolvedPlatform]
	if !ok {
		return "", nil, nil, fmt.Errorf("import: unsupported platform %q", resolvedPlatform)
	}

	txs, warnings, err = parse(bytes.NewReader(data))
	if err != nil {
		return "", nil, nil, fmt.Errorf("import: parsing %s as %s: %w", source, resolvedPlatform, err)
	}

	return resolvedPlatform, txs, warnings, nil
}

// detectPlatform reads just data's header row and tries each
// registered detector against it, in Platforms order. It refuses to
// guess: if no detector matches, it returns an error rather than
// defaulting to any particular platform.
func detectPlatform(data []byte, source string) (string, error) {
	header, err := csv.NewReader(bytes.NewReader(data)).Read()
	if err != nil {
		return "", fmt.Errorf("reading header of %s for platform detection: %w", source, err)
	}

	for _, name := range Platforms {
		if detectors[name](header) {
			return name, nil
		}
	}

	return "", fmt.Errorf("import: could not auto-detect platform for %s; pass --platform explicitly", source)
}

// Store inserts every transaction in txs via a ledger.Store over
// conn, returning counts of newly inserted vs. already-present rows.
// Shared by `taxman import`, `taxman backfill`, and the web upload
// handler so all three report identical new/already-present counts.
func Store(conn *sql.DB, txs []ledger.Transaction) (inserted, skipped int, err error) {
	store := ledger.NewStore(conn)
	for _, tx := range txs {
		wasInserted, err := store.Insert(tx)
		if err != nil {
			return inserted, skipped, err
		}
		if wasInserted {
			inserted++
		} else {
			skipped++
		}
	}
	return inserted, skipped, nil
}
