package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/ingest"
)

// runBackfill implements `taxman backfill <dir> [--db path]`: ingests
// every file in dir, auto-detecting each one's platform independently
// (mixed-platform directories are expected — that's the point). See
// SPEC.md §3: this is intentionally a distinct command from `import`,
// not a loop calling it, so a one-time historical load reads
// differently in the log from an ongoing "add this month's export"
// operation, even though both ultimately go through the same
// internal/ingest machinery as runImport.
func runBackfill(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxman backfill <dir> [--db path]")
	}

	dir := args[0]

	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite database file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("backfill: reading directory %s: %w", dir, err)
	}

	conn, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		return fmt.Errorf("opening database %s: %w", *dbPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := db.Up(conn, db.Migrations); err != nil {
		return fmt.Errorf("ensuring schema is up to date: %w", err)
	}

	totalInserted, totalSkipped, filesProcessed := 0, 0, 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		platform, txs, warnings, err := ingest.ParseFile(path, "")
		if err != nil {
			return fmt.Errorf("backfill: %w", err)
		}
		printWarnings(path, warnings)

		inserted, skipped, err := ingest.Store(conn, txs)
		if err != nil {
			return fmt.Errorf("backfill: storing transactions from %s: %w", path, err)
		}

		fmt.Printf("backfill: %s (platform: %s): %d new, %d already present\n", path, platform, inserted, skipped)
		totalInserted += inserted
		totalSkipped += skipped
		filesProcessed++
	}

	fmt.Printf("backfill complete: %d file(s) processed, %d new transactions, %d already present\n", filesProcessed, totalInserted, totalSkipped)
	return nil
}
