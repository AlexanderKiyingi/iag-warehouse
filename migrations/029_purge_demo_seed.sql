-- Purge the demo items seeded by 002_seed.sql.
--
-- Deliberately preserved:
--   * wh_facilities / wh_zones / wh_bins — the site topology is treated as
--     configuration an operator adjusts, not demo data.
--   * RM-FIELD-SACK (009_field_intake_sack.sql) — a functional SKU the field
--     intake capture flow writes against, not part of the demo catalogue.
--
-- wh_items has roughly twenty dependent tables (stock balances, movements, picks,
-- receipts, counts, transfers, handling units, …). Rather than enumerate every
-- foreign key, each row is deleted in its own subtransaction and skipped if it is
-- still referenced: an item an operator has since transacted against survives and
-- the migration cannot fail on a foreign key.

-- The migration runner already wraps every file in a single transaction holding an
-- advisory lock, so this file must not open one of its own: a COMMIT here would end
-- that outer transaction early and release the lock mid-run.

-- wh_spare_compat is pure demo mapping and cascades from nothing else.
DELETE FROM wh_spare_compat
WHERE item_id IN (SELECT id FROM wh_items WHERE sku = 'SP-FILTER-01');

DO $$
DECLARE
    demo_sku TEXT;
    kept     INT := 0;
BEGIN
    FOREACH demo_sku IN ARRAY ARRAY[
        'RM-GREEN-001',   -- Arabica Green Coffee
        'FG-ROAST-250',   -- Roasted Coffee 250g
        'CON-DIESEL',     -- Diesel Fuel (no code references this SKU)
        'SP-FILTER-01',   -- Oil Filter HF-204
        'EQ-ROASTER-01'   -- Probat Roaster Unit
    ]
    LOOP
        BEGIN
            DELETE FROM wh_items WHERE sku = demo_sku;
        EXCEPTION WHEN foreign_key_violation THEN
            kept := kept + 1;
            RAISE NOTICE 'wh_items % is still referenced by live stock — kept', demo_sku;
        END;
    END LOOP;
    IF kept > 0 THEN
        RAISE NOTICE 'purge: % demo item(s) retained because live rows reference them', kept;
    END IF;
END $$;
