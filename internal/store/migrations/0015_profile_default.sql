-- The profile that happened to be created first was Caravan's original global
-- default. Preserve that behavior on upgrade, but record it explicitly so
-- later profile ordering cannot change what unassigned items resolve to.
--
-- This does not rewrite profile rows: the chosen id is copied into settings.
INSERT INTO settings (key, value, updated_at)
SELECT 'default_quality_profile_id', CAST(id AS TEXT),
       strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM quality_profiles
ORDER BY id
LIMIT 1;
