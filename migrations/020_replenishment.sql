-- 020: replenishment tasks.
--
-- wh_stock_thresholds and wh_items.min_qty already noticed when stock ran low —
-- and then raised an alert and stopped. An alert is not work. Somebody still had
-- to read it, find where the stock actually was, and walk it over.
--
-- A replenishment level is a min/max on a *specific bin*, normally a pick face:
-- when free stock in that bin falls below min, top it back up to max from bulk.
-- That is a different question from wh_items.min_qty, which asks whether the
-- site as a whole is running out and should reorder from a supplier. Both are
-- needed and neither substitutes for the other.
--
-- An open task reserves its source stock the same way an open pick list does, so
-- replenishment that has been planned but not yet walked cannot be picked out
-- from under itself. Completing the task consumes that reservation and posts a
-- normal transfer movement; cancelling releases it.
--
-- The partial unique index is what makes the generator safe to run on a
-- schedule: a second run while the first task is still open is a no-op rather
-- than a duplicate instruction.

CREATE TABLE IF NOT EXISTS wh_replen_levels (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id        UUID NOT NULL REFERENCES wh_items(id) ON DELETE CASCADE,
    bin_id         UUID NOT NULL REFERENCES wh_bins(id) ON DELETE CASCADE,
    min_qty        NUMERIC(18, 3) NOT NULL CHECK (min_qty >= 0),
    max_qty        NUMERIC(18, 3) NOT NULL CHECK (max_qty > 0),
    source_zone_id UUID REFERENCES wh_zones(id) ON DELETE SET NULL,
    active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_by     UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (item_id, bin_id),
    CONSTRAINT wh_replen_levels_max_ge_min CHECK (max_qty >= min_qty)
);

CREATE INDEX IF NOT EXISTS wh_replen_levels_active_idx ON wh_replen_levels (item_id) WHERE active;

CREATE TABLE IF NOT EXISTS wh_replen_tasks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id      UUID NOT NULL REFERENCES wh_items(id),
    from_bin_id  UUID NOT NULL REFERENCES wh_bins(id),
    to_bin_id    UUID NOT NULL REFERENCES wh_bins(id),
    lot_key      TEXT NOT NULL DEFAULT '',
    qty          NUMERIC(18, 3) NOT NULL CHECK (qty > 0),
    moved_qty    NUMERIC(18, 3) NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'completed', 'cancelled')),
    trigger      TEXT NOT NULL DEFAULT 'min_max' CHECK (trigger IN ('min_max', 'manual')),
    level_id     UUID REFERENCES wh_replen_levels(id) ON DELETE SET NULL,
    notes        TEXT,
    created_by   UUID,
    completed_by UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS wh_replen_tasks_open_uniq
    ON wh_replen_tasks (item_id, to_bin_id) WHERE status = 'open';

CREATE INDEX IF NOT EXISTS wh_replen_tasks_status_idx ON wh_replen_tasks (status, created_at DESC);
