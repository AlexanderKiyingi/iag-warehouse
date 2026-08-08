-- Persist the valuation on the movement itself.
--
-- Cost was computed per movement and put on the emitted event, but never
-- stored. That was tolerable while the only consumer was finance's GL, which
-- reads the event. It stops being tolerable for production: clearing an order's
-- work in progress means knowing what that order has already consumed, and
-- that is a query over past movements, not over an event stream nobody keeps.
--
-- It also makes wh_movements a complete subledger — a movement ledger that
-- records quantity but not value is half a record, and reconciling inventory
-- value back to the GL previously had nothing on this side to reconcile
-- against.
--
-- total_cost keeps the signed convention the emitted payload already uses:
-- positive into stock, negative out of it.

ALTER TABLE wh_movements ADD COLUMN IF NOT EXISTS unit_cost NUMERIC(18, 4) NOT NULL DEFAULT 0;

ALTER TABLE wh_movements ADD COLUMN IF NOT EXISTS total_cost NUMERIC(18, 4) NOT NULL DEFAULT 0;

ALTER TABLE wh_movements ADD COLUMN IF NOT EXISTS cost_currency TEXT NOT NULL DEFAULT '';

-- Work in progress is summed per batch across consume and output movements, so
-- this is the index that query rides.
CREATE INDEX IF NOT EXISTS wh_movements_batch_cost_idx
    ON wh_movements (batch_business_id, movement_type)
    WHERE batch_business_id IS NOT NULL;
