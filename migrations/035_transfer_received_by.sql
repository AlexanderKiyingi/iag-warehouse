-- 035: who signed for a transfer at the far end.
--
-- A transfer posts in one act here — stock leaves the source bin and lands in
-- the destination bin inside one transaction — so there is no second "receive"
-- step to hang a receiver off. That is a deliberate design and this migration
-- does not change it: a two-step transfer with goods in transit is a workflow
-- decision, not a column.
--
-- What was missing is smaller and worth having on its own: the name of the
-- person who took delivery. Clients collect it (the inventory app has had a
-- "Received by" field on the transfer form since before the platform wiring)
-- and it had nowhere to go, so it was dropped on every save.
--
-- Nullable, because every peer service and every internal transfer — a
-- replenishment task, a handling-unit move — raises transfers without one.

ALTER TABLE wh_transfers
    ADD COLUMN IF NOT EXISTS received_by TEXT;

COMMENT ON COLUMN wh_transfers.received_by IS
    'Who took delivery at the destination. Recorded at posting; there is no separate receive step.';
