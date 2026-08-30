-- 036: the documents that justify a write-off.
--
-- Migration 034 gave an adjustment its reason code, its expense account, its
-- declared value and its evidence notes. It did not give it the evidence — the
-- photograph of the damaged pallet, the police report, the carrier's claim
-- form. Those are the part a reviewer actually wants, and the write-off form
-- has always asked for them under "Supporting documents".
--
-- The bytes live in iag-dms, which owns object storage on this platform. What
-- goes here is the reference list: id, storage id, filename, mime type, size,
-- upload timestamp. Storing the payload in a column of this table would put
-- megabytes of base64 inside a row that every stock query joins.
--
-- JSONB rather than a child table because there is exactly one consumer shape —
-- "show me this write-off's documents" — and nothing joins, filters or reports
-- across attachments. A child table would buy referential integrity against a
-- service that does not own the rows either.
--
-- Nullable, like every other 034 column and for the same reason: adjustments
-- raised by peer services and by count approval have no documents behind them.

ALTER TABLE wh_adjustments
    ADD COLUMN IF NOT EXISTS attachments JSONB;

COMMENT ON COLUMN wh_adjustments.attachments IS
    'References to files held in iag-dms: [{id, storageId, name, mime, size, uploadedAt}]. Never the bytes.';
