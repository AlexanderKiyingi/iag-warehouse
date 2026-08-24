-- 025: an issue must name the document that caused it.
--
-- Principle P3 of the IMS specification: no stock moves without a reference.
-- Until now the only mandatory field on an issue was `department`, which
-- records who took the stock rather than what consumed it. The application
-- enforces this from now on (WAREHOUSE_ISSUE_REQUIRE_REFERENCE, default on);
-- this constraint is the backstop for anything that writes to the table by
-- another route.
--
-- NOT VALID is deliberate and is the only safe option here. Existing rows
-- predate the rule and some of them will violate it; validating would abort
-- the migration and take the service down on boot. NOT VALID applies the
-- check to every INSERT and UPDATE from now on while leaving history alone,
-- which is exactly the intent — we are closing the door, not rewriting what
-- already walked through it.
--
-- To adopt history once it has been attributed, run:
--   ALTER TABLE wh_issues VALIDATE CONSTRAINT wh_issues_has_reference;
-- It will fail loudly and name nothing that is still unreferenced, so use the
-- view below to find them first. Statements are split on ";\n\n" by the migrator.

ALTER TABLE wh_issues DROP CONSTRAINT IF EXISTS wh_issues_has_reference;

ALTER TABLE wh_issues ADD CONSTRAINT wh_issues_has_reference CHECK (
    COALESCE(NULLIF(TRIM(cost_center), ''), '') <> ''
 OR COALESCE(NULLIF(TRIM(production_order_ref), ''), '') <> ''
 OR COALESCE(NULLIF(TRIM(work_order_ref), ''), '') <> ''
) NOT VALID;

-- The remediation worklist. Anonymous issues are not deleted or back-filled
-- with a guess: somebody who knows what the stock was for has to say so, and
-- until they do the rows should be visible rather than quietly tidied away.
CREATE OR REPLACE VIEW wh_unreferenced_issues AS
SELECT id,
       status,
       department,
       notes,
       posted_at,
       created_by,
       created_at
FROM wh_issues
WHERE COALESCE(NULLIF(TRIM(cost_center), ''), '') = ''
  AND COALESCE(NULLIF(TRIM(production_order_ref), ''), '') = ''
  AND COALESCE(NULLIF(TRIM(work_order_ref), ''), '') = '';
