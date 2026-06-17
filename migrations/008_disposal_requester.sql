-- 008: capture the disposal requester (email) so the tiered approval can enforce
-- segregation of duties — the person who requested a disposal may not approve it.

ALTER TABLE wh_asset_disposals ADD COLUMN IF NOT EXISTS requested_by TEXT NOT NULL DEFAULT '';
