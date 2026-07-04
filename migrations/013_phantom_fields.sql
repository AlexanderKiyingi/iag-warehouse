-- Phantom-field storage: capture the descriptive fields the storesiag UI
-- already collects but had no backend home for, so receipts/requests round-trip
-- real data instead of blanks. Receipt monetary value is NOT stored — it is
-- computed on read from the priced lines (qty * unit_cost).

ALTER TABLE wh_receipts ADD COLUMN IF NOT EXISTS supplier    TEXT;
ALTER TABLE wh_receipts ADD COLUMN IF NOT EXISTS received_by TEXT;

ALTER TABLE wh_issues ADD COLUMN IF NOT EXISTS requested_by TEXT;
ALTER TABLE wh_issues ADD COLUMN IF NOT EXISTS priority     TEXT;
