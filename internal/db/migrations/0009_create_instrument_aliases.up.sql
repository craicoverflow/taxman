CREATE TABLE instrument_aliases (
    alias      TEXT PRIMARY KEY, -- an instrument key as stored in transactions.instrument
    canonical  TEXT NOT NULL,    -- the instrument key it is read as; never itself an alias
    created_at TEXT NOT NULL,    -- RFC3339, UTC
    CHECK (alias <> canonical)
);
