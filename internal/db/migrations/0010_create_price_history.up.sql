CREATE TABLE price_history (
    symbol     TEXT PRIMARY KEY, -- market symbol as mapped in instrument_tickers
    currency   TEXT NOT NULL,    -- ISO 4217 of the closes (pence listings already scaled to pounds)
    closes     TEXT NOT NULL,    -- JSON array of ["YYYY-MM-DD","<decimal close>"] pairs, ascending
    fetched_at TEXT NOT NULL     -- RFC3339, UTC — when this series last came back from a provider
);
