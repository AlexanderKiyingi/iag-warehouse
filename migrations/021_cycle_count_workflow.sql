-- 021: cycle counting as a controlled workflow.
--
-- A "cycle count" was previously a stock adjustment with a different label on
-- it: one person, one row, one instant write to the balance, no second pair of
-- eyes and no record of what the system thought was there before somebody
-- decided otherwise. Auditors ask about that, and they are right to.
--
-- A count task snapshots system quantities for a scope, is counted line by line
-- (optionally blind, meaning the counter is not shown what the system expects),
-- is submitted for review, and is approved by someone other than the counter
-- before a single balance moves. Approval is the only step that writes stock,
-- and it writes it through the ordinary adjustment path so the movement ledger,
-- the valuation and the finance GL all see it the way they see everything else.
--
-- Tolerance decides what needs a human. A line inside tolerance is accepted on
-- submit; a line outside it stays pending an explicit accept or a recount, so
-- attention goes to the twelve lines that are wrong rather than the four hundred
-- that are right.
--
-- ABC class drives *when* an item gets counted: A items are the small share of
-- the catalogue carrying most of the value and are counted often, C items
-- rarely. abc_class is nullable because an unclassified item is a real state —
-- it means nothing has moved it yet — and last_counted_at is what the scheduler
-- compares against count_interval_days to decide what is due.

ALTER TABLE wh_items ADD COLUMN IF NOT EXISTS abc_class TEXT CHECK (abc_class IN ('A', 'B', 'C'));

ALTER TABLE wh_items ADD COLUMN IF NOT EXISTS count_interval_days INT;

ALTER TABLE wh_items ADD COLUMN IF NOT EXISTS last_counted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS wh_items_count_due_idx ON wh_items (abc_class, last_counted_at);

CREATE TABLE IF NOT EXISTS wh_count_tasks (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code           TEXT NOT NULL UNIQUE,
    status         TEXT NOT NULL DEFAULT 'counting' CHECK (status IN (
        'counting', 'review', 'approved', 'cancelled'
    )),
    scope_type     TEXT NOT NULL CHECK (scope_type IN ('zone', 'bin', 'item', 'abc')),
    scope_ref      TEXT NOT NULL DEFAULT '',
    blind          BOOLEAN NOT NULL DEFAULT TRUE,
    tolerance_pct  NUMERIC(9, 4) NOT NULL DEFAULT 0,
    tolerance_value NUMERIC(18, 4) NOT NULL DEFAULT 0,
    notes          TEXT,
    created_by     UUID,
    submitted_by   UUID,
    approved_by    UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    submitted_at   TIMESTAMPTZ,
    approved_at    TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS wh_count_tasks_status_idx ON wh_count_tasks (status, created_at DESC);

CREATE TABLE IF NOT EXISTS wh_count_lines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    count_task_id  UUID NOT NULL REFERENCES wh_count_tasks(id) ON DELETE CASCADE,
    item_id        UUID NOT NULL REFERENCES wh_items(id),
    bin_id         UUID NOT NULL REFERENCES wh_bins(id),
    lot_key        TEXT NOT NULL DEFAULT '',
    serial_key     TEXT NOT NULL DEFAULT '',
    system_qty     NUMERIC(18, 3) NOT NULL DEFAULT 0,
    counted_qty    NUMERIC(18, 3),
    variance_qty   NUMERIC(18, 3) NOT NULL DEFAULT 0,
    variance_value NUMERIC(18, 4) NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'counted', 'accepted', 'rejected', 'recount'
    )),
    note           TEXT,
    counted_by     UUID,
    counted_at     TIMESTAMPTZ,
    adjustment_id  UUID REFERENCES wh_adjustments(id) ON DELETE SET NULL,
    UNIQUE (count_task_id, item_id, bin_id, lot_key, serial_key)
);

CREATE INDEX IF NOT EXISTS wh_count_lines_task_idx ON wh_count_lines (count_task_id, status);
