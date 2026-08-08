-- kafka_dedupe recorded that an event had been *seen*, not that it had been
-- *handled*, and the row was committed before the handler ran. A dispatch that
-- failed left the claim behind: the message was never committed to Kafka, so it
-- was redelivered, and the redelivery found the row and skipped the work. A
-- transient database error during a goods receipt therefore discarded the
-- receipt permanently — stock that never landed, with the event marked done.
--
-- completed_at separates the two states. A row with no completion is a claim
-- from an attempt that failed or died, and may be taken over; only a completed
-- row means "this event has already been applied, do not apply it again".

ALTER TABLE kafka_dedupe
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;

-- Rows written before this migration were only ever created on the seen-path
-- and were treated as final by the old code. Marking them complete keeps that
-- meaning rather than re-opening every historic event for reprocessing.
UPDATE kafka_dedupe SET completed_at = seen_at WHERE completed_at IS NULL;

-- Stale claims are the ones worth looking at: an event that was claimed and
-- never completed is work that was dropped by a process that died mid-flight.
CREATE INDEX IF NOT EXISTS kafka_dedupe_incomplete_idx
    ON kafka_dedupe (seen_at) WHERE completed_at IS NULL;
