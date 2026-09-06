CREATE TABLE holding_classifications (
    instrument     TEXT PRIMARY KEY, -- ISIN or platform-specific identifier, matches transactions.instrument
    classification TEXT NOT NULL     -- CGT_ASSET | EXIT_TAX_FUND
);
