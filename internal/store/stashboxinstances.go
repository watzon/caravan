package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const stashboxInstanceColumns = `id, provider_id, name, endpoint, api_key, created_at, updated_at`

// UpsertStashboxInstance inserts or updates in and writes back the assigned ID.
// Identity is in.ID when set; otherwise a new instance is inserted.
//
// provider_id is deliberately absent from the update: it is the value every
// pinned row and every provider chain stores, so it is the instance's identity
// rather than one of its fields. The endpoint is writable here and refused at
// the API door, which is where "immutable after creation" is enforced — the
// store's job is to describe the table, and a restore or a repair path that
// legitimately rewrites a row must not have to go around it.
func (s *Store) UpsertStashboxInstance(ctx context.Context, in *core.StashboxInstance) error {
	ts := formatTime(now())

	if in.ID != 0 {
		res, err := s.db.ExecContext(ctx, `
			UPDATE stashbox_instances SET name = ?, endpoint = ?, api_key = ?, updated_at = ?
			WHERE id = ?`,
			in.Name, in.Endpoint, in.APIKey, ts, in.ID)
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

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO stashbox_instances (provider_id, name, endpoint, api_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		in.ProviderID, in.Name, in.Endpoint, in.APIKey, ts, ts)
	if err != nil {
		return fmt.Errorf("store: insert stash-box instance %q: %w", in.ProviderID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert stash-box instance %q: %w", in.ProviderID, err)
	}
	in.ID = id
	return nil
}

// GetStashboxInstance returns the instance with the given id, or ErrNotFound.
func (s *Store) GetStashboxInstance(ctx context.Context, id int64) (*core.StashboxInstance, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+stashboxInstanceColumns+" FROM stashbox_instances WHERE id = ?", id)
	in, err := scanStashboxInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: stash-box instance %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get stash-box instance %d: %w", id, err)
	}
	return in, nil
}

// GetStashboxInstanceByProviderID returns the instance a provider id names, or
// ErrNotFound. This is the lookup every pinned item resolves through: the id on
// the row is the question, and a missing answer is a gone instance rather than
// a reason to fall back to another box.
func (s *Store) GetStashboxInstanceByProviderID(ctx context.Context, providerID string) (*core.StashboxInstance, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+stashboxInstanceColumns+" FROM stashbox_instances WHERE provider_id = ?", providerID)
	in, err := scanStashboxInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: stash-box instance %q: %w", providerID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get stash-box instance %q: %w", providerID, err)
	}
	return in, nil
}

// ListStashboxInstances returns every configured instance, oldest first. Id
// order is creation order, which puts the legacy instance — the endpoint that
// was configured before there were instances — at the head of the list it has
// always been the only member of.
func (s *Store) ListStashboxInstances(ctx context.Context) ([]core.StashboxInstance, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+stashboxInstanceColumns+" FROM stashbox_instances ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("store: list stash-box instances: %w", err)
	}
	defer rows.Close()

	out := []core.StashboxInstance{}
	for rows.Next() {
		in, err := scanStashboxInstance(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan stash-box instance: %w", err)
		}
		out = append(out, *in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list stash-box instances: %w", err)
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
	if _, err := s.db.ExecContext(ctx, "DELETE FROM stashbox_instances WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete stash-box instance %d: %w", id, err)
	}
	return nil
}

// CountLibrariesUsingProvider reports how many libraries name providerID —
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
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM libraries
		WHERE provider = ? OR providers LIKE ?`,
		providerID, `%"`+providerID+`"%`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count libraries using provider %q: %w", providerID, err)
	}
	return n, nil
}

// CountItemsPinnedToProvider reports how many movies and series are pinned to
// providerID — the rows whose refs only this provider can be asked about.
func (s *Store) CountItemsPinnedToProvider(ctx context.Context, providerID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM movies WHERE provider = ?)
		     + (SELECT COUNT(*) FROM series WHERE provider = ?)`,
		providerID, providerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count items pinned to provider %q: %w", providerID, err)
	}
	return n, nil
}

func scanStashboxInstance(sc scanner) (*core.StashboxInstance, error) {
	var (
		in                   core.StashboxInstance
		createdAt, updatedAt string
	)
	if err := sc.Scan(&in.ID, &in.ProviderID, &in.Name, &in.Endpoint, &in.APIKey,
		&createdAt, &updatedAt); err != nil {
		return nil, err
	}
	in.CreatedAt = parseTime(createdAt)
	in.UpdatedAt = parseTime(updatedAt)
	return &in, nil
}
