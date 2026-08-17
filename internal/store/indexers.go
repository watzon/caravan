package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

var indexerReadColumns = []string{
	"id", "definition_id", "settings", "name", "url", "api_key", "protocol", "categories", "priority", "enabled",
	"health_error", "consecutive_failures", "last_health_at",
}

var indexerWriteColumns = []string{
	"definition_id", "settings", "name", "url", "api_key", "protocol", "categories", "priority", "enabled",
	"health_error", "consecutive_failures", "last_health_at", "updated_at",
}

// UpsertIndexer inserts or updates c and writes back the assigned ID.
// Identity is c.ID when set; otherwise a new indexer is inserted.
func (s *Store) UpsertIndexer(ctx context.Context, c *core.IndexerConfig) error {
	categories, err := json.Marshal(c.Categories)
	if err != nil {
		return fmt.Errorf("store: encode categories of indexer %q: %w", c.Name, err)
	}
	settings, err := json.Marshal(c.Settings)
	if err != nil {
		return fmt.Errorf("store: encode settings of indexer %q: %w", c.Name, err)
	}
	ts := formatTime(now())
	model := indexerModelFromCore(c, string(categories), string(settings))
	model.UpdatedAt = ts
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin indexer upsert: %w", err)
	}
	defer tx.Rollback()

	if c.ID != 0 {
		// V9 makes a pin and definition_id inseparable. Remove any old pin in
		// this transaction before changing definition_id; rollback restores it if
		// the replacement update or new pin insert fails.
		if _, err := tx.NewDelete().Model((*indexerDefinitionPinModel)(nil)).Where("indexer_id = ?", c.ID).Exec(ctx); err != nil {
			return fmt.Errorf("store: clear indexer %d definition pin: %w", c.ID, err)
		}
		res, err := tx.NewUpdate().Model(model).
			Column(indexerWriteColumns...).
			Where("id = ?", c.ID).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("store: update indexer %d: %w", c.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update indexer %d: %w", c.ID, err)
		}
		if n == 0 {
			return fmt.Errorf("store: update indexer %d: %w", c.ID, ErrNotFound)
		}
		if err := upsertIndexerDefinitionPin(ctx, tx, c); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit indexer %d update: %w", c.ID, err)
		}
		return nil
	}

	model.CreatedAt = ts
	if _, err := tx.NewInsert().Model(model).Exec(ctx); err != nil {
		return fmt.Errorf("store: insert indexer %q: %w", c.Name, err)
	}
	copy := *c
	copy.ID = model.ID
	if err := upsertIndexerDefinitionPin(ctx, tx, &copy); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit indexer %q insert: %w", c.Name, err)
	}
	c.ID = copy.ID
	return nil
}

// GetIndexer returns the indexer with the given id, or ErrNotFound.
func (s *Store) GetIndexer(ctx context.Context, id int64) (*core.IndexerConfig, error) {
	var model indexerModel
	err := s.db.NewSelect().Model(&model).
		Column(indexerReadColumns...).
		Where("id = ?", id).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: indexer %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get indexer %d: %w", id, err)
	}
	indexer, err := model.coreConfig()
	if err != nil {
		return nil, fmt.Errorf("store: get indexer %d: %w", id, err)
	}
	if err := s.loadIndexerDefinitionPin(ctx, &indexer); err != nil {
		return nil, err
	}
	return &indexer, nil
}

// ListIndexers returns every configured indexer in search order.
func (s *Store) ListIndexers(ctx context.Context) ([]core.IndexerConfig, error) {
	return s.listIndexers(ctx, false)
}

// ListEnabledIndexers returns only the indexers search fans out to, in search
// order. A disabled indexer keeps its configuration but is skipped.
func (s *Store) ListEnabledIndexers(ctx context.Context) ([]core.IndexerConfig, error) {
	return s.listIndexers(ctx, true)
}

func (s *Store) listIndexers(ctx context.Context, enabledOnly bool) ([]core.IndexerConfig, error) {
	models := make([]indexerModel, 0)
	query := s.db.NewSelect().Model(&models).
		Column(indexerReadColumns...).
		Order("priority ASC", "name ASC")
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list indexers: %w", err)
	}

	out := make([]core.IndexerConfig, 0, len(models))
	for i := range models {
		indexer, err := models[i].coreConfig()
		if err != nil {
			return nil, fmt.Errorf("store: scan indexer: %w", err)
		}
		if err := s.loadIndexerDefinitionPin(ctx, &indexer); err != nil {
			return nil, err
		}
		out = append(out, indexer)
	}
	return out, nil
}

// DeleteIndexer removes the indexer row. Cached releases keep their
// indexer_id and denormalized name: the reference is soft, so a deleted
// indexer never invalidates history.
func (s *Store) DeleteIndexer(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*indexerModel)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete indexer %d: %w", id, err)
	}
	return nil
}

// RecordIndexerHealth writes the outcome of a probe. A nil err clears the
// failing streak. A non-nil err flags the indexer out of search and, after
// IndexerHealthDisableAfter failures, switches it off.
func (s *Store) RecordIndexerHealth(ctx context.Context, id int64, probeErr error) error {
	stored, err := s.GetIndexer(ctx, id)
	if err != nil {
		return err
	}
	if probeErr == nil {
		stored.HealthError = ""
		stored.ConsecutiveFailures = 0
		stored.LastHealthAt = now()
		return s.UpsertIndexer(ctx, stored)
	}
	stored.HealthError = redactIndexerHealthError(*stored, probeErr)
	stored.ConsecutiveFailures++
	stored.LastHealthAt = now()
	if stored.ConsecutiveFailures >= core.IndexerHealthDisableAfter {
		stored.Enabled = false
	}
	return s.UpsertIndexer(ctx, stored)
}

func redactIndexerHealthError(indexer core.IndexerConfig, probeErr error) string {
	return indexer.RedactSecrets(probeErr.Error())
}

func indexerModelFromCore(c *core.IndexerConfig, categories, settings string) *indexerModel {
	return &indexerModel{
		ID:                  c.ID,
		DefinitionID:        c.DefinitionID,
		Settings:            settings,
		Name:                c.Name,
		URL:                 c.URL,
		APIKey:              c.APIKey,
		Protocol:            c.Type,
		Categories:          categories,
		Priority:            c.Priority,
		Enabled:             c.Enabled,
		HealthError:         c.HealthError,
		ConsecutiveFailures: c.ConsecutiveFailures,
		LastHealthAt:        formatTime(c.LastHealthAt),
	}
}

func (m *indexerModel) coreConfig() (core.IndexerConfig, error) {
	indexer := core.IndexerConfig{
		ID:                  m.ID,
		DefinitionID:        m.DefinitionID,
		Name:                m.Name,
		URL:                 m.URL,
		APIKey:              m.APIKey,
		Type:                m.Protocol,
		Priority:            m.Priority,
		Enabled:             m.Enabled,
		HealthError:         m.HealthError,
		ConsecutiveFailures: m.ConsecutiveFailures,
		LastHealthAt:        parseTime(m.LastHealthAt),
	}
	if m.Settings != "" {
		if err := json.Unmarshal([]byte(m.Settings), &indexer.Settings); err != nil {
			return core.IndexerConfig{}, fmt.Errorf("decode settings of indexer %q: %w", m.Name, err)
		}
	}
	if m.Categories != "" {
		if err := json.Unmarshal([]byte(m.Categories), &indexer.Categories); err != nil {
			return core.IndexerConfig{}, fmt.Errorf("decode categories of indexer %q: %w", m.Name, err)
		}
	}
	return indexer, nil
}
