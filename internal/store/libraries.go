package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const libraryColumns = `id, kind, name, root_path, dlna_visible,
	route_torrent, route_usenet, quality_profile_id`

// ListLibraries returns every library ordered by id, which is the order 0012
// seeded them in: Movies first, then Series.
func (s *Store) ListLibraries(ctx context.Context) ([]core.Library, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+libraryColumns+" FROM libraries ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("store: list libraries: %w", err)
	}
	defer rows.Close()

	out := []core.Library{}
	for rows.Next() {
		l, err := scanLibrary(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan library: %w", err)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list libraries: %w", err)
	}
	return out, nil
}

// GetLibrary returns the library with the given id, or ErrNotFound.
func (s *Store) GetLibrary(ctx context.Context, id int64) (*core.Library, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+libraryColumns+" FROM libraries WHERE id = ?", id)
	l, err := scanLibrary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: library %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get library %d: %w", id, err)
	}
	return l, nil
}

// GetLibraryByKind returns the library for one of the core.LibraryKind*
// constants, or ErrNotFound. This is how an item finds its library: kind is
// the only mapping, so a movie row and the movie library never need a join
// column between them.
func (s *Store) GetLibraryByKind(ctx context.Context, kind string) (*core.Library, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+libraryColumns+" FROM libraries WHERE kind = ?", kind)
	l, err := scanLibrary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: library of kind %q: %w", kind, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get library of kind %q: %w", kind, err)
	}
	return l, nil
}

// UpdateLibrary rewrites the mutable fields of an existing library. Kind is
// not among them: it is the library's identity and what items are mapped by.
// Updating an absent library is ErrNotFound.
//
// Flipping dlna_visible also advances SettingDLNAUpdateID, because that flag is
// the one library field the DLNA content tree is built from: a television
// caches the tree against SystemUpdateID, so a library that vanished from it
// while the counter stood still is one the TV keeps showing. Doing it here
// rather than in the caller is what makes the two impossible to get out of
// step.
func (s *Store) UpdateLibrary(ctx context.Context, l *core.Library) error {
	prev, err := s.GetLibrary(ctx, l.ID)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE libraries SET name = ?, root_path = ?, dlna_visible = ?,
			route_torrent = ?, route_usenet = ?, quality_profile_id = ?
		WHERE id = ?`,
		l.Name, l.RootPath, l.DLNAVisible,
		nullString(l.RouteTorrent), nullString(l.RouteUsenet), nullInt64(l.QualityProfileID),
		l.ID)
	if err != nil {
		return fmt.Errorf("store: update library %d: %w", l.ID, err)
	}
	if err := affectedOne(res, "library", l.ID); err != nil {
		return err
	}
	if prev.DLNAVisible != l.DLNAVisible {
		return s.bumpDLNAUpdateID(ctx)
	}
	return nil
}

// bumpDLNAUpdateID advances the ContentDirectory tree version by one.
//
// The increment is done in SQL rather than read-modify-write in Go so two
// concurrent toggles cannot both read 3 and both write 4, which would leave a
// client that had already cached 4 believing it was up to date. An absent key
// reads as 1 (see SettingDLNAUpdateID), so the first bump lands on 2.
func (s *Store) bumpDLNAUpdateID(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, '2', ?)
		ON CONFLICT (key) DO UPDATE SET
			value = CAST(CAST(settings.value AS INTEGER) + 1 AS TEXT),
			updated_at = excluded.updated_at`,
		SettingDLNAUpdateID, formatTime(now()))
	if err != nil {
		return fmt.Errorf("store: bump dlna update id: %w", err)
	}
	return nil
}

// ListLibraryIndexers returns the override rows stored for one library. It is
// the raw table, not the resolved set: an indexer with no row here is missing
// from the result and is enabled with its own categories. Callers rendering
// the per-library indexer matrix list the indexers separately and treat a
// missing row as the default; callers running a search want
// ResolveLibrarySettings instead.
func (s *Store) ListLibraryIndexers(ctx context.Context, libraryID int64) ([]core.LibraryIndexer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT library_id, indexer_id, enabled, categories FROM library_indexers
		WHERE library_id = ? ORDER BY indexer_id`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("store: list indexers of library %d: %w", libraryID, err)
	}
	defer rows.Close()

	out := []core.LibraryIndexer{}
	for rows.Next() {
		var (
			li         core.LibraryIndexer
			categories sql.NullString
		)
		if err := rows.Scan(&li.LibraryID, &li.IndexerID, &li.Enabled, &categories); err != nil {
			return nil, fmt.Errorf("store: scan library indexer: %w", err)
		}
		if categories.Valid && categories.String != "" {
			if err := json.Unmarshal([]byte(categories.String), &li.Categories); err != nil {
				return nil, fmt.Errorf("store: decode categories of library %d indexer %d: %w",
					li.LibraryID, li.IndexerID, err)
			}
		}
		out = append(out, li)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list indexers of library %d: %w", libraryID, err)
	}
	return out, nil
}

// SetLibraryIndexer writes one (library, indexer) override, replacing any
// previous one. Writing enabled with nil categories is equivalent to having no
// row at all, so a user can always get back to the default.
func (s *Store) SetLibraryIndexer(ctx context.Context, li *core.LibraryIndexer) error {
	var categories any
	if li.Categories != nil {
		b, err := json.Marshal(li.Categories)
		if err != nil {
			return fmt.Errorf("store: encode categories of library %d indexer %d: %w",
				li.LibraryID, li.IndexerID, err)
		}
		categories = string(b)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO library_indexers (library_id, indexer_id, enabled, categories)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (library_id, indexer_id) DO UPDATE SET
			enabled = excluded.enabled, categories = excluded.categories`,
		li.LibraryID, li.IndexerID, li.Enabled, categories)
	if err != nil {
		return fmt.Errorf("store: set library %d indexer %d: %w", li.LibraryID, li.IndexerID, err)
	}
	return nil
}

