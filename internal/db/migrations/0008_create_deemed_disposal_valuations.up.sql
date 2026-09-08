-- The market value of a fund holding at an 8-year deemed-disposal
-- anniversary. User-entered, never inferred: `internal/prices` is a
-- live-quote client walled off from `internal/engine` (SPEC.md §4, §9)
-- and cannot supply a historical anniversary value, so the engine
-- refuses to compute a deemed disposal until a value is on record here
-- — the same "flag, don't guess" stance as holding_classifications.
--
-- The gain on the deemed disposal is this value less the lot's original
-- cost of acquisition (TCA 1997 s.747E(6); Revenue TDM Part 27-04-01
-- §4.1.5).
CREATE TABLE deemed_disposal_valuations (
    instrument     TEXT NOT NULL, -- ISIN or platform-specific identifier, matches transactions.instrument
    valuation_date TEXT NOT NULL, -- RFC3339, UTC — the anniversary date the value was taken at
    value_per_unit TEXT NOT NULL, -- decimal string, in currency (never float — see internal/ledger)
    currency       TEXT NOT NULL, -- ISO 4217; restated to EUR at this date's ECB rate by internal/fx
    created_at     TEXT NOT NULL, -- RFC3339, UTC
    PRIMARY KEY (instrument, valuation_date)
);
