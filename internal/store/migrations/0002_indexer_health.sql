-- +goose Up

-- Health of each configured search source. A failing indexer is still enabled
-- until consecutive_failures reaches the disable threshold; search skips it
-- as soon as health_error is set so one dead host cannot stall a fan-out.

ALTER TABLE indexers ADD COLUMN health_error TEXT NOT NULL DEFAULT '';
ALTER TABLE indexers ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE indexers ADD COLUMN last_health_at TEXT NOT NULL DEFAULT '';
