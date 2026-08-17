-- 022: handling units (license plates).
--
-- Stock could be counted but not carried. Everything in this service is keyed by
-- (item, bin, lot, serial), so the only way to move a pallet of eleven different
-- SKUs was eleven separate line entries naming eleven quantities — which is why
-- nobody did it, and why "where is that pallet" had no answer.
--
-- A handling unit is a container with a licence plate: a pallet, a carton, a
-- tote, a bag. Scanning one plate identifies everything on it, and moving one
-- plate moves everything on it in a single instruction.
--
-- The deliberate design decision here is that wh_hu_contents is NOT a second
-- source of truth for stock. wh_stock_balances remains authoritative; contents
-- record which of the stock standing in a bin is sitting on which plate. Moving
-- a handling unit posts ordinary transfer movements for its contents, so the
-- ledger, the valuation and finance see a pallet move exactly as they see any
-- other move, and a service that knows nothing about handling units is never
-- wrong about how much stock exists. The cost of that choice is an invariant the
-- application has to hold — contents of a bin must not exceed its balance — and
-- the add path checks it under a row lock.
--
-- parent_hu_id allows nesting (cartons on a pallet). It is a plain self
-- reference rather than a closure table because warehouse nesting is two or
-- three deep in practice, never a deep tree.

CREATE TABLE IF NOT EXISTS wh_handling_units (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lpn          TEXT NOT NULL UNIQUE,
    hu_type      TEXT NOT NULL DEFAULT 'pallet' CHECK (hu_type IN ('pallet', 'carton', 'tote', 'bag')),
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed', 'shipped', 'consumed', 'cancelled')),
    bin_id       UUID REFERENCES wh_bins(id) ON DELETE SET NULL,
    parent_hu_id UUID REFERENCES wh_handling_units(id) ON DELETE SET NULL,
    attrs        JSONB NOT NULL DEFAULT '{}',
    created_by   UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS wh_handling_units_bin_idx ON wh_handling_units (bin_id) WHERE status IN ('open', 'closed');

CREATE INDEX IF NOT EXISTS wh_handling_units_parent_idx ON wh_handling_units (parent_hu_id) WHERE parent_hu_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS wh_hu_contents (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hu_id      UUID NOT NULL REFERENCES wh_handling_units(id) ON DELETE CASCADE,
    item_id    UUID NOT NULL REFERENCES wh_items(id),
    lot_key    TEXT NOT NULL DEFAULT '',
    serial_key TEXT NOT NULL DEFAULT '',
    qty        NUMERIC(18, 3) NOT NULL CHECK (qty >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (hu_id, item_id, lot_key, serial_key)
);

CREATE INDEX IF NOT EXISTS wh_hu_contents_item_idx ON wh_hu_contents (item_id);
