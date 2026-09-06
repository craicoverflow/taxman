package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunMigrate_Up_CreatesSchemaMigrationsTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	if err := runMigrate([]string{"up", "--db", dbPath}); err != nil {
		t.Fatalf("runMigrate up: %v", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var name string
	err = conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&name)
	if err != nil {
		t.Fatalf("expected schema_migrations table to exist, query failed: %v", err)
	}
}

func TestRunMigrate_Down_OnFreshDB_NoError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	if err := runMigrate([]string{"up", "--db", dbPath}); err != nil {
		t.Fatalf("runMigrate up: %v", err)
	}
	if err := runMigrate([]string{"down", "--db", dbPath}); err != nil {
		t.Fatalf("runMigrate down: %v", err)
	}
}

func TestRunMigrate_MissingDirection_ReturnsUsageError(t *testing.T) {
	err := runMigrate(nil)
	if err == nil {
		t.Fatal("expected an error when no direction is given")
	}
	if !strings.Contains(err.Error(), "up") || !strings.Contains(err.Error(), "down") {
		t.Errorf("expected error to mention up/down, got: %v", err)
	}
}

func TestRunMigrate_UnknownDirection_ReturnsError(t *testing.T) {
	err := runMigrate([]string{"sideways", "--db", filepath.Join(t.TempDir(), "test.db")})
	if err == nil {
		t.Fatal("expected an error for an unknown direction")
	}
}
