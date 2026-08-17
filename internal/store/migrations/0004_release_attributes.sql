-- +goose Up

-- Non-canonical Torznab/Newznab result attributes are protocol metadata rather
-- than first-class release fields. JSON preserves the bounded, ordered bag
-- across cached search results without changing existing canonical columns.
ALTER TABLE releases ADD COLUMN attributes TEXT NOT NULL DEFAULT '';
