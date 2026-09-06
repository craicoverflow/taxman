package db

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

// migration is one versioned schema change: an up script to apply it
// and a down script to reverse it.
type migration struct {
	version int
	name    string
	up      string
	down    string
}

var fileNamePattern = regexp.MustCompile(`^migrations/(\d+)_(.+)\.(up|down)\.sql$`)

// loadMigrations reads every *.up.sql/*.down.sql pair out of fsys and
// returns them sorted by version ascending. It returns an error if an
// up script is missing its matching down script or vice versa.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries := map[int]*migration{}

	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		m := fileNamePattern.FindStringSubmatch(path)
		if m == nil {
			return nil
		}

		version, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			return fmt.Errorf("migration %s: invalid version %q: %w", path, m[1], convErr)
		}

		content, readErr := fs.ReadFile(fsys, path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}

		mig, ok := entries[version]
		if !ok {
			mig = &migration{version: version, name: m[2]}
			entries[version] = mig
		}

		switch m[3] {
		case "up":
			mig.up = string(content)
		case "down":
			mig.down = string(content)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	migrations := make([]migration, 0, len(entries))
	for _, m := range entries {
		if m.up == "" {
			return nil, fmt.Errorf("migration %04d_%s: missing .up.sql", m.version, m.name)
		}
		if m.down == "" {
			return nil, fmt.Errorf("migration %04d_%s: missing .down.sql", m.version, m.name)
		}
		migrations = append(migrations, *m)
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	return migrations, nil
}

// ensureSchemaMigrationsTable creates the version-tracking table if it
// doesn't already exist.
func ensureSchemaMigrationsTable(conn *sql.DB) error {
	_, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name    TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}
	return nil
}

func appliedVersionSet(conn *sql.DB) (map[int]bool, error) {
	rows, err := conn.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("querying schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scanning schema_migrations: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// Up applies every migration in fsys that hasn't already been applied
// to conn, in ascending version order. Re-running Up when everything
// is already applied is a no-op.
func Up(conn *sql.DB, fsys fs.FS) error {
	if err := ensureSchemaMigrationsTable(conn); err != nil {
		return err
	}

	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}

	applied, err := appliedVersionSet(conn)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}

		tx, err := conn.Begin()
		if err != nil {
			return fmt.Errorf("beginning transaction for migration %04d_%s: %w", m.version, m.name, err)
		}

		if _, err := tx.Exec(m.up); err != nil {
			return errors.Join(
				fmt.Errorf("applying migration %04d_%s: %w", m.version, m.name, err),
				tx.Rollback(),
			)
		}

		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			return errors.Join(
				fmt.Errorf("recording migration %04d_%s: %w", m.version, m.name, err),
				tx.Rollback(),
			)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %04d_%s: %w", m.version, m.name, err)
		}
	}

	return nil
}

// Down reverses the single most recently applied migration in fsys.
// If no migrations have been applied, Down is a no-op and returns nil.
func Down(conn *sql.DB, fsys fs.FS) error {
	if err := ensureSchemaMigrationsTable(conn); err != nil {
		return err
	}

	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}

	applied, err := appliedVersionSet(conn)
	if err != nil {
		return err
	}

	var latest *migration
	for i := range migrations {
		if applied[migrations[i].version] {
			if latest == nil || migrations[i].version > latest.version {
				latest = &migrations[i]
			}
		}
	}

	if latest == nil {
		return nil // nothing applied, nothing to reverse
	}

	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction to reverse migration %04d_%s: %w", latest.version, latest.name, err)
	}

	if _, err := tx.Exec(latest.down); err != nil {
		return errors.Join(
			fmt.Errorf("reversing migration %04d_%s: %w", latest.version, latest.name, err),
			tx.Rollback(),
		)
	}

	if _, err := tx.Exec(`DELETE FROM schema_migrations WHERE version = ?`, latest.version); err != nil {
		return errors.Join(
			fmt.Errorf("unrecording migration %04d_%s: %w", latest.version, latest.name, err),
			tx.Rollback(),
		)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing reversal of migration %04d_%s: %w", latest.version, latest.name, err)
	}

	return nil
}
