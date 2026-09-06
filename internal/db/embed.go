package db

import "embed"

// Migrations is the embedded set of *.up.sql/*.down.sql pairs applied
// by Up and reversed by Down. See internal/db/migrations/README.md for
// the filename convention.
//
//go:embed migrations
var Migrations embed.FS
