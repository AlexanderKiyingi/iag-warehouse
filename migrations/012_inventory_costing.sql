-- 012: Weighted-average-cost columns for perpetual inventory.
--
-- avg_cost is the item's moving-average unit cost, recomputed on each valued
-- receipt; unit_cost is the per-line purchase cost captured on receipt (from the
-- PO/GRN). Both default 0 so existing rows are cost-less until costing is enabled
-- (INVENTORY_COSTING_ENABLED) and priced receipts flow. See
-- docs/PERPETUAL_INVENTORY_EVENTS.md.
ALTER TABLE wh_items ADD COLUMN IF NOT EXISTS avg_cost NUMERIC(18, 4) NOT NULL DEFAULT 0;
ALTER TABLE wh_receipt_lines ADD COLUMN IF NOT EXISTS unit_cost NUMERIC(18, 4) NOT NULL DEFAULT 0;
