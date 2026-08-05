package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/watzon/caravan/internal/core"
)

const downloadColumns = `id, grab_id, engine, engine_id, title, state, progress,
	bytes_done, size, max_down_rate, max_up_rate, output_path, error, created_at, updated_at`

// UpsertDownload inserts or updates d and writes back the assigned ID.
//
// Identity is the engine's own handle (EngineID), because that is the only id
// that survives a restart on both sides: the engine reports its downloads by
// handle and this table is how those handles are mapped back to grabs.
//
// A zero GrabID never clears an existing link. Grabs are a concept the download
// engine does not have — core.AddOpts carries no grab id — so every progress
// record an engine writes after the grab handler linked the row reports zero.
// Treating that as "no grab" would unlink the download from the item it was
// fetched for, and the import watcher would then skip it forever.
func (s *Store) UpsertDownload(ctx context.Context, d *core.Download) error {
	if d.EngineID == "" {
		return fmt.Errorf("store: upsert download %q: engine id must not be empty", d.Title)
	}
	ts := now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = ts
	}
	d.UpdatedAt = ts

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO downloads (grab_id, engine, engine_id, title, state, progress,
			bytes_done, size, max_down_rate, max_up_rate, output_path, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (engine_id) DO UPDATE SET
			grab_id = CASE WHEN excluded.grab_id = 0 THEN downloads.grab_id ELSE excluded.grab_id END,
			engine = excluded.engine, title = excluded.title,
			state = excluded.state, progress = excluded.progress,
			bytes_done = excluded.bytes_done, size = excluded.size,
			max_down_rate = excluded.max_down_rate, max_up_rate = excluded.max_up_rate,
			output_path = excluded.output_path, error = excluded.error,
			updated_at = excluded.updated_at`,
		d.GrabID, d.Engine, string(d.EngineID), d.Title, string(d.State), d.Progress,
		d.BytesDone, d.Size, d.MaxDownRate, d.MaxUpRate, d.SavePath, d.Error,
		formatTime(d.CreatedAt), formatTime(d.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: upsert download %q: %w", d.EngineID, err)
	}
	if d.ID != 0 {
		return nil
	}
	// Read back both id and created_at: on conflict the insert's created_at was
	// discarded, and the caller's copy must not claim the row is newer than it
	// is.
	var createdAt string
	err = s.db.QueryRowContext(ctx,
		"SELECT id, created_at FROM downloads WHERE engine_id = ?", string(d.EngineID)).
		Scan(&d.ID, &createdAt)
	if err != nil {
		return fmt.Errorf("store: upsert download %q: %w", d.EngineID, err)
	}
	d.CreatedAt = parseTime(createdAt)
	return nil
}

// GetDownloadByEngineID returns the persisted download with the given engine
// handle, or ErrNotFound.
func (s *Store) GetDownloadByEngineID(ctx context.Context, engineID core.DownloadID) (*core.Download, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+downloadColumns+" FROM downloads WHERE engine_id = ?", string(engineID))
	d, err := scanDownload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: download %q: %w", engineID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get download %q: %w", engineID, err)
	}
	return d, nil
}

// ListDownloads returns every known download, newest first. These are the rows
// the queue is rebuilt from after a restart, before the engine reports live
// status.
func (s *Store) ListDownloads(ctx context.Context) ([]core.Download, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+downloadColumns+" FROM downloads ORDER BY id DESC")
	if err != nil {
		return nil, fmt.Errorf("store: list downloads: %w", err)
	}
	defer rows.Close()

	out := []core.Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan download: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list downloads: %w", err)
	}
	return out, nil
}

// ListDownloadsPage returns one persisted download page, newest first.
func (s *Store) ListDownloadsPage(ctx context.Context, limit int, beforeID int64) ([]core.Download, int64, error) {
	if limit <= 0 {
		return []core.Download{}, 0, nil
	}
	query := "SELECT " + downloadColumns + " FROM downloads"
	args := []any{}
	if beforeID > 0 {
		query += " WHERE id < ?"
		args = append(args, beforeID)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list download page: %w", err)
	}
	defer rows.Close()

	out := make([]core.Download, 0, limit)
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("store: scan download page: %w", err)
		}
		if len(out) < limit {
			out = append(out, *d)
			continue
		}
		return out, out[len(out)-1].ID, nil
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: list download page: %w", err)
	}
	return out, 0, nil
}

// ListDownloadsForGrab returns the downloads a grab started. Usually zero or
// one row; it is what an item removal walks to withdraw the in-flight work.
func (s *Store) ListDownloadsForGrab(ctx context.Context, grabID int64) ([]core.Download, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+downloadColumns+" FROM downloads WHERE grab_id = ? ORDER BY id", grabID)
	if err != nil {
		return nil, fmt.Errorf("store: list downloads for grab %d: %w", grabID, err)
	}
	defer rows.Close()

	out := []core.Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan download: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list downloads for grab %d: %w", grabID, err)
	}
	return out, nil
}

// ListDownloadsForGrabs returns downloads linked to the supplied grab IDs,
// newest first.
func (s *Store) ListDownloadsForGrabs(ctx context.Context, grabIDs []int64) ([]core.Download, error) {
	if len(grabIDs) == 0 {
		return []core.Download{}, nil
	}

	byID := make(map[int64]core.Download)
	for start := 0; start < len(grabIDs); start += sqliteIDQueryBatchSize {
		end := min(start+sqliteIDQueryBatchSize, len(grabIDs))
		args := make([]any, 0, end-start)
		for _, id := range grabIDs[start:end] {
			args = append(args, id)
		}
		query := "SELECT " + downloadColumns + " FROM downloads WHERE grab_id IN (" +
			placeholders(end-start) + ") ORDER BY id DESC"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("store: list downloads for grabs: %w", err)
		}
		for rows.Next() {
			d, err := scanDownload(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: scan download for grabs: %w", err)
			}
			byID[d.ID] = *d
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: list downloads for grabs: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("store: list downloads for grabs: %w", err)
		}
	}

	out := make([]core.Download, 0, len(byID))
	for _, d := range byID {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// DeleteDownloadByEngineID forgets a download. It removes a row and nothing
// else: deleting downloaded data is the engine's job, and the library is never
// touched by a download removal (SPEC §13). Deleting an absent handle is not an
// error.
func (s *Store) DeleteDownloadByEngineID(ctx context.Context, engineID core.DownloadID) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM downloads WHERE engine_id = ?", string(engineID)); err != nil {
		return fmt.Errorf("store: delete download %q: %w", engineID, err)
	}
	return nil
}

func scanDownload(sc scanner) (*core.Download, error) {
	var (
		d         core.Download
		engineID  string
		state     string
		createdAt string
		updatedAt string
	)
	err := sc.Scan(&d.ID, &d.GrabID, &d.Engine, &engineID, &d.Title, &state, &d.Progress,
		&d.BytesDone, &d.Size, &d.MaxDownRate, &d.MaxUpRate, &d.SavePath, &d.Error, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	d.EngineID = core.DownloadID(engineID)
	d.State = core.DownloadState(state)
	d.CreatedAt = parseTime(createdAt)
	d.UpdatedAt = parseTime(updatedAt)
	return &d, nil
}
