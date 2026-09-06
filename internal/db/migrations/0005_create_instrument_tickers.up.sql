CREATE TABLE instrument_tickers (
    instrument TEXT PRIMARY KEY, -- ISIN or platform-specific identifier, matches transactions.instrument
    symbol     TEXT NOT NULL,    -- market symbol a price provider understands (e.g. AAPL, VWRL.L)
    created_at TEXT NOT NULL     -- RFC3339, UTC
);
