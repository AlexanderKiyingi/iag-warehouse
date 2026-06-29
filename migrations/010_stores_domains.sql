-- Stores-domain record tables backing the storesiag workspace views that had
-- no warehouse home: stock-alert thresholds, stock returns, gate passes,
-- equipment warranties, and event/furniture requests. These are flat
-- record-keeping tables (no hard item/bin FKs) so the stores frontend's generic
-- forms can create and edit them directly. Dates are stored as ISO text to keep
-- the round-trip with the flat forms friction-free.

CREATE TABLE IF NOT EXISTS wh_stock_thresholds (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item         TEXT NOT NULL,
    dept         TEXT NOT NULL DEFAULT '',
    current_qty  NUMERIC(18, 3) NOT NULL DEFAULT 0,
    min_qty      NUMERIC(18, 3) NOT NULL DEFAULT 0,
    reorder_qty  NUMERIC(18, 3) NOT NULL DEFAULT 0,
    alert_method TEXT NOT NULL DEFAULT 'System',
    status       TEXT NOT NULL DEFAULT 'Active',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_returns (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item        TEXT NOT NULL,
    sku         TEXT NOT NULL DEFAULT '',
    qty         NUMERIC(18, 3) NOT NULL DEFAULT 0,
    returned_by TEXT NOT NULL DEFAULT '',
    condition   TEXT NOT NULL DEFAULT '',
    linked_ref  TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'Pending',
    notes       TEXT NOT NULL DEFAULT '',
    return_date TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_gate_passes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    gate_pass_no  TEXT NOT NULL DEFAULT '',
    items         TEXT NOT NULL DEFAULT '',
    issued_to     TEXT NOT NULL DEFAULT '',
    dept          TEXT NOT NULL DEFAULT '',
    purpose       TEXT NOT NULL DEFAULT '',
    date_out      TEXT NOT NULL DEFAULT '',
    return_by     TEXT NOT NULL DEFAULT '',
    return_date   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'On Loan',
    authorized_by TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_warranties (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item          TEXT NOT NULL,
    supplier      TEXT NOT NULL DEFAULT '',
    asset_ref     TEXT NOT NULL DEFAULT '',
    purchase_date TEXT NOT NULL DEFAULT '',
    expiry_date   TEXT NOT NULL DEFAULT '',
    duration      TEXT NOT NULL DEFAULT '',
    covers        TEXT NOT NULL DEFAULT '',
    contact       TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'Active',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_event_requests (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_name   TEXT NOT NULL,
    items        TEXT NOT NULL DEFAULT '',
    qty          NUMERIC(18, 3) NOT NULL DEFAULT 0,
    dept         TEXT NOT NULL DEFAULT '',
    requested_by TEXT NOT NULL DEFAULT '',
    needed_by    TEXT NOT NULL DEFAULT '',
    return_date  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'Requested',
    notes        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
