-- Allow pick confirmation movements.
ALTER TABLE wh_movements DROP CONSTRAINT IF EXISTS wh_movements_movement_type_check;
ALTER TABLE wh_movements ADD CONSTRAINT wh_movements_movement_type_check CHECK (movement_type IN (
    'receipt', 'issue', 'transfer', 'production_consume', 'production_output',
    'return', 'adjustment', 'asset_checkin', 'asset_checkout', 'pick'
));
