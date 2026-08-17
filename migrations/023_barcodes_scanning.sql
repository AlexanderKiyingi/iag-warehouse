-- 023: barcodes and the RF scanning surface.
--
-- Every renowned warehouse system is, underneath the dashboards, an application
-- driven by someone holding a scanner in a cold aisle. This service had no way
-- to turn a scanned string into anything: an item was found by UUID, a bin by
-- its code, and neither is what is printed on the label.
--
-- wh_barcodes is one flat resolution table across every scannable entity, so a
-- terminal can send a string it knows nothing about and be told what it is. Item
-- barcodes carry a uom, because the label on a case and the label on an each are
-- different labels and scanning the case one should credit a case: qty_per_scan
-- is then the base-unit quantity a single scan represents, derived from the
-- wh_item_uoms factor at registration time and stored so a later change to the
-- factor cannot silently re-value historic scans.
--
-- Bins and assets are also registered here rather than resolved by their natural
-- key, because a warehouse relabels bins without renaming them and a barcode
-- must keep pointing at the same physical location when it does.
--
-- picked_set on a pick line is what makes short picks possible. picked_qty alone
-- could not distinguish "nobody has picked this yet" from "the picker went and
-- there were none", since both read zero. Confirm therefore uses qty when
-- picked_set is false — exactly the old all-or-nothing behaviour, so lists
-- created before scanning existed still confirm the way they always did — and
-- uses picked_qty when it is true, releasing the reservation on the shortfall.

CREATE TABLE IF NOT EXISTS wh_barcodes (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    barcode      TEXT NOT NULL UNIQUE,
    entity_type  TEXT NOT NULL CHECK (entity_type IN ('item', 'bin', 'asset', 'handling_unit', 'lot')),
    entity_id    UUID,
    lot_key      TEXT NOT NULL DEFAULT '',
    uom          TEXT NOT NULL DEFAULT '',
    qty_per_scan NUMERIC(18, 6) NOT NULL DEFAULT 1 CHECK (qty_per_scan > 0),
    active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_by   UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT wh_barcodes_entity_ref CHECK (entity_type = 'lot' OR entity_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS wh_barcodes_entity_idx ON wh_barcodes (entity_type, entity_id);

ALTER TABLE wh_pick_lines ADD COLUMN IF NOT EXISTS picked_set BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE wh_pick_lines ADD COLUMN IF NOT EXISTS picked_by UUID;

ALTER TABLE wh_pick_lines ADD COLUMN IF NOT EXISTS picked_at TIMESTAMPTZ;

ALTER TABLE wh_pick_lines ADD COLUMN IF NOT EXISTS short_reason TEXT;
