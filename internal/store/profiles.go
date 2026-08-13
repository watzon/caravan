package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// DefaultQualityProfileName is the profile seeded by the baseline schema, so a
// first run always has something to assign to a new library item.
const DefaultQualityProfileName = "Standard"

// CreateQualityProfile inserts p and writes back the assigned ID. The name
// must be unique; a duplicate is a store error the API layer turns into a 409.
func (s *Store) CreateQualityProfile(ctx context.Context, p *core.QualityProfile) error {
	items, preferredSources, customFormats, err := qualityProfileJSONValues(p)
	if err != nil {
		return err
	}
	ts := formatTime(now())
	model := qualityProfileStoreModelFromCore(p, items, preferredSources, customFormats)
	model.CreatedAt = ts
	model.UpdatedAt = ts
	if err := s.db.NewInsert().Model(model).Returning("id").Scan(ctx); err != nil {
		return fmt.Errorf("store: create quality profile %q: %w", p.Name, err)
	}
	p.ID = model.ID
	return nil
}

// UpdateQualityProfile rewrites the mutable fields of an existing profile.
// Updating an absent profile is ErrNotFound.
func (s *Store) UpdateQualityProfile(ctx context.Context, p *core.QualityProfile) error {
	items, preferredSources, customFormats, err := qualityProfileJSONValues(p)
	if err != nil {
		return err
	}
	model := qualityProfileStoreModelFromCore(p, items, preferredSources, customFormats)
	model.UpdatedAt = formatTime(now())
	res, err := s.db.NewUpdate().Model(model).
		Column("name", "cutoff", "items", "upgrade_allowed", "preferred_sources",
			"proper_repack_preference", "min_seeders", "min_size_mb", "max_size_mb",
			"custom_formats", "tv_profile", "tv_compatibility_policy", "updated_at").
		WherePK().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: update quality profile %d: %w", p.ID, err)
	}
	return affectedOne(res, "quality profile", p.ID)
}

// ImportQualityProfiles upserts profiles by name and selects defaultName in
// one transaction. Existing ids are retained, so current assignments remain
// valid. Profiles absent from the import are not changed.
func (s *Store) ImportQualityProfiles(ctx context.Context, profiles []core.QualityProfile, defaultName string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin importing quality profiles: %w", err)
	}
	defer tx.Rollback()

	for i := range profiles {
		p := &profiles[i]
		items, preferredSources, customFormats, err := qualityProfileJSONValues(p)
		if err != nil {
			return err
		}
		model := qualityProfileStoreModelFromCore(p, items, preferredSources, customFormats)
		var existing qualityProfileStoreModel
		err = tx.NewSelect().Model(&existing).Column("id").Where("name = ?", p.Name).Scan(ctx)
		switch {
		case err == nil:
			model.ID = existing.ID
			model.UpdatedAt = formatTime(now())
			if _, err := tx.NewUpdate().Model(model).
				Column("cutoff", "items", "upgrade_allowed", "preferred_sources", "proper_repack_preference",
					"min_seeders", "min_size_mb", "max_size_mb", "custom_formats", "tv_profile",
					"tv_compatibility_policy", "updated_at").
				WherePK().Exec(ctx); err != nil {
				return fmt.Errorf("store: import quality profile %q: %w", p.Name, err)
			}
			p.ID = existing.ID
		case errors.Is(err, sql.ErrNoRows):
			ts := formatTime(now())
			model.CreatedAt = ts
			model.UpdatedAt = ts
			if err := tx.NewInsert().Model(model).Returning("id").Scan(ctx); err != nil {
				return fmt.Errorf("store: import quality profile %q: %w", p.Name, err)
			}
			p.ID = model.ID
		default:
			return fmt.Errorf("store: find imported quality profile %q: %w", p.Name, err)
		}
	}

	var defaultProfile qualityProfileStoreModel
	if err := tx.NewSelect().Model(&defaultProfile).Column("id").Where("name = ?", defaultName).Scan(ctx); err != nil {
		return fmt.Errorf("store: find imported default quality profile %q: %w", defaultName, err)
	}
	if err := s.setDefaultQualityProfile(ctx, tx, defaultProfile.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: import quality profiles: %w", err)
	}
	return nil
}

// QualityProfileReferenceCounts describes the rows that select one profile.
// The profile ids are intentionally soft references, but a selected profile is
// configuration a user has made and must be replaced before it can be removed.
type QualityProfileReferenceCounts struct {
	Libraries int64
	Movies    int64
	Series    int64
}

