-- Stock-in and stock transfers from the ERP monolith
--
-- Created during the ERP monolith clone, when the tail of the migration turned
-- out to have no platform tables to land in. Recorded here so a fresh
-- environment reproduces them; IF NOT EXISTS makes this a no-op where they
-- already stand.
--
-- Cross-service references (project_id -> finance.projects) are plain columns
-- rather than foreign keys: the services deploy independently and a constraint
-- would couple their migration order. The accompanying *_name column carries
-- the source's own text, so the link survives even where the id does not.

CREATE TABLE IF NOT EXISTS warehouse.wh_stock_in (
    id            UUID PRIMARY KEY,
    reference     TEXT NOT NULL DEFAULT '',
    item_ref      TEXT NOT NULL DEFAULT '',
    location_ref  TEXT NOT NULL DEFAULT '',
    supplier      TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    quantity      NUMERIC(18,4) NOT NULL DEFAULT 0,
    unit_cost     NUMERIC(18,4),
    amount        NUMERIC(18,4),
    stock_value   NUMERIC(18,4),
    currency      TEXT NOT NULL DEFAULT 'UGX',
    status        TEXT NOT NULL DEFAULT '',
    received_on   DATE,
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS warehouse.wh_stock_transfers (
    id             UUID PRIMARY KEY,
    reference      TEXT NOT NULL DEFAULT '',
    item_ref       TEXT NOT NULL DEFAULT '',
    from_location  TEXT NOT NULL DEFAULT '',
    to_location    TEXT NOT NULL DEFAULT '',
    description    TEXT NOT NULL DEFAULT '',
    quantity       NUMERIC(18,4) NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT '',
    transfer_date  DATE,
    received_by    TEXT NOT NULL DEFAULT '',
    received_on    DATE,
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
