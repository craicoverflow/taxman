package main

import (
	"database/sql"
	"flag"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
)

const defaultDBPath = "taxman.db"

// runMigrate implements `taxman migrate up|down [--db path]`.
func runMigrate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxman migrate up|down [--db path]")
	}

	direction := args[0]
	if direction != "up" && direction != "down" {
		return fmt.Errorf("unknown migrate direction %q (expected up or down)", direction)
	}

	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite database file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	conn, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		return fmt.Errorf("opening database %s: %w", *dbPath, err)
	}
	defer func() { _ = conn.Close() }()

	switch direction {
	case "up":
		return db.Up(conn, db.Migrations)
	case "down":
		return db.Down(conn, db.Migrations)
	}
	return nil // unreachable: direction validated above
}