func (c QualityProfileReferenceCounts) any() bool {
	return c.Libraries != 0 || c.Movies != 0 || c.Series != 0
}

// GetQualityProfileReferenceCounts returns the rows that explicitly select a
// profile. It shares DeleteQualityProfile's accounting so the profile summary
// and deletion safety always agree.
func (s *Store) GetQualityProfileReferenceCounts(ctx context.Context, id int64) (QualityProfileReferenceCounts, error) {
	return s.qualityProfileReferenceCounts(ctx, s.db, id)
}

// QualityProfileDeleteConflict explains why DeleteQualityProfile preserved a
// profile. Callers can turn it into a useful 409 rather than treating it as a
// generic storage failure.
type QualityProfileDeleteConflict struct {
	Default    bool
	References QualityProfileReferenceCounts
}

func (e *QualityProfileDeleteConflict) Error() string {
	if e.Default {
		return "store: the system default quality profile cannot be deleted"
	}
	return fmt.Sprintf("store: quality profile is referenced by %d libraries, %d movies, and %d series",
		e.References.Libraries, e.References.Movies, e.References.Series)
}

// DeleteQualityProfile removes an unselected, non-default profile. The check
// and deletion share one transaction so no request can create a dangling
// configuration reference between them.
func (s *Store) DeleteQualityProfile(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin deleting quality profile %d: %w", id, err)
	}
	defer tx.Rollback()

	refs, err := s.qualityProfileReferenceCounts(ctx, tx, id)
	if err != nil {
		return err
	}
	defaultProfile, err := s.defaultQualityProfile(ctx, tx)
	if err != nil {
		return err
	}
	if defaultProfile.ID == id {
		return &QualityProfileDeleteConflict{Default: true, References: refs}
	}
	if refs.any() {
		return &QualityProfileDeleteConflict{References: refs}
	}

	res, err := tx.NewDelete().Model((*qualityProfileStoreModel)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: delete quality profile %d: %w", id, err)
	}
	if err := affectedOne(res, "quality profile", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete quality profile %d: %w", id, err)
	}
	return nil
}

// ResolveQualityProfile returns the effective system profile for an item or
// library id. ResolveItemQualityProfile applies the item and library steps
// first; this method resolves the final system-default step.
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
	return s.GetDefaultQualityProfile(ctx)
}

// SetDefaultQualityProfile makes an existing profile the explicit system
// default. An id of zero is invalid: inherited defaults resolve to this value,
// they do not replace it.
func (s *Store) SetDefaultQualityProfile(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin setting default quality profile %d: %w", id, err)
	}
	defer tx.Rollback()

	if err := s.setDefaultQualityProfile(ctx, tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: set default quality profile %d: %w", id, err)
	}
	return nil
}

// SetMovieQualityProfile validates and assigns a movie's explicit profile in
// one transaction. Zero clears the override and inherits the library or system
// default.
func (s *Store) SetMovieQualityProfile(ctx context.Context, movieID, profileID int64) error {
	return s.setQualityProfile(ctx, "movie", movieID, profileID)
}

// SetSeriesQualityProfile validates and assigns a series' explicit profile in
// one transaction. Zero clears the override and inherits the library or system
// default.
func (s *Store) SetSeriesQualityProfile(ctx context.Context, seriesID, profileID int64) error {
	return s.setQualityProfile(ctx, "series", seriesID, profileID)
}

// SetLibraryQualityProfile validates and assigns a library's explicit profile
// in one transaction. Zero clears the override and inherits the system default.
func (s *Store) SetLibraryQualityProfile(ctx context.Context, libraryID, profileID int64) error {
	return s.setQualityProfile(ctx, "library", libraryID, profileID)
}

// GetDefaultQualityProfile returns the persisted system default. Missing,
// malformed, and stale settings occur only on upgraded or manually edited
// databases; each is repaired once to the oldest still-valid profile.
func (s *Store) GetDefaultQualityProfile(ctx context.Context) (*core.QualityProfile, error) {
	return s.defaultQualityProfile(ctx, s.db)
}

