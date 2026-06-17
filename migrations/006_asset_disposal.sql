-- 006: asset disposal — retire a serialized asset out of stores with a recorded
-- method / proceeds / authorization and a gate pass. The asset is removed from
-- its bin and marked disposed (one-way), a disposal movement is posted, and
-- warehouse.asset.disposed is emitted so finance can book the gain/loss on
-- disposal against the carried book value. Split on ";\n\n" by the migrator.

ALTER TABLE wh_assets ADD COLUMN IF NOT EXISTS disposed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS wh_asset_disposals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id      UUID NOT NULL REFERENCES wh_assets(id),
    asset_tag     TEXT NOT NULL,
    method        TEXT NOT NULL CHECK (method IN ('sale', 'scrap', 'donation', 'trade_in', 'write_off', 'lost')),
    reason        TEXT NOT NULL DEFAULT '',
    proceeds      NUMERIC(18, 2) NOT NULL DEFAULT 0,
    currency      TEXT NOT NULL DEFAULT 'UGX',
    book_value    NUMERIC(18, 2),
    gate_pass_no  TEXT NOT NULL DEFAULT '',
    authorized_by TEXT NOT NULL DEFAULT '',
    disposed_by   UUID,
    attrs         JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS wh_asset_disposals_asset_idx ON wh_asset_disposals (asset_id);
