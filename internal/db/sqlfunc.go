package db

import (
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"fmt"

	sqlite "modernc.org/sqlite"
)

// A data migration that rewrites any of the columns a transaction's
// fingerprint is derived from has to rewrite the fingerprint too, or
// the row stops deduping against a later re-entry of the same event.
// SQLite has no hash function of its own, so this registers one:
// sha256_hex(text) returns the lowercase hex SHA-256 of its argument,
// which is exactly what ledger.Transaction.Fingerprint computes over
// its pipe-joined identifying fields. Migrations are the only intended
// caller.
//
// The driver makes a registered function available to connections
// opened after registration; init runs before any of them, so every
// connection this program opens has it.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("sha256_hex", 1, sha256Hex)
}

func sha256Hex(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("sha256_hex: want 1 argument, got %d", len(args))
	}
	var b []byte
	switch v := args[0].(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	case nil:
		return nil, nil // SQL NULL in, SQL NULL out
	default:
		return nil, fmt.Errorf("sha256_hex: unsupported argument type %T", args[0])
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
