-- 026: item status lifecycle.
--
-- FR-ITM-10. An item master with no status has only one way to retire a part:
-- delete it, which breaks every historical movement that references it. So in
-- practice nothing is ever retired, superseded parts stay orderable, and the
-- catalogue grows until nobody trusts a search result.
--
--   draft       created but not yet approved for use — no transaction at all
--   active      normal
--   restricted  transactable only by someone holding warehouse.override_item_status
--   obsolete    no new receipts — stop buying it — but issues still allowed,
--               because running the remaining stock down is the point, and a
--               status that stranded it would just get switched back off
--   blocked     hard stop, typically a quality or safety decision
--
-- Every existing row becomes 'active'. That is the only honest default: the
-- rows were transactable a moment ago and a migration is not the place to
-- decide that some of them should not be. Statements are split on ";\n\n".

ALTER TABLE wh_items ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';

ALTER TABLE wh_items DROP CONSTRAINT IF EXISTS wh_items_status_check;

ALTER TABLE wh_items ADD CONSTRAINT wh_items_status_check
    CHECK (status IN ('draft', 'active', 'restricted', 'obsolete', 'blocked'));

-- Status is read on every receipt and issue line, and the overwhelming
-- majority of the table is 'active', so index the exceptions rather than the
-- whole column — a full index here would be mostly one repeated value.
CREATE INDEX IF NOT EXISTS wh_items_status_idx ON wh_items (status)
    WHERE status <> 'active';
