-- Seed: Mbale mill, KLA FG warehouse, workshop + sample items.

INSERT INTO wh_facilities (code, name, site_type) VALUES
    ('MBALE-MILL', 'Mbale Coffee Mill', 'mill'),
    ('KLA-FG', 'Kampala Finished Goods Warehouse', 'fg_warehouse'),
    ('WORKSHOP', 'Fleet Workshop Store', 'workshop')
ON CONFLICT (code) DO NOTHING;

INSERT INTO wh_zones (facility_id, code, name, zone_type)
SELECT f.id, z.code, z.name, z.zone_type
FROM (VALUES
    ('MBALE-MILL', 'RCV', 'Receiving', 'receiving'),
    ('MBALE-MILL', 'RM-BULK', 'Raw Material Bulk', 'bulk'),
    ('MBALE-MILL', 'STG', 'Staging', 'staging'),
    ('KLA-FG', 'RCV', 'Receiving', 'receiving'),
    ('KLA-FG', 'FG-STG', 'FG Staging', 'staging'),
    ('KLA-FG', 'COLD', 'Cold Storage', 'cold'),
    ('WORKSHOP', 'SPARES', 'Spare Parts', 'bulk'),
    ('WORKSHOP', 'CONS', 'Consumables', 'bulk'),
    ('WORKSHOP', 'ASSETS', 'Equipment Yard', 'assets')
) AS z(facility_code, code, name, zone_type)
JOIN wh_facilities f ON f.code = z.facility_code
ON CONFLICT (facility_id, code) DO NOTHING;

INSERT INTO wh_bins (zone_id, code, capacity_kg, status)
SELECT z.id, b.code, b.capacity_kg, 'active'
FROM (VALUES
    ('MBALE-MILL', 'RM-BULK', 'RM-A1', 50000),
    ('MBALE-MILL', 'RCV', 'RCV-01', 10000),
    ('MBALE-MILL', 'STG', 'STG-01', 20000),
    ('KLA-FG', 'FG-STG', 'FG-A1', 30000),
    ('KLA-FG', 'RCV', 'RCV-01', 5000),
    ('KLA-FG', 'COLD', 'COLD-01', 15000),
    ('WORKSHOP', 'SPARES', 'SP-A1', 5000),
    ('WORKSHOP', 'CONS', 'CON-A1', 3000),
    ('WORKSHOP', 'ASSETS', 'EQ-01', NULL)
) AS b(facility_code, zone_code, code, capacity_kg)
JOIN wh_facilities f ON f.code = b.facility_code
JOIN wh_zones z ON z.facility_id = f.id AND z.code = b.zone_code
ON CONFLICT (zone_id, code) DO NOTHING;

INSERT INTO wh_items (sku, name, material_class, tracking_mode, uom, min_qty, attrs)
SELECT v.sku, v.name, v.material_class, v.tracking_mode, v.uom, v.min_qty, v.attrs::jsonb
FROM (VALUES
    ('RM-GREEN-001', 'Arabica Green Coffee', 'raw_material', 'lot', 'kg', 0, '{"origin":"Uganda"}'),
    ('FG-ROAST-250', 'Roasted Coffee 250g', 'finished_good', 'lot', 'ea', 0, '{}'),
    ('CON-DIESEL', 'Diesel Fuel', 'consumable', 'bulk', 'litre', 500, '{"department":"fleet"}'),
    ('SP-FILTER-01', 'Oil Filter HF-204', 'spare_part', 'sku', 'ea', 10, '{}'),
    ('EQ-ROASTER-01', 'Probat Roaster Unit', 'equipment', 'serial', 'ea', 0, '{}')
) AS v(sku, name, material_class, tracking_mode, uom, min_qty, attrs)
WHERE NOT EXISTS (SELECT 1 FROM wh_items i WHERE i.sku = v.sku);

INSERT INTO wh_spare_compat (item_id, asset_type)
SELECT i.id, 'roaster'
FROM wh_items i WHERE i.sku = 'SP-FILTER-01'
ON CONFLICT (item_id, asset_type) DO NOTHING;

INSERT INTO wh_spare_compat (item_id, asset_type)
SELECT i.id, 'vehicle'
FROM wh_items i WHERE i.sku = 'SP-FILTER-01'
ON CONFLICT (item_id, asset_type) DO NOTHING;
