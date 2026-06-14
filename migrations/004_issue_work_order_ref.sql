-- Fleet integration: carry the originating fleet maintenance work-order id on
-- stock issues so a warehouse issue is traceable back to the WO that consumed
-- it (production already uses production_order_ref; fleet WOs are a distinct
-- concept and deserve their own column rather than overloading that one).
ALTER TABLE wh_issues ADD COLUMN IF NOT EXISTS work_order_ref TEXT;

-- Index for the common "show me everything issued against this WO" lookup and
-- for the fleet reconciliation/idempotency guard (issue once per WO line).
CREATE INDEX IF NOT EXISTS wh_issues_work_order_ref_idx
    ON wh_issues (work_order_ref)
    WHERE work_order_ref IS NOT NULL;
