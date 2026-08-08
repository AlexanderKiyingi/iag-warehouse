-- The bespoke consumer loop is gone, replaced by the shared platform consumer
-- (retries, DLQ, and dedupe ordered after the work). That consumer's dedupe
-- writes only the event id, so the topic column can no longer be required.
--
-- With the shared consumer, a row's existence means "handled" — it is written
-- after the handler succeeds, so the claim/completion split that 016 introduced
-- is no longer needed to tell the two apart.

ALTER TABLE kafka_dedupe ALTER COLUMN topic DROP NOT NULL;

-- Rows left incomplete are the ones 016 was written to rescue: events claimed by
-- an attempt that failed or died, whose work never happened. Under the new
-- semantics their mere presence would read as "handled" and suppress the
-- redelivery, which is the data loss 016 fixed. Delete them so they reprocess.
DELETE FROM kafka_dedupe WHERE completed_at IS NULL;

DROP INDEX IF EXISTS kafka_dedupe_incomplete_idx;
