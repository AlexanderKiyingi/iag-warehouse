-- 005: stock reservations + non-negative guards.
--
-- reserved holds stock allocated to open pick lists so it can't be issued or
-- promised twice: available = qty - reserved. A pick reserves at creation,
-- consumes (qty- and reserved-) on confirm, and releases (reserved-) on cancel.
-- The qty/reserved >= 0 CHECKs are defence-in-depth behind the app-level
-- FOR UPDATE guards (reserved <= qty is maintained by the reserve/consume paths
-- rather than a DB CHECK, so a legitimate negative adjustment can't trip a raw
-- constraint error). Statements are split on ";\n\n" by the migrator.

ALTER TABLE wh_stock_balances ADD COLUMN IF NOT EXISTS reserved NUMERIC(18, 3) NOT NULL DEFAULT 0;

ALTER TABLE wh_stock_balances ADD CONSTRAINT wh_stock_balances_qty_nonneg CHECK (qty >= 0);

ALTER TABLE wh_stock_balances ADD CONSTRAINT wh_stock_balances_reserved_nonneg CHECK (reserved >= 0);