// ResolveLibrarySettings returns the effective settings for one library: its
// own value where it set one, the global default everywhere else (PLAN phase 8
// task 2).
//
// This is the only place the fallback rule lives, and the set of settings it
// covers is closed — routing, DLNA visibility, the default quality profile,
// and the indexer set with its per-pair categories. Every other setting stays
// global, so nothing else has to learn that libraries exist.
func (s *Store) ResolveLibrarySettings(ctx context.Context, libraryID int64) (*core.LibrarySettings, error) {
	lib, err := s.GetLibrary(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	settings, err := s.AllSettings(ctx)
	if err != nil {
		return nil, err
	}
	// Globally disabled indexers are not a library's decision to reverse: a
	// disabled indexer is out of every fan-out, and the per-library rows only
	// ever narrow that set further.
	indexers, err := s.ListEnabledIndexers(ctx)
	if err != nil {
		return nil, err
	}
	overrides, err := s.ListLibraryIndexers(ctx, lib.ID)
	if err != nil {
		return nil, err
	}
	byIndexer := make(map[int64]core.LibraryIndexer, len(overrides))
	for _, o := range overrides {
		byIndexer[o.IndexerID] = o
	}

	resolved := &core.LibrarySettings{
		LibraryID:        lib.ID,
		Kind:             lib.Kind,
		RouteTorrent:     overrideOrGlobal(lib.RouteTorrent, settings[SettingRouteTorrent]),
		RouteUsenet:      overrideOrGlobal(lib.RouteUsenet, settings[SettingRouteUsenet]),
		DLNAVisible:      lib.DLNAVisible,
		QualityProfileID: lib.QualityProfileID,
		Indexers:         []core.IndexerConfig{},
	}
	for _, ix := range indexers {
		o, ok := byIndexer[ix.ID]
		if ok && !o.Enabled {
			continue
		}
		if ok && o.Categories != nil {
			ix.Categories = o.Categories
		}
		resolved.Indexers = append(resolved.Indexers, ix)
	}
	return resolved, nil
}

// ResolveLibrarySettingsByKind resolves the settings of the library that items
// of the given core.LibraryKind* belong to.
//
// Kind is the whole item -> library mapping, so this is the entire lookup a
// search does: a movie search resolves the movie library, an episode search the
// tv library, and neither needs to know a library id exists.
func (s *Store) ResolveLibrarySettingsByKind(ctx context.Context, kind string) (*core.LibrarySettings, error) {
	lib, err := s.GetLibraryByKind(ctx, kind)
	if err != nil {
		return nil, err
	}
	return s.ResolveLibrarySettings(ctx, lib.ID)
}

// ResolveItemQualityProfile returns the effective profile for one library item:
// the profile the item names, the default of the library its kind maps to when
// it names none, and the store-wide default when neither answers.
//
// It is the library step ResolveQualityProfile deliberately has no notion of.
// Every scoring site goes through here rather than through ResolveQualityProfile
// directly, because a library default nobody reads is a setting that saves,
// renders as an override, and changes nothing (PLAN phase 8 task 2).
//
// A missing libraries row — a database not yet migrated past 0012 — resolves
// exactly as it did before there were libraries.
func (s *Store) ResolveItemQualityProfile(ctx context.Context, kind string, itemProfileID int64) (*core.QualityProfile, error) {
	if itemProfileID > 0 {
		p, err := s.GetQualityProfile(ctx, itemProfileID)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	lib, err := s.GetLibraryByKind(ctx, kind)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return s.ResolveQualityProfile(ctx, 0)
		}
		return nil, err
	}
	return s.ResolveQualityProfile(ctx, lib.QualityProfileID)
}

// overrideOrGlobal is the whole fallback rule: the library answers when it has
// an answer, the global setting answers when it does not.
func overrideOrGlobal(library, global string) string {
	if library != "" {
		return library
	}
	return global
}

// nullInt64 stores zero as SQL NULL, which is what a nullable id column means
// by "not set" — pointing at row 0 is not the same as pointing at nothing.
func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func scanLibrary(sc scanner) (*core.Library, error) {
	var (
		l         core.Library
		torrent   sql.NullString
		usenet    sql.NullString
		profileID sql.NullInt64
	)
	if err := sc.Scan(&l.ID, &l.Kind, &l.Name, &l.RootPath, &l.DLNAVisible,
		&torrent, &usenet, &profileID); err != nil {
		return nil, err
	}
	l.RouteTorrent = torrent.String
	l.RouteUsenet = usenet.String
	l.QualityProfileID = profileID.Int64
	return &l, nil
}
