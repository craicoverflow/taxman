CREATE TABLE price_quotes (
    symbol     TEXT PRIMARY KEY, -- market symbol as mapped in instrument_tickers
    price      TEXT NOT NULL,    -- decimal string, in the instrument's native currency
    currency   TEXT NOT NULL,    -- ISO 4217, or '' when the source reported a price but no currency
    fetched_at TEXT NOT NULL     -- RFC3339, UTC — when this price last came back from a provider
);
