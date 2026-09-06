package main

import (
	"database/sql"
	"io"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestConn opens dbPath (assumed already created/migrated by
// newTestDBPath) for direct assertions against its contents.
func openTestConn(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// captureStdout runs fn with os.Stdout redirected to a pipe and
// returns everything written to it. Used for commands whose output is
// their primary observable behavior (e.g. runClassify --list-unclassified).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stdout = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe writer: %v", err)
	}
	os.Stdout = orig

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	return string(out)
}
