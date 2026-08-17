-- 024: equipment handover slips and cargo gate passes as controlled documents.
--
-- wh_gate_passes (migration 010) is a flat note: five text fields, no line
-- items, no link to the stock or the asset that left, no authorisation, and
-- nothing for the guard on the gate to check against. It records that somebody
-- typed something. It is left exactly as it is because storesiag reads it, but
-- it is not what a gate pass has to be.
--
-- A slip here is a controlled document. It has a number from a real sequence, an
-- author who is not its authoriser, itemised lines that point at actual items
-- and assets, a token the guard verifies at the barrier, a record of who
-- released it and when, and — for anything that is meant to come back — a return
-- that can be outstanding and therefore chased.
--
-- Two kinds share the table because they share almost everything that matters:
--   equipment_handover — custody of equipment moving between people or sites
--   cargo_gate_pass    — goods leaving the premises, shown to security at the gate
-- The columns that differ are the party columns (a handover has custodians, a
-- cargo pass has a driver and a vehicle), and keeping one table means one number
-- series, one authorisation path, one verification endpoint and one print
-- renderer rather than two of each drifting apart.
--
-- verify_token is what the printed barcode encodes. It is separate from slip_no
-- because the number is meant to be quoted out loud and written in a ledger,
-- while the token is what proves the slip in a guard's hand is the one this
-- system issued — so it is random, unguessable, and issued once at authorisation
-- rather than derivable from anything on the face of the document.

CREATE TABLE IF NOT EXISTS wh_document_counters (
    scope      TEXT PRIMARY KEY,
    next_value BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_slips (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slip_no      TEXT UNIQUE,
    slip_type    TEXT NOT NULL CHECK (slip_type IN ('equipment_handover', 'cargo_gate_pass')),
    status       TEXT NOT NULL DEFAULT 'draft' CHECK (status IN (
        'draft', 'issued', 'released', 'returned', 'closed', 'rejected', 'cancelled'
    )),
    purpose      TEXT NOT NULL DEFAULT '',
    notes        TEXT NOT NULL DEFAULT '',
    facility_id  UUID REFERENCES wh_facilities(id) ON DELETE SET NULL,

    issued_to_name TEXT NOT NULL DEFAULT '',
    issued_to_id   UUID,
    dept           TEXT NOT NULL DEFAULT '',
    from_custodian TEXT NOT NULL DEFAULT '',
    to_custodian   TEXT NOT NULL DEFAULT '',

    driver_name  TEXT NOT NULL DEFAULT '',
    driver_id_no TEXT NOT NULL DEFAULT '',
    vehicle_reg  TEXT NOT NULL DEFAULT '',
    transporter  TEXT NOT NULL DEFAULT '',
    destination  TEXT NOT NULL DEFAULT '',

    returnable         BOOLEAN NOT NULL DEFAULT FALSE,
    return_by          DATE,
    returned_at        TIMESTAMPTZ,
    returned_condition TEXT NOT NULL DEFAULT '',

    requested_by   UUID,
    requested_name TEXT NOT NULL DEFAULT '',
    authorized_by  UUID,
    authorized_name TEXT NOT NULL DEFAULT '',
    authorized_at  TIMESTAMPTZ,
    reject_reason  TEXT NOT NULL DEFAULT '',

    verify_token     TEXT UNIQUE,
    gate_name        TEXT NOT NULL DEFAULT '',
    gate_verified_by UUID,
    gate_verified_name TEXT NOT NULL DEFAULT '',
    gate_verified_at TIMESTAMPTZ,
    gate_notes       TEXT NOT NULL DEFAULT '',

    issue_id     UUID REFERENCES wh_issues(id) ON DELETE SET NULL,
    pick_list_id UUID REFERENCES wh_pick_lists(id) ON DELETE SET NULL,
    hu_id        UUID REFERENCES wh_handling_units(id) ON DELETE SET NULL,
    ref_type     TEXT NOT NULL DEFAULT '',
    ref_id       TEXT NOT NULL DEFAULT '',

    attrs      JSONB NOT NULL DEFAULT '{}',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT wh_slips_issued_has_number
        CHECK (status IN ('draft', 'rejected', 'cancelled') OR slip_no IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS wh_slips_status_idx ON wh_slips (status, created_at DESC);

CREATE INDEX IF NOT EXISTS wh_slips_type_idx ON wh_slips (slip_type, created_at DESC);

-- Outstanding returnables are the whole point of tracking: this is the index the
-- overdue query rides.
CREATE INDEX IF NOT EXISTS wh_slips_outstanding_idx
    ON wh_slips (return_by)
    WHERE returnable AND status IN ('issued', 'released');

CREATE TABLE IF NOT EXISTS wh_slip_lines (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slip_id       UUID NOT NULL REFERENCES wh_slips(id) ON DELETE CASCADE,
    item_id       UUID REFERENCES wh_items(id) ON DELETE SET NULL,
    asset_id      UUID REFERENCES wh_assets(id) ON DELETE SET NULL,
    description   TEXT NOT NULL,
    qty           NUMERIC(18, 3) NOT NULL DEFAULT 1 CHECK (qty > 0),
    uom           TEXT NOT NULL DEFAULT 'ea',
    serial_no     TEXT NOT NULL DEFAULT '',
    lot_key       TEXT NOT NULL DEFAULT '',
    condition_out TEXT NOT NULL DEFAULT '',
    condition_in  TEXT NOT NULL DEFAULT '',
    returned_qty  NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (returned_qty >= 0),
    attrs         JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS wh_slip_lines_slip_idx ON wh_slip_lines (slip_id);

CREATE TABLE IF NOT EXISTS wh_slip_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slip_id    UUID NOT NULL REFERENCES wh_slips(id) ON DELETE CASCADE,
    event      TEXT NOT NULL,
    actor_id   UUID,
    actor_name TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS wh_slip_events_slip_idx ON wh_slip_events (slip_id, created_at);
