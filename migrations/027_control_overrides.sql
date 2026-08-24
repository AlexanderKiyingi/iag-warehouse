-- 027: the control override log.
--
-- FR-ADM-07 and acceptance criterion 14. A warehouse system earns its controls
-- by refusing things; it earns trust by recording the times it did not. Every
-- FEFO override, tolerance breach, emergency issue, negative-stock exception,
-- item-status change and gate exception lands here with an actor and a reason,
-- and the exception report is one query rather than a hunt through the API log.
--
-- Separate from wh_api_audit on purpose. That table answers "what requests were
-- made"; this one answers "when did we let something through that the rules
-- said no to", which is a much shorter list and the one an auditor asks for.
-- Statements are split on ";\n\n" by the migrator.

CREATE TABLE IF NOT EXISTS wh_control_overrides (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT NOT NULL,
    -- What was overridden, in the operator's vocabulary: a SKU, a slip number,
    -- a count code. Free text because the subject differs per kind and a join
    -- would tie the log's readability to rows that may later be deleted.
    subject     TEXT NOT NULL DEFAULT '',
    from_state  TEXT NOT NULL DEFAULT '',
    to_state    TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT '',
    ref_type    TEXT NOT NULL DEFAULT '',
    ref_id      TEXT NOT NULL DEFAULT '',
    -- The permission that made it possible, so a review can ask who else holds it.
    permission  TEXT NOT NULL DEFAULT '',
    actor_id    UUID,
    actor_name  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS wh_control_overrides_created_idx
    ON wh_control_overrides (created_at DESC);

CREATE INDEX IF NOT EXISTS wh_control_overrides_kind_idx
    ON wh_control_overrides (kind, created_at DESC);

CREATE INDEX IF NOT EXISTS wh_control_overrides_actor_idx
    ON wh_control_overrides (actor_id, created_at DESC)
    WHERE actor_id IS NOT NULL;

-- Immutability, enforced rather than promised.
--
-- "Append-only by convention" survives exactly until the first person who wants
-- a tidier report. A log of the controls we bypassed is worth nothing if it can
-- itself be edited, so the database refuses. Correcting an entry means writing
-- a new one that says so.
CREATE OR REPLACE FUNCTION wh_control_overrides_immutable() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'wh_control_overrides is append-only: % is not permitted', TG_OP
        USING HINT = 'Record a correcting entry instead of amending this one.';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS wh_control_overrides_no_amend ON wh_control_overrides;

CREATE TRIGGER wh_control_overrides_no_amend
    BEFORE UPDATE OR DELETE ON wh_control_overrides
    FOR EACH ROW EXECUTE FUNCTION wh_control_overrides_immutable();
