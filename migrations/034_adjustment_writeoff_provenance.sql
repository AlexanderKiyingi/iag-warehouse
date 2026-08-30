-- 034: what a write-off was for, who wears the cost, and what it was worth.
--
-- `wh_adjustments` recorded only a free-text reason. That is enough to move
-- stock and not enough to review the movement afterwards: a stores clerk
-- entering a write-off is asked for a reason type, an expense account and a
-- value, and none of the three had anywhere to go, so a required field on the
-- form was collected and discarded.
--
-- All four columns are nullable. Adjustments are also raised by peer services
-- and by count approval, and none of those know an expense account; making any
-- of this mandatory in the database would break callers that are behaving
-- correctly. The requirement belongs on the write-off path in the handler,
-- where it can be stated as a rule about that document rather than about every
-- stock adjustment.

ALTER TABLE wh_adjustments
    ADD COLUMN IF NOT EXISTS reason_code     TEXT,
    ADD COLUMN IF NOT EXISTS expense_account TEXT,
    ADD COLUMN IF NOT EXISTS unit_cost       NUMERIC(18, 4),
    ADD COLUMN IF NOT EXISTS value           NUMERIC(18, 2),
    ADD COLUMN IF NOT EXISTS evidence_notes  TEXT;

-- The reason codes are a closed set because the point of the column is to make
-- write-offs countable by cause; free text does not aggregate. It is a superset
-- of what any one client offers today so a client can add a reason without a
-- migration. No existing row can violate it — the column is new — so this is a
-- plain validated CHECK rather than the NOT VALID pattern used in 026/033.
ALTER TABLE wh_adjustments
    ADD CONSTRAINT wh_adjustments_reason_code_chk
    CHECK (reason_code IS NULL OR reason_code IN (
        'damage', 'loss', 'theft', 'expiry', 'obsolescence', 'shrinkage', 'other'
    ));

-- `unit_cost` and `value` are the CLAIMED valuation of the write-off, entered
-- by the person raising it. They are deliberately separate from the costing
-- engine's `wh_movements.unit_cost` / `total_cost`, which is what finance books
-- the GL from. Two services must not be in charge of one number: this pair is
-- evidence of what the raiser believed the loss was worth, and it is carried to
-- finance on the movement event as declared_* so it can be reconciled against
-- the computed figure rather than silently replacing it.
COMMENT ON COLUMN wh_adjustments.unit_cost IS
    'Declared unit cost entered on the write-off. Not the WAC — see wh_movements.unit_cost.';
COMMENT ON COLUMN wh_adjustments.value IS
    'Declared total value of the write-off. Derived from unit_cost x quantity when only one was given.';

-- The worklist: write-offs with no expense account, for the same reason
-- 026 shipped wh_unreferenced_issues. History is left alone; this is what shows
-- how much of it there is.
CREATE OR REPLACE VIEW wh_unaccounted_writeoffs AS
SELECT a.id, a.item_id, i.sku, a.qty_before, a.qty_after,
       (a.qty_after - a.qty_before) AS qty_delta,
       a.reason, a.reason_code, a.created_at
FROM wh_adjustments a
JOIN wh_items i ON i.id = a.item_id
WHERE a.adj_type = 'adjustment'
  AND a.qty_after < a.qty_before
  AND (a.expense_account IS NULL OR a.expense_account = '');

CREATE INDEX IF NOT EXISTS wh_adjustments_reason_code_idx
    ON wh_adjustments (reason_code)
    WHERE reason_code IS NOT NULL;
