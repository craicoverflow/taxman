# Migrations

Each migration is a pair of files named `NNNN_description.up.sql` and
`NNNN_description.down.sql`, where `NNNN` is a zero-padded, strictly
increasing version number (`0001`, `0002`, ...). `up` files are applied
in ascending version order; `down` reverses the most recently applied
migration.

No domain migrations exist yet — this directory currently only tracks
the mechanism itself (see `internal/db/migrate.go`). The first real
migration (`0001_create_transactions.sql`) lands with the ledger
package in Phase 1.
