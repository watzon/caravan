-- Acquisition policy defaults reproduce the historical quality-profile score.
-- JSON arrays are stored as TEXT, as quality_profiles.items already is.
ALTER TABLE quality_profiles ADD COLUMN preferred_sources TEXT NOT NULL DEFAULT '[]';
ALTER TABLE quality_profiles ADD COLUMN proper_repack_preference TEXT NOT NULL DEFAULT 'prefer';
ALTER TABLE quality_profiles ADD COLUMN min_seeders INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quality_profiles ADD COLUMN min_size_mb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quality_profiles ADD COLUMN max_size_mb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quality_profiles ADD COLUMN custom_formats TEXT NOT NULL DEFAULT '[]';
ALTER TABLE quality_profiles ADD COLUMN tv_profile TEXT NOT NULL DEFAULT 'safe';
ALTER TABLE quality_profiles ADD COLUMN tv_compatibility_policy TEXT NOT NULL DEFAULT 'ignore';

-- Rows upgraded from before this migration keep the old source ladder and
-- PROPER/REPACK bonuses, with no new rejection thresholds or format scores.
UPDATE quality_profiles
SET preferred_sources = '[]',
    proper_repack_preference = 'prefer',
    min_seeders = 0,
    min_size_mb = 0,
    max_size_mb = 0,
    custom_formats = '[]',
    tv_profile = 'safe',
    tv_compatibility_policy = 'ignore';
