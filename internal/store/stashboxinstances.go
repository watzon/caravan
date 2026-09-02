package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// stashboxInstanceModel is the database representation of a configured
// stash-box endpoint. Stored timestamps stay as strings to preserve the
// store-wide RFC3339Nano parsing behavior.
type stashboxInstanceModel struct {
	bun.BaseModel `bun:"table:stashbox_instances,alias:stashbox_instance"`

	ID         int64 `bun:",pk,autoincrement"`
	ProviderID string
	Name       string
	Endpoint   string
	APIKey     string
	CreatedAt  string
	UpdatedAt  string
}

func (m stashboxInstanceModel) coreValue() core.StashboxInstance {
	return core.StashboxInstance{
		ID:         m.ID,
		ProviderID: m.ProviderID,
		Name:       m.Name,
		Endpoint:   m.Endpoint,
		APIKey:     m.APIKey,
		CreatedAt:  parseTime(m.CreatedAt),
		UpdatedAt:  parseTime(m.UpdatedAt),
	}
}

// These narrow models support the provider-usage aggregate queries without
// exposing database rows through the core API.
type libraryProviderUsageModel struct {
	bun.BaseModel `bun:"table:libraries,alias:library"`

	Provider  string
	Providers string
}

type movieProviderUsageModel struct {
	bun.BaseModel `bun:"table:movies,alias:movie"`

	Provider string
}

type seriesProviderUsageModel struct {
	bun.BaseModel `bun:"table:series,alias:series"`

	Provider string
}

// UpsertStashboxInstance inserts or updates in and writes back the assigned ID.
// Identity is in.ID when set; otherwise a new instance is inserted.
//
// provider_id is deliberately absent from the update: it is the value every
// pinned row and every provider chain stores, so it is the instance's identity
// rather than one of its fields. The endpoint is writable here and refused at
// the API door, which is where "immutable after creation" is enforced. The
// store's job is to describe the table, and a restore or a repair path that
// legitimately rewrites a row must not have to go around it.
func (s *Store) UpsertStashboxInstance(ctx context.Context, in *core.StashboxInstance) error {
	ts := formatTime(now())

	if in.ID != 0 {
		model := &stashboxInstanceModel{
			ID:        in.ID,
			Name:      in.Name,
			Endpoint:  in.Endpoint,
			APIKey:    in.APIKey,
			UpdatedAt: ts,
		}
		res, err := s.db.NewUpdate().Model(model).
			Column("name", "endpoint", "api_key", "updated_at").
			WherePK().Exec(ctx)
		if err != nil {
			return fmt.Errorf("store: update stash-box instance %d: %w", in.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update stash-box instance %d: %w", in.ID, err)
		}
		if n == 0 {
			return fmt.Errorf("store: update stash-box instance %d: %w", in.ID, ErrNotFound)
		}
		return nil
	}

	model := &stashboxInstanceModel{
		ProviderID: in.ProviderID,
		Name:       in.Name,
		Endpoint:   in.Endpoint,
		APIKey:     in.APIKey,
		CreatedAt:  ts,
		UpdatedAt:  ts,
	}
	if _, err := s.db.NewInsert().Model(model).Exec(ctx); err != nil {
		return fmt.Errorf("store: insert stash-box instance %q: %w", in.ProviderID, err)
	}
	in.ID = model.ID
	return nil
}

// GetStashboxInstance returns the instance with the given id, or ErrNotFound.
func (s *Store) GetStashboxInstance(ctx context.Context, id int64) (*core.StashboxInstance, error) {
	var model stashboxInstanceModel
	err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: stash-box instance %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get stash-box instance %d: %w", id, err)
	}
	out := model.coreValue()
	return &out, nil
}

// GetStashboxInstanceByProviderID returns the instance a provider id names, or
// ErrNotFound. This is the lookup every pinned item resolves through: the id on
// the row is the question, and a missing answer is a gone instance rather than
// a reason to fall back to another box.
func (s *Store) GetStashboxInstanceByProviderID(ctx context.Context, providerID string) (*core.StashboxInstance, error) {
	var model stashboxInstanceModel
	err := s.db.NewSelect().Model(&model).Where("provider_id = ?", providerID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: stash-box instance %q: %w", providerID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get stash-box instance %q: %w", providerID, err)
	}
	out := model.coreValue()
	return &out, nil
}

// ListStashboxInstances returns every configured instance, oldest first. Id
// order is creation order, which puts the legacy instance (the endpoint that
// was configured before there were instances) at the head of the list it has
// always been the only member of.
func (s *Store) ListStashboxInstances(ctx context.Context) ([]core.StashboxInstance, error) {
	models := make([]stashboxInstanceModel, 0)
	if err := s.db.NewSelect().Model(&models).Order("id ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list stash-box instances: %w", err)
	}

	out := make([]core.StashboxInstance, 0, len(models))
	for _, model := range models {
		out = append(out, model.coreValue())
	}
	return out, nil
}

// DeleteStashboxInstance removes the instance row.
//
// Nothing cascades. Items pinned to the instance keep their provider id and
// their refs: the reference is soft, exactly as a deleted indexer's is on a
// cached release, so removing an instance loses the ability to refresh those
// items rather than the items themselves. Whether a deletion should be allowed
// at all is a question about use, and CountLibrariesUsingProvider and
// CountItemsPinnedToProvider are what answer it at the door.
func (s *Store) DeleteStashboxInstance(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*stashboxInstanceModel)(nil)).
		Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete stash-box instance %d: %w", id, err)
	}
	return nil
}

// CountLibrariesUsingProvider reports how many libraries name providerID,
// either as the head they identify through or anywhere in their chain.
//
// The chain is JSON and this matches it as text, with the quotes included. That
// is what makes the match exact rather than a prefix: '"stashbox"' does not
// occur inside '"stashbox:stashdb"', because a slug follows the colon and not a
// closing quote. Provider ids are drawn from an alphabet with no quote and no
// backslash in it (core.ValidProviderInstanceID), so nothing can be spelled in
// a way that escapes the delimiters this leans on.
func (s *Store) CountLibrariesUsingProvider(ctx context.Context, providerID string) (int, error) {
	var n int
	err := s.db.NewSelect().Model((*libraryProviderUsageModel)(nil)).
		ColumnExpr("COUNT(*)").
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("provider = ?", providerID).
				WhereOr("providers LIKE ?", `%"`+providerID+`"%`)
		}).
		Scan(ctx, &n)
	if err != nil {
		return 0, fmt.Errorf("store: count libraries using provider %q: %w", providerID, err)
	}
	return n, nil
}

// CountItemsPinnedToProvider reports how many movies and series are pinned to
// providerID. The rows whose refs only this provider can be asked about.
func (s *Store) CountItemsPinnedToProvider(ctx context.Context, providerID string) (int, error) {
	movieCount, err := s.db.NewSelect().Model((*movieProviderUsageModel)(nil)).
		Where("provider = ?", providerID).Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: count items pinned to provider %q: %w", providerID, err)
	}
	seriesCount, err := s.db.NewSelect().Model((*seriesProviderUsageModel)(nil)).
		Where("provider = ?", providerID).Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: count items pinned to provider %q: %w", providerID, err)
	}
	return movieCount + seriesCount, nil
}
