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