func (s *Store) setQualityProfile(ctx context.Context, item string, itemID, profileID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin setting %s quality profile: %w", item, err)
	}
	defer tx.Rollback()

	if profileID > 0 {
		if _, err := s.getQualityProfile(ctx, tx, profileID); err != nil {
			return err
		}
	}

	var res sql.Result
	switch item {
	case "movie":
		res, err = tx.NewUpdate().Model((*movieProfileAssignmentStoreModel)(nil)).
			Set("quality_profile_id = ?", profileID).Where("id = ?", itemID).Exec(ctx)
	case "series":
		res, err = tx.NewUpdate().Model((*seriesProfileAssignmentStoreModel)(nil)).
			Set("quality_profile_id = ?", profileID).Where("id = ?", itemID).Exec(ctx)
	case "library":
		res, err = tx.NewUpdate().Model((*libraryProfileAssignmentStoreModel)(nil)).
			Set("quality_profile_id = ?", int64Pointer(profileID)).Where("id = ?", itemID).Exec(ctx)
	default:
		return fmt.Errorf("store: unsupported quality profile target %q", item)
	}
	if err != nil {
		return fmt.Errorf("store: set %s %d quality profile: %w", item, itemID, err)
	}
	if err := affectedOne(res, item, itemID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: set %s %d quality profile: %w", item, itemID, err)
	}
	return nil
}

func (s *Store) defaultQualityProfile(ctx context.Context, db bun.IDB) (*core.QualityProfile, error) {
	var setting settingModel
	err := db.NewSelect().Model(&setting).Column("value").Where("key = ?", SettingDefaultQualityProfileID).Scan(ctx)
	if err == nil {
		if id, parseErr := strconv.ParseInt(setting.Value, 10, 64); parseErr == nil && id > 0 {
			p, profileErr := s.getQualityProfile(ctx, db, id)
			if profileErr == nil {
				return p, nil
			}
			if !errors.Is(profileErr, ErrNotFound) {
				return nil, profileErr
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get default quality profile setting: %w", err)
	}

	p, err := s.oldestQualityProfile(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := s.setDefaultQualityProfile(ctx, db, p.ID); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) setDefaultQualityProfile(ctx context.Context, db bun.IDB, id int64) error {
	if id <= 0 {
		return fmt.Errorf("store: default quality profile id must be positive")
	}
	if _, err := s.getQualityProfile(ctx, db, id); err != nil {
		return err
	}
	setting := &settingModel{
		Key: SettingDefaultQualityProfileID, Value: strconv.FormatInt(id, 10), UpdatedAt: formatTime(now()),
	}
	if _, err := db.NewInsert().Model(setting).
		On("CONFLICT (key) DO UPDATE").
		Set("value = EXCLUDED.value").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx); err != nil {
		return fmt.Errorf("store: set default quality profile %d: %w", id, err)
	}
	return nil
}

func (s *Store) oldestQualityProfile(ctx context.Context, db bun.IDB) (*core.QualityProfile, error) {
	var model qualityProfileStoreModel
	err := db.NewSelect().Model(&model).Order("id ASC").Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: no quality profiles: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get oldest quality profile: %w", err)
	}
	p, err := model.coreQualityProfile()
	if err != nil {
		return nil, fmt.Errorf("store: get oldest quality profile: %w", err)
	}
	return &p, nil
}

func (s *Store) qualityProfileReferenceCounts(ctx context.Context, db bun.IDB, id int64) (QualityProfileReferenceCounts, error) {
	var refs QualityProfileReferenceCounts
	err := db.NewSelect().
		ColumnExpr("(SELECT COUNT(*) FROM libraries WHERE quality_profile_id = ?) AS libraries", id).
		ColumnExpr("(SELECT COUNT(*) FROM movies WHERE quality_profile_id = ?) AS movies", id).
		ColumnExpr("(SELECT COUNT(*) FROM series WHERE quality_profile_id = ?) AS series", id).
		Scan(ctx, &refs)
	if err != nil {
		return QualityProfileReferenceCounts{}, fmt.Errorf("store: count quality profile %d references: %w", id, err)
	}
	return refs, nil
}

// GetQualityProfile returns the profile with the given id, or ErrNotFound.
func (s *Store) GetQualityProfile(ctx context.Context, id int64) (*core.QualityProfile, error) {
	return s.getQualityProfile(ctx, s.db, id)
}

func (s *Store) getQualityProfile(ctx context.Context, db bun.IDB, id int64) (*core.QualityProfile, error) {
	var model qualityProfileStoreModel
	err := db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: quality profile %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get quality profile %d: %w", id, err)
	}
	p, err := model.coreQualityProfile()
	if err != nil {
		return nil, fmt.Errorf("store: get quality profile %d: %w", id, err)
	}
	return &p, nil
}

