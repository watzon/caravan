-- Phase 3: automation (PLAN phase 3).
--
-- The quality_profiles, grabs and jobs tables already carry everything the
-- automation brain needs (0001 sketched them ahead of the code, 0002
-- reconciled them). What is new is per-download state for the torrent
-- controls the queue grows this phase (PLAN phase 3, task 10).

-- Per-torrent transfer rate limits in bytes per second. 0 means "inherit the
-- engine's global limit" — the common case, so the common case costs nothing.
ALTER TABLE downloads ADD COLUMN max_down_rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE downloads ADD COLUMN max_up_rate   INTEGER NOT NULL DEFAULT 0;

-- Rejected grab candidates are decision history, not grabs: they index by the
-- item they were considered for so the UI can answer "why was this skipped"
-- without scanning the whole table.
CREATE INDEX idx_grabs_status ON grabs (status);
