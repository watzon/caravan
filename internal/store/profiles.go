package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// DefaultQualityProfileName is the profile seeded by migration 0001, so a
// first run always has something to assign to a new library item.
const DefaultQualityProfileName = "Standard"

const qualityProfileColumns = `id, name, cutoff, items, upgrade_allowed, created_at, updated_at`

// CreateQualityProfile inserts p and writes back the assigned ID. The name
// must be unique; a duplicate is a store error the API layer turns into a 409.
func (s *Store) CreateQualityProfile(ctx context.Context, p *core.QualityProfile) error {
	items, err := json.Marshal(p.Items)
	if err != nil {
		return fmt.Errorf("store: encode items of profile %q: %w", p.Name, err)
	}
	ts := formatTime(now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO quality_profiles (name, cutoff, items, upgrade_allowed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, p.Cutoff, string(items), p.UpgradeAllowed, ts, ts)
	if err != nil {
		return fmt.Errorf("store: create quality profile %q: %w", p.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create quality profile %q: %w", p.Name, err)
	}
	p.ID = id
	return nil
}

// UpdateQualityProfile rewrites the mutable fields of an existing profile.
// Updating an absent profile is ErrNotFound.
func (s *Store) UpdateQualityProfile(ctx context.Context, p *core.QualityProfile) error {
	items, err := json.Marshal(p.Items)
	if err != nil {
		return fmt.Errorf("store: encode items of profile %q: %w", p.Name, err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE quality_profiles SET name = ?, cutoff = ?, items = ?, upgrade_allowed = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.Cutoff, string(items), p.UpgradeAllowed, formatTime(now()), p.ID)
	if err != nil {
		return fmt.Errorf("store: update quality profile %d: %w", p.ID, err)
	}
	return affectedOne(res, "quality profile", p.ID)
}

// DeleteQualityProfile removes a profile. Items assigned to it keep their
// dangling profile id and fall back to the default (see ResolveQualityProfile):
// quality_profile_id is a soft reference precisely so deleting a profile can
// never orphan a library item (0001's schema note).
func (s *Store) DeleteQualityProfile(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM quality_profiles WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete quality profile %d: %w", id, err)
	}
	return affectedOne(res, "quality profile", id)
}

// ResolveQualityProfile returns the effective profile for an item: the named
// one when id is positive and exists, the default (oldest) profile otherwise.
// A library item with quality_profile_id 0, or one pointing at a deleted
// profile, must never be profile-less: the default is the safety net.
func (s *Store) ResolveQualityProfile(ctx context.Context, id int64) (*core.QualityProfile, error) {
	if id > 0 {
		p, err := s.GetQualityProfile(ctx, id)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	profiles, err := s.ListQualityProfiles(ctx)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("store: no quality profiles: %w", ErrNotFound)
	}
	return &profiles[0], nil
}

// GetQualityProfile returns the profile with the given id, or ErrNotFound.
func (s *Store) GetQualityProfile(ctx context.Context, id int64) (*core.QualityProfile, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+qualityProfileColumns+" FROM quality_profiles WHERE id = ?", id)
	p, err := scanQualityProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: quality profile %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get quality profile %d: %w", id, err)
	}
	return p, nil
}

// ListQualityProfiles returns every profile ordered by id, so the seeded
// default comes first.
func (s *Store) ListQualityProfiles(ctx context.Context) ([]core.QualityProfile, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+qualityProfileColumns+" FROM quality_profiles ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("store: list quality profiles: %w", err)
	}
	defer rows.Close()

	out := []core.QualityProfile{}
	for rows.Next() {
		p, err := scanQualityProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan quality profile: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list quality profiles: %w", err)
	}
	return out, nil
}

func scanQualityProfile(sc scanner) (*core.QualityProfile, error) {
	var (
		p         core.QualityProfile
		items     string
		createdAt string
		updatedAt string
	)
	if err := sc.Scan(&p.ID, &p.Name, &p.Cutoff, &items, &p.UpgradeAllowed, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if items != "" {
		if err := json.Unmarshal([]byte(items), &p.Items); err != nil {
			return nil, fmt.Errorf("decode items of profile %q: %w", p.Name, err)
		}
	}
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}
