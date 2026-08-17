-- +goose Up

-- A non-empty definition_id routes this indexer through Caravan's local
-- Cardigann-compatible engine. Settings are stored as JSON and are write-only at
-- the HTTP boundary because private definitions can put credentials in them.
ALTER TABLE indexers ADD COLUMN definition_id TEXT NOT NULL DEFAULT '';
ALTER TABLE indexers ADD COLUMN settings TEXT NOT NULL DEFAULT '{}';
