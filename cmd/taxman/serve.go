package main

import (
	"database/sql"
	"flag"
	"fmt"
	"net"
	"net/http"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/db"
	"github.com/craicoverflow/taxman/internal/web"
)

const defaultPort = "8080"

// defaultHost is the bind address for `taxman serve`. It is loopback
// by design — taxman is a single-user local tool with no auth — so a
// bare `taxman serve` is never exposed on the network. Running inside
// a container (where the port is reached only via the Docker network
// and a reverse proxy) is the case for overriding it with
// `--host 0.0.0.0`.
const defaultHost = "127.0.0.1"

// runServe implements `taxman serve [--host addr] [--port p] [--db path]`.
// It blocks serving HTTP until the listener returns an error (including
// on graceful process shutdown paths not yet wired at this layer).
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	host := fs.String("host", defaultHost, "address to bind (use 0.0.0.0 in a container)")
	port := fs.String("port", defaultPort, "port to listen on")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite database file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	conn, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		return fmt.Errorf("opening database %s: %w", *dbPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := db.Up(conn, db.Migrations); err != nil {
		return fmt.Errorf("ensuring schema is up to date: %w", err)
	}

	addr := net.JoinHostPort(*host, *port)
	fmt.Printf("taxman: serving dashboard on http://%s\n", addr)
	return http.ListenAndServe(addr, web.NewServer(conn).Handler())
}
