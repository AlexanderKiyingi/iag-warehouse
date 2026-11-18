-- 038: Persist optional fleet damage-claim link on warehouse returns.
ALTER TABLE wh_returns ADD COLUMN IF NOT EXISTS fleet_claim_ref TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS wh_returns_fleet_claim_ref_idx ON wh_returns (fleet_claim_ref)
  WHERE fleet_claim_ref <> '';
