-- 007: tiered approval for asset disposal.
--
-- When WAREHOUSE_REQUIRE_DISPOSAL_APPROVAL is on, a disposal is created
-- 'pending_approval' (the asset is NOT yet retired) and must collect a signature
-- from every tier whose min_amount is below the disposal value (max of proceeds
-- and book value), cleared low-to-high by DISTINCT approvers holding the tier's
-- required_perm — then it executes (retires the asset, posts the movement, emits
-- warehouse.asset.disposed). With the flag off, disposal executes immediately
-- ('executed'). Statements are split on ";\n\n" by the migrator.

ALTER TABLE wh_asset_disposals ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'executed'
    CHECK (status IN ('pending_approval', 'approved', 'rejected', 'executed'));

ALTER TABLE wh_asset_disposals ADD COLUMN IF NOT EXISTS disposal_value NUMERIC(18, 2) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS wh_disposal_approval_tiers (
    tier          INTEGER PRIMARY KEY,
    label         TEXT NOT NULL DEFAULT '',
    min_amount    NUMERIC(18, 2) NOT NULL DEFAULT 0,
    max_amount    NUMERIC(18, 2),
    required_perm TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS wh_disposal_approvals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    disposal_id  UUID NOT NULL REFERENCES wh_asset_disposals(id) ON DELETE CASCADE,
    tier         INTEGER NOT NULL,
    actor        TEXT NOT NULL DEFAULT '',
    decision     TEXT NOT NULL,
    note         TEXT NOT NULL DEFAULT '',
    decided_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS wh_disposal_approvals_disposal_idx ON wh_disposal_approvals (disposal_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_wh_disposal_approvals_tier ON wh_disposal_approvals (disposal_id, tier) WHERE decision = 'approved';

INSERT INTO wh_disposal_approval_tiers (tier, label, min_amount, max_amount, required_perm)
VALUES
    (1, 'Supervisor', 0,        5000000,  'warehouse.approve_disposal_tier1'),
    (2, 'Manager',    5000000,  20000000, 'warehouse.approve_disposal_tier2'),
    (3, 'Director',   20000000, NULL,     'warehouse.approve_disposal_tier3')
ON CONFLICT (tier) DO NOTHING;
