-- Warehouse schema: locations, items, balances, movements, integration tables.

CREATE TABLE IF NOT EXISTS wh_facilities (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    site_type  TEXT NOT NULL CHECK (site_type IN ('mill', 'fg_warehouse', 'workshop', 'yard')),
    attrs      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_zones (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id  UUID NOT NULL REFERENCES wh_facilities(id),
    code         TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    zone_type    TEXT NOT NULL CHECK (zone_type IN ('receiving', 'bulk', 'cold', 'quarantine', 'staging', 'assets')),
    attrs        JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (facility_id, code)
);

CREATE TABLE IF NOT EXISTS wh_bins (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    zone_id          UUID NOT NULL REFERENCES wh_zones(id),
    code             TEXT NOT NULL,
    capacity_kg      NUMERIC(18, 3),
    temperature_band TEXT,
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    attrs            JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (zone_id, code)
);

CREATE TABLE IF NOT EXISTS wh_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sku             TEXT NOT NULL,
    name            TEXT NOT NULL,
    material_class  TEXT NOT NULL CHECK (material_class IN ('raw_material', 'finished_good', 'consumable', 'spare_part', 'equipment')),
    tracking_mode   TEXT NOT NULL CHECK (tracking_mode IN ('bulk', 'lot', 'sku', 'serial')),
    uom             TEXT NOT NULL DEFAULT 'ea',
    min_qty         NUMERIC(18, 3) NOT NULL DEFAULT 0,
    max_qty         NUMERIC(18, 3),
    attrs           JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS wh_items_sku_idx ON wh_items (sku);
CREATE INDEX IF NOT EXISTS wh_items_material_class_idx ON wh_items (material_class);

CREATE TABLE IF NOT EXISTS wh_lots (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lot_key            TEXT NOT NULL UNIQUE,
    batch_business_id  TEXT,
    expiry_on          DATE,
    origin             TEXT,
    attrs              JSONB NOT NULL DEFAULT '{}',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_stock_balances (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id     UUID NOT NULL REFERENCES wh_items(id),
    bin_id      UUID NOT NULL REFERENCES wh_bins(id),
    lot_key     TEXT NOT NULL DEFAULT '',
    serial_key  TEXT NOT NULL DEFAULT '',
    qty         NUMERIC(18, 3) NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'hold', 'damaged')),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (item_id, bin_id, lot_key, serial_key)
);

CREATE INDEX IF NOT EXISTS wh_stock_balances_item_idx ON wh_stock_balances (item_id);
CREATE INDEX IF NOT EXISTS wh_stock_balances_bin_idx ON wh_stock_balances (bin_id);

CREATE TABLE IF NOT EXISTS wh_assets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_tag       TEXT NOT NULL UNIQUE,
    serial_no       TEXT,
    item_id         UUID NOT NULL REFERENCES wh_items(id),
    current_bin_id  UUID REFERENCES wh_bins(id),
    condition       TEXT NOT NULL DEFAULT 'good',
    book_value_ref  TEXT,
    attrs           JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_spare_compat (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id         UUID NOT NULL REFERENCES wh_items(id),
    asset_type      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (item_id, asset_type)
);

