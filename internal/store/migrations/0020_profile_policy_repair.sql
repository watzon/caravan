-- Some prerelease databases recorded 0017 before every acquisition-policy
-- column had landed. SQLite has no ALTER TABLE ADD COLUMN IF NOT EXISTS, so
-- applyCompatibilityMigration validates and adds the eight final columns one
-- by one inside the migration transaction. This statement documents the
-- migration in the embedded set; the compatibility handler owns the repair.
SELECT 1;
