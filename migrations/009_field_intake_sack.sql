-- Field-intake coffee sacks: the stock-of-record item the Field & Farm field
-- app receives RFID-tagged sacks against. Distinct from RM-GREEN-001 (milled
-- green coffee) — this is farmer-delivered parchment/cherry captured at the
-- farm gate. Lot-tracked so each physical sack carries its RFID as the lot_key
-- while keeping a variable kg quantity; per-sack provenance (farmer, bean,
-- source, condition) rides in the receipt-line / balance attrs.
INSERT INTO wh_items (sku, name, material_class, tracking_mode, uom, min_qty, attrs)
SELECT v.sku, v.name, v.material_class, v.tracking_mode, v.uom, v.min_qty, v.attrs::jsonb
FROM (VALUES
    ('RM-FIELD-SACK', 'Field Coffee Sack (parchment/cherry)', 'raw_material', 'lot', 'kg', 0,
     '{"origin":"Uganda","capture":"field_intake","tag":"rfid"}')
) AS v(sku, name, material_class, tracking_mode, uom, min_qty, attrs)
WHERE NOT EXISTS (SELECT 1 FROM wh_items i WHERE i.sku = v.sku);