CREATE TABLE IF NOT EXISTS wh_movements (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    movement_type    TEXT NOT NULL CHECK (movement_type IN (
        'receipt', 'issue', 'transfer', 'production_consume', 'production_output',
        'return', 'adjustment', 'asset_checkin', 'asset_checkout'
    )),
    item_id          UUID REFERENCES wh_items(id),
    from_bin_id      UUID REFERENCES wh_bins(id),
    to_bin_id        UUID REFERENCES wh_bins(id),
    qty              NUMERIC(18, 3) NOT NULL DEFAULT 0,
    lot_key          TEXT NOT NULL DEFAULT '',
    serial_key       TEXT NOT NULL DEFAULT '',
    ref_type         TEXT,
    ref_id           UUID,
    batch_business_id TEXT,
    actor_id         UUID,
    occurred_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attrs            JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS wh_movements_occurred_idx ON wh_movements (occurred_at DESC);
CREATE INDEX IF NOT EXISTS wh_movements_ref_idx ON wh_movements (ref_type, ref_id);

CREATE TABLE IF NOT EXISTS wh_receipts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    receipt_type TEXT NOT NULL DEFAULT 'standard',
    status       TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'cancelled')),
    source_ref   TEXT,
    grn_id       TEXT,
    po_id        TEXT,
    notes        TEXT,
    posted_at    TIMESTAMPTZ,
    created_by   UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_receipt_lines (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    receipt_id         UUID NOT NULL REFERENCES wh_receipts(id) ON DELETE CASCADE,
    item_id            UUID NOT NULL REFERENCES wh_items(id),
    qty                NUMERIC(18, 3) NOT NULL,
    uom                TEXT NOT NULL,
    bin_id             UUID NOT NULL REFERENCES wh_bins(id),
    lot_key            TEXT NOT NULL DEFAULT '',
    batch_business_id  TEXT,
    attrs              JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS wh_issues (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status               TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'cancelled')),
    department           TEXT,
    cost_center          TEXT,
    production_order_ref TEXT,
    batch_business_id    TEXT,
    notes                TEXT,
    posted_at            TIMESTAMPTZ,
    created_by           UUID,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_issue_lines (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id   UUID NOT NULL REFERENCES wh_issues(id) ON DELETE CASCADE,
    item_id    UUID NOT NULL REFERENCES wh_items(id),
    qty        NUMERIC(18, 3) NOT NULL,
    uom        TEXT NOT NULL,
    bin_id     UUID NOT NULL REFERENCES wh_bins(id),
    lot_key    TEXT NOT NULL DEFAULT '',
    serial_key TEXT NOT NULL DEFAULT '',
    attrs      JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS wh_transfers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'cancelled')),
    from_facility_id UUID REFERENCES wh_facilities(id),
    to_facility_id   UUID REFERENCES wh_facilities(id),
    notes           TEXT,
    posted_at       TIMESTAMPTZ,
    created_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_transfer_lines (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_id  UUID NOT NULL REFERENCES wh_transfers(id) ON DELETE CASCADE,
    item_id      UUID NOT NULL REFERENCES wh_items(id),
    qty          NUMERIC(18, 3) NOT NULL,
    from_bin_id  UUID NOT NULL REFERENCES wh_bins(id),
    to_bin_id    UUID NOT NULL REFERENCES wh_bins(id),
    lot_key      TEXT NOT NULL DEFAULT '',
    serial_key   TEXT NOT NULL DEFAULT '',
    attrs        JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS wh_adjustments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    adj_type     TEXT NOT NULL CHECK (adj_type IN ('adjustment', 'cycle_count')),
    item_id      UUID NOT NULL REFERENCES wh_items(id),
    bin_id       UUID NOT NULL REFERENCES wh_bins(id),
    lot_key      TEXT NOT NULL DEFAULT '',
    serial_key   TEXT NOT NULL DEFAULT '',
    qty_before   NUMERIC(18, 3) NOT NULL,
    qty_after    NUMERIC(18, 3) NOT NULL,
    reason       TEXT,
    actor_id     UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_pick_lists (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status     TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'confirmed', 'cancelled')),
    order_ref  TEXT,
    notes      TEXT,
    confirmed_at TIMESTAMPTZ,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_pick_lines (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pick_list_id UUID NOT NULL REFERENCES wh_pick_lists(id) ON DELETE CASCADE,
    item_id      UUID NOT NULL REFERENCES wh_items(id),
    qty          NUMERIC(18, 3) NOT NULL,
    bin_id       UUID NOT NULL REFERENCES wh_bins(id),
    lot_key      TEXT NOT NULL DEFAULT '',
    picked_qty   NUMERIC(18, 3) NOT NULL DEFAULT 0,
    attrs        JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS wh_pack_sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pick_list_id UUID REFERENCES wh_pick_lists(id),
    status       TEXT NOT NULL DEFAULT 'open',
    attrs        JSONB NOT NULL DEFAULT '{}',
    created_by   UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wh_external_refs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_service TEXT NOT NULL,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    target_type    TEXT NOT NULL,
    target_id      UUID NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_service, source_type, source_id)
);

CREATE TABLE IF NOT EXISTS wh_idempotency (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id        UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    route           TEXT NOT NULL,
    status_code     INT NOT NULL,
    response_body   JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (actor_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS wh_event_outbox (
    id            BIGSERIAL PRIMARY KEY,
    event_type    TEXT NOT NULL,
    event_key     TEXT,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dispatched_at TIMESTAMPTZ,
    attempts      INT NOT NULL DEFAULT 0,
    last_error    TEXT
);

CREATE INDEX IF NOT EXISTS wh_event_outbox_due_idx
    ON wh_event_outbox (available_at)
    WHERE dispatched_at IS NULL;

CREATE TABLE IF NOT EXISTS wh_api_audit (
    id           BIGSERIAL PRIMARY KEY,
    logged_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    method       TEXT NOT NULL,
    path         TEXT NOT NULL,
    status_code  INT NOT NULL DEFAULT 0,
    user_name    TEXT NOT NULL DEFAULT '',
    duration_ms  INT NOT NULL DEFAULT 0,
    client_ip    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS wh_api_audit_logged_at_idx
    ON wh_api_audit (logged_at DESC);

CREATE TABLE IF NOT EXISTS kafka_dedupe (
    event_id TEXT PRIMARY KEY,
    topic    TEXT NOT NULL,
    seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
