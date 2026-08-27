-- 030: Bring wh_external_refs up to the platform-wide shape
--
-- wh_external_refs was the first external reference map on the platform and the
-- template for the ones now added to finance, procurement, ERP, CRM, fleet,
-- production and quality control. Those carry two columns this one does not,
-- needed by any source system that keeps running alongside the platform rather
-- than being imported once:
--
--   origin          which side last wrote the row. A relay importing from
--                   another system stamps its own label here, and the relay
--                   running the other direction skips those rows. Without it
--                   two relays echo each other's writes indefinitely.
--   source_version  the source's own monotonic cursor for that record, so a
--                   replayed or out-of-order change can be recognised as stale
--                   and dropped rather than overwriting newer data.
--
-- Both are additive with defaults, so the existing insert in the receipts store
-- keeps working untouched and existing rows read as platform-originated.
--
-- target_id stays UUID here. The newer tables use TEXT because they span
-- services that disagree on key type, but warehouse's own keys are uuid and the
-- column is already in use; retyping it would rewrite the table for no gain.

ALTER TABLE wh_external_refs
    ADD COLUMN IF NOT EXISTS origin         TEXT NOT NULL DEFAULT 'platform',
    ADD COLUMN IF NOT EXISTS source_version BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS synced_at      TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_wh_external_refs_target
    ON wh_external_refs (target_type, target_id);

CREATE INDEX IF NOT EXISTS idx_wh_external_refs_cursor
    ON wh_external_refs (source_service, source_version DESC);
