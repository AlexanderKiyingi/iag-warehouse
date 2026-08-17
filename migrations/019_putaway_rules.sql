-- 019: directed putaway.
--
-- Until now a receipt named the bin it was going into: the system recorded a
-- decision an operator had already made in their head, and never made one. That
-- is the difference between an inventory ledger and a warehouse system, and it
-- is why stock drifts to wherever there was room that morning.
--
-- A putaway rule is a match plus a target scope plus a strategy. Rules are
-- evaluated in priority order (lowest number first) and the first one that both
-- matches the item and yields a legal bin wins. Every criterion is nullable and
-- a NULL criterion means "don't care", so a single row with everything NULL is a
-- valid catch-all default.
--
-- Strategies:
--   fixed_bin   — always target_bin_id (a home slot)
--   consolidate — a bin already holding this item/lot, fullest first
--   empty_bin   — a bin holding nothing at all
--   least_used  — the emptiest bin in scope by weight
--   capacity_fit— best fit: the bin whose remaining capacity is tightest
--
-- Capacity is checked for every strategy, not just capacity_fit, so a rule can
-- never direct stock into a bin that cannot hold it. It needs a weight per unit
-- to do that, which is what weight_kg adds. weight_kg = 0 (the default, and
-- every pre-existing row) means the item's weight is unknown, and an unknown
-- weight is treated as unconstrained rather than as zero — guessing "weightless"
-- would silently fill every bin to infinity.

ALTER TABLE wh_items ADD COLUMN IF NOT EXISTS weight_kg NUMERIC(18, 4) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS wh_putaway_rules (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL,
    priority         INT NOT NULL DEFAULT 100,
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    item_id          UUID REFERENCES wh_items(id) ON DELETE CASCADE,
    material_class   TEXT,
    tracking_mode    TEXT,
    facility_id      UUID REFERENCES wh_facilities(id) ON DELETE CASCADE,
    target_zone_id   UUID REFERENCES wh_zones(id) ON DELETE CASCADE,
    target_zone_type TEXT,
    target_bin_id    UUID REFERENCES wh_bins(id) ON DELETE CASCADE,
    strategy         TEXT NOT NULL DEFAULT 'consolidate' CHECK (strategy IN (
        'fixed_bin', 'consolidate', 'empty_bin', 'least_used', 'capacity_fit'
    )),
    notes            TEXT,
    created_by       UUID,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT wh_putaway_rules_fixed_bin_needs_target
        CHECK (strategy <> 'fixed_bin' OR target_bin_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS wh_putaway_rules_eval_idx
    ON wh_putaway_rules (priority, created_at) WHERE active;

-- Which rule directed a receipt line, kept so a putaway decision can be audited
-- after the rule that made it has been edited or deleted.
ALTER TABLE wh_receipt_lines ADD COLUMN IF NOT EXISTS putaway_rule_id UUID;
