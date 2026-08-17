-- 018: unit-of-measure conversion hierarchy.
--
-- wh_items.uom is the item's BASE unit — the unit every balance, movement and
-- valuation figure is expressed in. Real traffic rarely runs in the base unit:
-- coffee arrives by the 60kg bag and is issued by the kilo, spares arrive by the
-- case and are issued by the each. Until now `uom` on a document line was a free
-- text label that nothing acted on, so a line entered as 5 "case" put 5 units
-- into stock. wh_item_uoms makes the alternates real.
--
-- factor is base-units-per-alternate-unit (a case of 24 ea has factor 24). The
-- base unit is implicit and always factor 1, so it is not stored here.
--
-- Document lines now keep BOTH figures: qty stays in base units, so every
-- existing balance, costing and movement path is untouched, while
-- entered_qty/entered_uom preserve what the operator actually typed for the
-- audit trail and for reprinting the document. Existing rows backfill to
-- entered = qty at factor 1, which is exactly what they meant back when the base
-- unit was the only unit. An item with no wh_item_uoms rows therefore behaves
-- precisely as it did before this migration.

CREATE TABLE IF NOT EXISTS wh_item_uoms (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id             UUID NOT NULL REFERENCES wh_items(id) ON DELETE CASCADE,
    uom                 TEXT NOT NULL,
    factor              NUMERIC(18, 6) NOT NULL CHECK (factor > 0),
    is_purchase_default BOOLEAN NOT NULL DEFAULT FALSE,
    is_sales_default    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (item_id, uom)
);

CREATE INDEX IF NOT EXISTS wh_item_uoms_item_idx ON wh_item_uoms (item_id);

-- At most one purchase default and one sales default per item, so resolving
-- "the unit this item is normally bought in" is a lookup, not a choice.
CREATE UNIQUE INDEX IF NOT EXISTS wh_item_uoms_purchase_default_uniq
    ON wh_item_uoms (item_id) WHERE is_purchase_default;

CREATE UNIQUE INDEX IF NOT EXISTS wh_item_uoms_sales_default_uniq
    ON wh_item_uoms (item_id) WHERE is_sales_default;

ALTER TABLE wh_receipt_lines ADD COLUMN IF NOT EXISTS entered_qty NUMERIC(18, 3) NOT NULL DEFAULT 0;

ALTER TABLE wh_receipt_lines ADD COLUMN IF NOT EXISTS entered_uom TEXT NOT NULL DEFAULT '';

ALTER TABLE wh_receipt_lines ADD COLUMN IF NOT EXISTS uom_factor NUMERIC(18, 6) NOT NULL DEFAULT 1;

ALTER TABLE wh_issue_lines ADD COLUMN IF NOT EXISTS entered_qty NUMERIC(18, 3) NOT NULL DEFAULT 0;

ALTER TABLE wh_issue_lines ADD COLUMN IF NOT EXISTS entered_uom TEXT NOT NULL DEFAULT '';

ALTER TABLE wh_issue_lines ADD COLUMN IF NOT EXISTS uom_factor NUMERIC(18, 6) NOT NULL DEFAULT 1;

UPDATE wh_receipt_lines SET entered_qty = qty, entered_uom = uom WHERE entered_uom = '';

UPDATE wh_issue_lines SET entered_qty = qty, entered_uom = uom WHERE entered_uom = '';
