-- Absolute (series-wide) episode numbering.
--
-- A column and not a table because it is one integer per episode and a
-- PROVIDER'S fact about that episode: TheTVDB serves absoluteNumber on every
-- episode record it has one for, the way it serves the title and the air date.
-- A provider that serves none leaves 0, and 0 reads as "not known" — which is
-- exactly what every row written before this migration correctly says. An
-- episode nobody ever told us the absolute number of is not an episode whose
-- absolute number is zero, and the default says so without a backfill.
ALTER TABLE episodes ADD COLUMN absolute_number INTEGER NOT NULL DEFAULT 0;

-- Partial for 0024's reason: 0 is not an identity, so the rows that carry it
-- do not belong in an index of identities — most of a non-anime library would
-- otherwise be indexed under one repeated value.
--
-- Deliberately NOT unique. Absolute numbers are upstream's running count and
-- upstream renumbers: a special promoted into the running order, a split cour
-- re-cut, a correction applied to half a series. A unique index would turn any
-- of those into a FAILED refresh — a series left half-written because two rows
-- momentarily disagree about one number nothing keys on.
CREATE INDEX idx_episodes_absolute ON episodes (series_id, absolute_number)
    WHERE absolute_number != 0;