// ListQualityProfiles returns every profile ordered by creation order.
func (s *Store) ListQualityProfiles(ctx context.Context) ([]core.QualityProfile, error) {
	models := make([]qualityProfileStoreModel, 0)
	if err := s.db.NewSelect().Model(&models).Order("id ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list quality profiles: %w", err)
	}
	out := make([]core.QualityProfile, 0, len(models))
	for i := range models {
		p, err := models[i].coreQualityProfile()
		if err != nil {
			return nil, fmt.Errorf("store: scan quality profile: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

func qualityProfileStoreModelFromCore(p *core.QualityProfile, items, preferredSources, customFormats string) *qualityProfileStoreModel {
	return &qualityProfileStoreModel{
		ID: p.ID, Name: p.Name, Cutoff: p.Cutoff, Items: items, UpgradeAllowed: p.UpgradeAllowed,
		PreferredSources: preferredSources, ProperRepackPreference: p.ProperRepackPreference,
		MinSeeders: p.MinSeeders, MinSizeMB: p.MinSizeMB, MaxSizeMB: p.MaxSizeMB,
		CustomFormats: customFormats, TVProfile: p.TVProfile, TVCompatibilityPolicy: p.TVCompatibilityPolicy,
	}
}

func (m *qualityProfileStoreModel) coreQualityProfile() (core.QualityProfile, error) {
	p := core.QualityProfile{
		ID: m.ID, Name: m.Name, Cutoff: m.Cutoff, UpgradeAllowed: m.UpgradeAllowed,
		ProperRepackPreference: m.ProperRepackPreference, MinSeeders: m.MinSeeders,
		MinSizeMB: m.MinSizeMB, MaxSizeMB: m.MaxSizeMB, TVProfile: m.TVProfile,
		TVCompatibilityPolicy: m.TVCompatibilityPolicy,
	}
	if m.Items != "" {
		if err := json.Unmarshal([]byte(m.Items), &p.Items); err != nil {
			return core.QualityProfile{}, fmt.Errorf("decode items of profile %q: %w", p.Name, err)
		}
	}
	if m.PreferredSources != "" {
		if err := json.Unmarshal([]byte(m.PreferredSources), &p.PreferredSources); err != nil {
			return core.QualityProfile{}, fmt.Errorf("decode preferred sources of profile %q: %w", p.Name, err)
		}
	}
	if m.CustomFormats != "" {
		if err := json.Unmarshal([]byte(m.CustomFormats), &p.CustomFormats); err != nil {
			return core.QualityProfile{}, fmt.Errorf("decode custom formats of profile %q: %w", p.Name, err)
		}
	}
	p.CreatedAt = parseTime(m.CreatedAt)
	p.UpdatedAt = parseTime(m.UpdatedAt)
	return p, nil
}

func effectiveProperRepackPreference(preference string) string {
	if preference == "" {
		return core.ProperRepackPreferencePrefer
	}
	return preference
}

func effectiveTVProfile(id string) string {
	if id == "" {
		return core.TVProfileSafe
	}
	return id
}

func effectiveTVCompatibilityPolicy(policy string) string {
	if policy == "" {
		return core.TVCompatibilityPolicyIgnore
	}
	return policy
}

func qualityProfileJSONValues(p *core.QualityProfile) (items, preferredSources, customFormats string, err error) {
	itemsJSON, err := json.Marshal(p.Items)
	if err != nil {
		return "", "", "", fmt.Errorf("store: encode items of profile %q: %w", p.Name, err)
	}
	sources := p.PreferredSources
	if sources == nil {
		sources = []string{}
	}
	sourcesJSON, err := json.Marshal(sources)
	if err != nil {
		return "", "", "", fmt.Errorf("store: encode preferred sources of profile %q: %w", p.Name, err)
	}
	formats := p.CustomFormats
	if formats == nil {
		formats = []core.CustomFormat{}
	}
	formatsJSON, err := json.Marshal(formats)
	if err != nil {
		return "", "", "", fmt.Errorf("store: encode custom formats of profile %q: %w", p.Name, err)
	}
	p.ProperRepackPreference = effectiveProperRepackPreference(p.ProperRepackPreference)
	p.TVProfile = effectiveTVProfile(p.TVProfile)
	p.TVCompatibilityPolicy = effectiveTVCompatibilityPolicy(p.TVCompatibilityPolicy)
	return string(itemsJSON), string(sourcesJSON), string(formatsJSON), nil
}
