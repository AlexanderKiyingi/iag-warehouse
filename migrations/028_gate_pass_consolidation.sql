-- 028: one gate pass, not two.
--
-- Two tables have been modelling the same physical event. wh_gate_passes came
-- from the stores increment (migration 010): flat, TEXT dates, a single free-
-- text `items` column, no lines, no approval, no gate verification. wh_slips
-- came from the execution layer (migration 024): typed, numbered, approval-
-- gated, verify token, per-line serials and conditions, immutable event log.
--
-- Both were live and both were routed, so which one holds the truth about what
-- left the site depended on which endpoint the caller happened to use. That is
-- worse than having neither: a gate-pass register you cannot trust to be
-- complete is not a control, and §7.19 exists precisely to make outbound
-- movement verifiable.
--
-- wh_slips wins on every axis, so the legacy rows move into it and the flat
-- table goes. The /gate-passes endpoints keep working — they are now a facade
-- over wh_slips, so storesiag and anything else on that contract is unaffected.
--
-- Statements are split on ";\n\n" by the migrator.

-- Legacy dates are free TEXT and some of them will not be dates. Cast what
-- parses and drop what does not, rather than failing the migration over a
-- typo somebody made in a spreadsheet two years ago.
CREATE OR REPLACE FUNCTION wh_try_date(txt TEXT) RETURNS DATE AS $$
BEGIN
    RETURN NULLIF(TRIM(txt), '')::DATE;
EXCEPTION WHEN OTHERS THEN
    RETURN NULL;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Carry the id across unchanged. Anything holding a gate-pass id — a link, a
-- printout, a row in another service — keeps resolving.
INSERT INTO wh_slips (
    id, slip_no, slip_type, status, purpose, notes,
    issued_to_name, dept,
    returnable, return_by, returned_at,
    authorized_name, requested_name,
    ref_type, attrs, created_at, updated_at
)
SELECT
    g.id,
    -- The status CHECK requires a number on anything past draft, and some
    -- legacy rows have none. Mint a traceable one rather than demoting the row
    -- to draft, which would misrepresent a pass that was actually issued.
    COALESCE(NULLIF(TRIM(g.gate_pass_no), ''), 'GP-LEGACY-' || SUBSTRING(g.id::text, 1, 8)),
    'equipment_handover',
    CASE
        WHEN LOWER(TRIM(g.status)) IN ('returned', 'closed') THEN 'returned'
        WHEN LOWER(TRIM(g.status)) IN ('cancelled', 'canceled', 'void') THEN 'cancelled'
        ELSE 'released'
    END,
    COALESCE(g.purpose, ''),
    '',
    COALESCE(g.issued_to, ''),
    COALESCE(g.dept, ''),
    -- Every legacy pass was "On Loan" by default: the flat model had no concept
    -- of a non-returnable pass, so all of them are returnable by construction.
    TRUE,
    wh_try_date(g.return_by),
    wh_try_date(g.return_date)::timestamptz,
    COALESCE(g.authorized_by, ''),
    COALESCE(g.issued_to, ''),
    'legacy_gate_pass',
    -- date_out has no typed home on a slip and is the only field that would be
    -- lost outright, so keep the original string where it can still be read.
    jsonb_build_object('legacy_date_out', COALESCE(g.date_out, ''),
                       'legacy_status', COALESCE(g.status, '')),
    g.created_at,
    g.updated_at
FROM wh_gate_passes g
WHERE NOT EXISTS (SELECT 1 FROM wh_slips s WHERE s.id = g.id);

-- The flat model held everything on one line of free text. One line per pass
-- preserves it verbatim; nobody can retro-fit per-item serials that were never
-- captured, and inventing structure here would fabricate detail.
INSERT INTO wh_slip_lines (slip_id, description, qty, uom)
SELECT g.id, COALESCE(NULLIF(TRIM(g.items), ''), 'unspecified (legacy gate pass)'), 1, 'ea'
FROM wh_gate_passes g
WHERE NOT EXISTS (SELECT 1 FROM wh_slip_lines l WHERE l.slip_id = g.id);

-- An event per migrated pass, so the slip's own history says where it came
-- from rather than appearing to have sprung into being fully formed.
INSERT INTO wh_slip_events (slip_id, event, actor_name, notes, created_at)
SELECT g.id, 'migrated', 'system',
       'Imported from wh_gate_passes (migration 028); original status ' || COALESCE(g.status, ''),
       g.updated_at
FROM wh_gate_passes g
WHERE NOT EXISTS (SELECT 1 FROM wh_slip_events e WHERE e.slip_id = g.id AND e.event = 'migrated');

DROP TABLE IF EXISTS wh_gate_passes;

DROP FUNCTION IF EXISTS wh_try_date(TEXT);
