CREATE TABLE transactions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    fingerprint TEXT NOT NULL UNIQUE,
    platform    TEXT NOT NULL,
    type        TEXT NOT NULL,
    date        TEXT NOT NULL, -- RFC3339, UTC
    instrument  TEXT NOT NULL, -- ISIN or platform-specific identifier
    quantity    TEXT NOT NULL, -- decimal, stored as text for exactness
    price       TEXT NOT NULL, -- decimal, stored as text for exactness
    currency    TEXT NOT NULL, -- ISO 4217
    source_ref  TEXT NOT NULL DEFAULT '' -- non-identifying: broker's own reference, for traceability only
);

CREATE INDEX idx_transactions_instrument ON transactions(instrument);
