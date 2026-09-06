-- Reverses 0007 for the two source names it created. A credit logged
-- since, under a name the user chose, has no pre-0007 equivalent —
-- there was no platform to put it under — so it is deliberately left
-- as 'manual' rather than being forced into a bank it never came from.

UPDATE transactions
SET platform = CASE instrument
        WHEN 'N26' THEN 'n26'
        ELSE 'traderepublic'
    END,
    instrument = CASE instrument
        WHEN 'N26' THEN 'N26_SAVINGS'
        ELSE 'TRADE_REPUBLIC_SAVINGS'
    END,
    fingerprint = sha256_hex(
        CASE instrument WHEN 'N26' THEN 'n26' ELSE 'traderepublic' END
        || '|' || type || '|' || date || '|'
        || CASE instrument WHEN 'N26' THEN 'N26_SAVINGS' ELSE 'TRADE_REPUBLIC_SAVINGS' END
        || '|' || quantity || '|' || price || '|' || currency
    )
WHERE platform = 'manual' AND type = 'interest'
  AND instrument IN ('N26', 'Trade Republic');
