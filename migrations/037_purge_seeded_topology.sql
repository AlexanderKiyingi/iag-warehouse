-- Purge the three demo facilities seeded by 002_seed.sql, and their zones and bins.
--
-- 029_purge_demo_seed removed the demo item catalogue but deliberately kept the
-- site topology, on the reasoning that facilities are "configuration an operator
-- adjusts, not demo data". That holds for a warehouse an operator set up. It does
-- not hold for these three, which came from a seed file rather than an operator:
--
--   MBALE-MILL  Mbale Coffee Mill
--   KLA-FG      Kampala Finished Goods Warehouse
--   WORKSHOP    Fleet Workshop Store
--
-- The six real facilities cloned from the monolith - General Warehouse, Milk
-- Processing Store, Poultry Warehouse, Cold warehouse, Nyabihoko Stores, KASESE -
-- are untouched. The distinction 029 drew was topology versus catalogue; the one
-- that matters is seeded versus real, and these three fall on the wrong side of it.
--
-- Each seeded facility carries 3 zones and 3 bins, so this removes 3 facilities,
-- 9 zones and 9 bins.
--
-- SAFE TO DELETE, VERIFIED RATHER THAN ASSUMED
--
-- Twenty-four foreign keys point at wh_facilities, wh_zones and wh_bins, and all
-- but four are ON DELETE NO ACTION - a single referencing row would abort this.
-- Every one of those tables was counted before writing this migration and every
-- one is empty: stock_balances, movements, assets, receipt/issue/transfer/pick/
-- count lines, adjustments, handling units, putaway rules, replen levels and
-- tasks, slips and transfers.
--
-- The real stock activity that does exist - 3 wh_stock_in rows and 1
-- wh_stock_transfer, including a UGX 20.3M cement receipt - records its location
-- as free text (location_ref), not a facility id, and names only real locations:
-- "General Warehouse", "Ntungamo", "Nyabihoko  Stores". None names a seeded
-- facility, so none is affected.
--
-- Guarded by name AND by the absence of dependants, so re-running is harmless and
-- a facility an operator has since transacted against would survive.
--
-- The migration runner wraps each file in its own transaction, so no BEGIN here.

DELETE FROM wh_bins b
 USING wh_zones z, wh_facilities f
 WHERE b.zone_id = z.id
   AND z.facility_id = f.id
   AND f.code IN ('MBALE-MILL', 'KLA-FG', 'WORKSHOP')
   AND NOT EXISTS (SELECT 1 FROM wh_stock_balances s WHERE s.bin_id = b.id)
   AND NOT EXISTS (SELECT 1 FROM wh_movements m WHERE m.from_bin_id = b.id OR m.to_bin_id = b.id);

DELETE FROM wh_zones z
 USING wh_facilities f
 WHERE z.facility_id = f.id
   AND f.code IN ('MBALE-MILL', 'KLA-FG', 'WORKSHOP')
   AND NOT EXISTS (SELECT 1 FROM wh_bins b WHERE b.zone_id = z.id);

DELETE FROM wh_facilities f
 WHERE f.code IN ('MBALE-MILL', 'KLA-FG', 'WORKSHOP')
   AND NOT EXISTS (SELECT 1 FROM wh_zones z WHERE z.facility_id = f.id);
