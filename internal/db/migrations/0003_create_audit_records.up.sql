CREATE TABLE audit_records (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    instrument           TEXT NOT NULL,
    kind                 TEXT NOT NULL, -- cgt | exit_tax | dirt
    liability_amount     TEXT NOT NULL, -- decimal, stored as text for exactness
    source_transactions  TEXT NOT NULL, -- JSON array of transaction fingerprints
    lot_ids              TEXT NOT NULL, -- JSON array of lot identifiers
    disposal_date        TEXT NOT NULL, -- RFC3339, UTC
    rule_effective_from  TEXT NOT NULL, -- RFC3339, UTC
    rule_rate            TEXT NOT NULL  -- decimal, stored as text for exactness
);

CREATE INDEX idx_audit_records_instrument ON audit_records(instrument);
