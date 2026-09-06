package main

import (
	"database/sql"
	"flag"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/ingest"
)

// runImport implements `taxman import <file> [--platform p] [--db path] [--dry-run]`.
func runImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxman import <file> [--platform degiro|ibkr|etrade|n26] [--db path] [--dry-run]")
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

	inserted, skipped, err := ingest.Store(conn, txs)
	if err != nil {
		return fmt.Errorf("storing transactions from %s: %w", file, err)
	}

	fmt.Printf("imported %s (platform: %s): %d new, %d already present\n", file, resolvedPlatform, inserted, skipped)
	return nil
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
