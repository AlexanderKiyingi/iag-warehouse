-- 014: Backfill wh_items.avg_cost from historical receipt-line prices.
--
-- When INVENTORY_COSTING_ENABLED is turned on, the weighted-average-cost engine
-- only recomputes avg_cost from the NEXT priced receipt forward. Any item with
-- existing on-hand stock would carry avg_cost = 0, so its issues/adjustments emit
-- zero-cost movements (finance no-ops) — understating COGS until it is received
-- again. This seeds each item's avg_cost from the weighted average of its
-- historical receipt lines that carried a positive unit_cost.
--
-- Only items currently at 0/NULL avg_cost are seeded, so a value already computed
-- by the live engine is never clobbered. Idempotent: re-running touches only rows
-- still at zero.
UPDATE wh_items i
SET avg_cost = w.wavg
FROM (
    SELECT item_id, SUM(qty * unit_cost) / NULLIF(SUM(qty), 0) AS wavg
    FROM wh_receipt_lines
    WHERE unit_cost > 0 AND qty > 0
    GROUP BY item_id
) w
WHERE i.id = w.item_id
  AND COALESCE(i.avg_cost, 0) = 0
  AND w.wavg IS NOT NULL;
