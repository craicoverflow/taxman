package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/ingest"
	"github.com/craicoverflow/taxman/internal/ledger"
)

// runImport implements `taxman import <file> [--platform p] [--db path] [--dry-run]`.
func runImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxman import <file> [--platform degiro|ibkr|etrade] [--db path] [--dry-run]")
	}

	file := args[0]

	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	platform := fs.String("platform", "", "source platform (auto-detected from the file header if omitted)")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite database file")
	dryRun := fs.Bool("dry-run", false, "report what would be imported without writing")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	resolvedPlatform, txs, warnings, err := ingest.ParseFile(file, *platform)
	if err != nil {
		return err
	}
	printWarnings(file, warnings)

	if *dryRun {
		printWarnings(file, dryRunNearMatches(*dbPath, txs))
		fmt.Printf("dry run: would import %d transaction(s) from %s (platform: %s)\n", len(txs), file, resolvedPlatform)
		return nil
	}

	conn, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		return fmt.Errorf("opening database %s: %w", *dbPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := db.Up(conn, db.Migrations); err != nil {
		return fmt.Errorf("ensuring schema is up to date: %w", err)
	}

	nearMatches, err := ingest.NearMatchWarnings(conn, txs)
	if err != nil {
		return fmt.Errorf("checking %s against manual trades: %w", file, err)
	}
	printWarnings(file, nearMatches)

	inserted, skipped, err := ingest.Store(conn, txs)
	if err != nil {
		return fmt.Errorf("storing transactions from %s: %w", file, err)
	}

	fmt.Printf("imported %s (platform: %s): %d new, %d already present\n", file, resolvedPlatform, inserted, skipped)
	return nil
}

// dryRunNearMatches checks txs against the hand-entered trades already
// in the database at dbPath without writing anything: a dry run must
// not create the file or migrate it. A database that doesn't exist yet
// has no manual trades to match; a check that can't run is reported as
// a warning rather than failing a dry run.
func dryRunNearMatches(dbPath string, txs []ledger.Transaction) []string {
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return []string{fmt.Sprintf("could not check for matching manual trades: %v", err)}
	}
	defer func() { _ = conn.Close() }()

	warnings, err := ingest.NearMatchWarnings(conn, txs)
	if err != nil {
		return []string{fmt.Sprintf("could not check for matching manual trades: %v", err)}
	}
	return warnings
}

// printWarnings reports non-fatal warnings from parsing file (e.g.
// corporate-action rows skipped rather than imported — see
// internal/ingest/degiro/account.go) without stopping the import.
// Shared by runImport and runBackfill.
func printWarnings(file string, warnings []string) {
	for _, w := range warnings {
		fmt.Printf("warning: %s: %s\n", file, w)
	}
}
