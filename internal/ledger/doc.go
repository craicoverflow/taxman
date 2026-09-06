// Package ledger implements the append-only transaction store: the
// Transaction and Fingerprint types, and idempotent-by-fingerprint
// storage backed by SQLite.
//
// The one correction path is for hand-entered interest credits
// (ManualInterestCredits / UpdateManualInterestCredit /
// DeleteManualInterestCredit): those are typed in on the dashboard
// for savings accounts with no clean CSV export, not parsed from a
// file, so a typo in the date, amount, or account name has no other
// way to be fixed. Every imported transaction stays immutable
// (SPEC.md §2).
package ledger
