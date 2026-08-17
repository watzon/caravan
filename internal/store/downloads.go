package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/uptrace/bun"
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

	model := downloadModelFromCore(d)
	_, err := s.db.NewInsert().Model(&model).
		On("CONFLICT (engine_id) DO UPDATE").
		Set("grab_id = CASE WHEN EXCLUDED.grab_id = 0 THEN download.grab_id ELSE EXCLUDED.grab_id END").
		Set("engine = EXCLUDED.engine").Set("title = EXCLUDED.title").
		Set("state = EXCLUDED.state").Set("progress = EXCLUDED.progress").
		Set("bytes_done = EXCLUDED.bytes_done").Set("size = EXCLUDED.size").
		Set("max_down_rate = EXCLUDED.max_down_rate").Set("max_up_rate = EXCLUDED.max_up_rate").
		Set("output_path = EXCLUDED.output_path").Set("error = EXCLUDED.error").
		Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: upsert download %q: %w", d.EngineID, err)
	}
	if d.ID != 0 {
		s.note("downloads")
		return nil
	}
	// Read back both id and created_at: on conflict the insert's created_at was
	// discarded, and the caller's copy must not claim the row is newer than it
	// is.
	err = s.db.NewSelect().Model(&model).Column("id", "created_at").
		Where("engine_id = ?", string(d.EngineID)).Scan(ctx)
	if err != nil {
		return fmt.Errorf("store: upsert download %q: %w", d.EngineID, err)
	}
	d.ID = model.ID
	d.CreatedAt = parseTime(model.CreatedAt)
	s.note("downloads")
	return nil
}

// GetDownloadByEngineID returns the persisted download with the given engine
// handle, or ErrNotFound.
func (s *Store) GetDownloadByEngineID(ctx context.Context, engineID core.DownloadID) (*core.Download, error) {
	var model downloadModel
	err := s.db.NewSelect().Model(&model).Where("engine_id = ?", string(engineID)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: download %q: %w", engineID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get download %q: %w", engineID, err)
	}
	out := model.core()
	return &out, nil
}

// ListDownloads returns every known download, newest first. These are the rows
// the queue is rebuilt from after a restart, before the engine reports live
// status.
func (s *Store) ListDownloads(ctx context.Context) ([]core.Download, error) {
	return s.listDownloadModels(ctx, s.db.NewSelect().Model((*downloadModel)(nil)).Order("id DESC"))
}

// ListDownloadsPage returns one persisted download page, newest first.
func (s *Store) ListDownloadsPage(ctx context.Context, limit int, beforeID int64) ([]core.Download, int64, error) {
	if limit <= 0 {
		return []core.Download{}, 0, nil
	}
	models := []downloadModel{}
	query := s.db.NewSelect().Model(&models).Order("id DESC").Limit(limit + 1)
	if beforeID > 0 {
		query.Where("id < ?", beforeID)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, 0, fmt.Errorf("store: list download page: %w", err)
	}
	more := len(models) > limit
	if more {
		models = models[:limit]
	}
	out := make([]core.Download, len(models))
	for i := range models {
		out[i] = models[i].core()
	}
	if more {
		return out, out[len(out)-1].ID, nil
	}
	return out, 0, nil
}

// ListDownloadsForGrab returns the downloads a grab started. Usually zero or
// one row; it is what an item removal walks to withdraw the in-flight work.
func (s *Store) ListDownloadsForGrab(ctx context.Context, grabID int64) ([]core.Download, error) {
	return s.listDownloadModels(ctx, s.db.NewSelect().Model((*downloadModel)(nil)).
		Where("grab_id = ?", grabID).Order("id ASC"))
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
		models := []downloadModel{}
		if err := s.db.NewSelect().Model(&models).
			Where("grab_id IN (?)", bun.In(grabIDs[start:end])).
			Order("id DESC").Scan(ctx); err != nil {
			return nil, fmt.Errorf("store: list downloads for grabs: %w", err)
		}
		for _, model := range models {
			d := model.core()
			byID[d.ID] = d
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
	if _, err := s.db.NewDelete().Model((*downloadModel)(nil)).Where("engine_id = ?", string(engineID)).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete download %q: %w", engineID, err)
	}
	return nil
}

func (s *Store) listDownloadModels(ctx context.Context, query *bun.SelectQuery) ([]core.Download, error) {
	models := []downloadModel{}
	if err := query.Model(&models).Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list downloads: %w", err)
	}
	out := make([]core.Download, len(models))
	for i := range models {
		out[i] = models[i].core()
	}
	return out, nil
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
