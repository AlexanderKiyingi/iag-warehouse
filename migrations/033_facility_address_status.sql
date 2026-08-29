-- Facility address and status.
--
-- Every client that renders a facility has asked for both since the first
-- screen was drawn: the form collects an address and an active/inactive state,
-- the service stored neither, so the fields were dropped on write and invented
-- on read — every site reported "Active" with a blank address whatever was
-- typed and whatever was true.
--
-- These are first-class columns rather than attrs keys because status is a
-- control, not a decoration: a closed site should be visible as closed to
-- anything that lists sites, and a JSONB key that only one client knows to look
-- for cannot carry that. Address is a column beside it for the plain reason
-- that it is a property of the site, not an extension of it.
--
-- Both are backfilled with a default rather than left NULL, so existing rows
-- stay valid and no reader has to handle an absent value.

ALTER TABLE wh_facilities
    ADD COLUMN IF NOT EXISTS address TEXT NOT NULL DEFAULT '';

ALTER TABLE wh_facilities
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';

-- NOT VALID, following the convention established in migration 026: existing
-- rows all carry the default and would pass, but validating a CHECK on a table
-- under load takes an ACCESS EXCLUSIVE lock for the length of the scan, and a
-- migration that can block the service on boot is a migration that will.
-- New writes are checked from here on.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'wh_facilities_status_check'
    ) THEN
        ALTER TABLE wh_facilities
            ADD CONSTRAINT wh_facilities_status_check
            CHECK (status IN ('active', 'inactive')) NOT VALID;
    END IF;
END
$$;
