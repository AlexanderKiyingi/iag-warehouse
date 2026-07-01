-- Small-tools tracker backing the storesiag /tools view. Flat record table
-- (no asset FK) so tagged tools can be loaned to custodians with overdue status.

CREATE TABLE IF NOT EXISTS wh_small_tools (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tag_no     TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL,
    category   TEXT NOT NULL DEFAULT '',
    custodian  TEXT NOT NULL DEFAULT '',
    dept       TEXT NOT NULL DEFAULT '',
    issued     TEXT NOT NULL DEFAULT '',
    return_by  TEXT NOT NULL DEFAULT '',
    condition  TEXT NOT NULL DEFAULT 'Good',
    status     TEXT NOT NULL DEFAULT 'In Store',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_wh_small_tools_status ON wh_small_tools (status);
