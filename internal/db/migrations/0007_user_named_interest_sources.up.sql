-- Hand-entered interest credits used to carry a per-bank platform
-- ('n26', 'traderepublic') and a fixed instrument id. DIRT does not
-- turn on which bank paid the interest, so taxman no longer models
-- banks: every hand-entered credit is platform 'manual', and the
-- account is named by the user in free text, stored in instrument.
--
-- The old names are carried across as ordinary user-chosen names, so
-- nothing already logged is lost. platform and instrument both feed
-- the fingerprint, so it is recomputed here (sha256_hex is registered
-- by internal/db; the expression must stay in step with
-- ledger.Transaction.Fingerprint) — otherwise a migrated credit would
-- no longer dedupe against a re-entry of the same credit.

UPDATE transactions
SET instrument = CASE instrument
        WHEN 'N26_SAVINGS' THEN 'N26'
        WHEN 'TRADE_REPUBLIC_SAVINGS' THEN 'Trade Republic'
        ELSE instrument
    END
WHERE platform IN ('n26', 'traderepublic');

UPDATE transactions
SET platform = 'manual',
    fingerprint = sha256_hex(
        'manual' || '|' || type || '|' || date || '|' || instrument
        || '|' || quantity || '|' || price || '|' || currency
    )
WHERE platform IN ('n26', 'traderepublic');
