-- wh_items.sku was indexed but not unique, which is why SKU-0041 could be issued
-- to two different products ("Nails 3 inch" and "CONCRETE NAILS 4 INCH") - a
-- duplicate that came across from the monolith, where the counter failed to
-- advance between two entries four minutes apart.
--
-- Partial, so an item with no sku yet is still permitted; a full UNIQUE would
-- conflate "not assigned" with "duplicate".
CREATE UNIQUE INDEX IF NOT EXISTS wh_items_sku_unique
    ON warehouse.wh_items (sku)
 WHERE btrim(coalesce(sku, '')) <> '';

-- A receipt line must not lose the putaway rule it was placed by.
ALTER TABLE warehouse.wh_receipt_lines
  DROP CONSTRAINT IF EXISTS wh_receipt_lines_putaway_rule_id_fkey;
ALTER TABLE warehouse.wh_receipt_lines
  ADD CONSTRAINT wh_receipt_lines_putaway_rule_id_fkey FOREIGN KEY (putaway_rule_id)
  REFERENCES warehouse.wh_putaway_rules (id) ON DELETE RESTRICT;
