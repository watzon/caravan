package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const seriesColumns = `id, kind, provider, provider_ref, tmdb_id, stash_id, tvdb_id, imdb_id,
	title, sort_title, year, overview,
	status, path, poster_path, poster_url, monitored, quality_profile_id, first_aired, added_at, updated_at,
	library_id`

// UpsertSeries inserts or updates sr and writes back the assigned ID.
//
// Identity is sr.ID when set, otherwise whichever provider id the series
// carries: sr.TMDBID for a television series, sr.StashID for an adult one. The
// two are looked up the same way and for the same reason — a refresh that
// already knows the provider's id must land on the row it wrote last time
// rather than making a second one — and both columns carry the same partial
// unique index, so only a matched row can collide.
func (s *Store) UpsertSeries(ctx context.Context, sr *core.Series) error {
	// One place defaults the discriminator, so a caller built before kinds
	// existed still writes a television series and nothing has to guess later.
	if sr.Kind == "" {
		sr.Kind = core.SeriesKindTV
	}
	if !core.ValidSeriesKind(sr.Kind) {
		return fmt.Errorf("store: upsert series %q: unknown kind %q", sr.Title, sr.Kind)
	}
	// A series' library must speak the series' kind: `series.kind` says what
	// the row IS, the library says which shelf answers for it, and the two
	// vocabularies line up through core.LibraryKindForSeries. Drift here would
	// grade an adult series against a television library's settings (or the
	// reverse), so it is refused loudly at the one door every write uses.
	if sr.LibraryID != 0 {
		lib, err := s.GetLibrary(ctx, sr.LibraryID)
		if err != nil {
			return fmt.Errorf("store: upsert series %q: %w", sr.Title, err)
		}
		if lib.Kind != core.LibraryKindForSeries(sr.Kind) {
			return fmt.Errorf("store: upsert series %q: kind %q does not belong in %q library %d",
				sr.Title, sr.Kind, lib.Kind, lib.ID)
		}
	}

	normalizeSeriesProvider(sr)

	if sr.ID == 0 {
		existing, err := s.matchExistingSeries(ctx, sr)
		if err != nil {
			return err
		}
		if existing != nil {
			sr.ID = existing.ID
			if sr.AddedAt.IsZero() {
				sr.AddedAt = existing.AddedAt
			}
		}
	}

	ts := now()
	if sr.AddedAt.IsZero() {
		sr.AddedAt = ts
	}
	sr.UpdatedAt = ts

	if sr.ID != 0 {
		// library_id 0 keeps the stored value — a refresh must never move a
		// series between libraries; a move names its target explicitly.
		res, err := s.db.ExecContext(ctx, `
			UPDATE series SET kind = ?, provider = ?, provider_ref = ?, tmdb_id = ?,
				stash_id = ?, tvdb_id = ?, imdb_id = ?,
				title = ?, sort_title = ?,
				year = ?, overview = ?, status = ?, path = ?, poster_path = ?, poster_url = ?,
				monitored = ?, quality_profile_id = ?, first_aired = ?, added_at = ?, updated_at = ?,
				library_id = COALESCE(NULLIF(?, 0), library_id)
			WHERE id = ?`,
			sr.Kind, sr.Provider, sr.ProviderRef, sr.TMDBID, sr.StashID, sr.TVDBID, sr.IMDBID,
			sr.Title, sr.SortTitle,
			sr.Year, sr.Overview,
			sr.Status, sr.Path, sr.PosterPath, sr.PosterURL, sr.Monitored, sr.QualityProfileID,
			formatTime(sr.FirstAired), formatTime(sr.AddedAt), formatTime(sr.UpdatedAt),
			sr.LibraryID, sr.ID)
		if err != nil {
			return fmt.Errorf("store: update series %d: %w", sr.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update series %d: %w", sr.ID, err)
		}
		if n == 0 {
			return fmt.Errorf("store: update series %d: %w", sr.ID, ErrNotFound)
		}
		return nil
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO series (kind, provider, provider_ref, tmdb_id, stash_id, tvdb_id, imdb_id,
			title, sort_title,
			year, overview,
			status, path, poster_path, poster_url, monitored, quality_profile_id, first_aired,
			added_at, updated_at, library_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sr.Kind, sr.Provider, sr.ProviderRef, sr.TMDBID, sr.StashID, sr.TVDBID, sr.IMDBID,
		sr.Title, sr.SortTitle,
		sr.Year, sr.Overview,
		sr.Status, sr.Path, sr.PosterPath, sr.PosterURL, sr.Monitored, sr.QualityProfileID,
		formatTime(sr.FirstAired), formatTime(sr.AddedAt), formatTime(sr.UpdatedAt),
		sr.LibraryID)
	if err != nil {
		return fmt.Errorf("store: insert series %q: %w", sr.Title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert series %q: %w", sr.Title, err)
	}
	sr.ID = id
	return nil
}

// GetSeries returns the series with the given id, or ErrNotFound.
func (s *Store) GetSeries(ctx context.Context, id int64) (*core.Series, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+seriesColumns+" FROM series WHERE id = ?", id)
	sr, err := scanSeries(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: series %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get series %d: %w", id, err)
	}
	return sr, nil
}

// GetSeriesByTMDBID returns the series with the given TMDB id, or ErrNotFound.
func (s *Store) GetSeriesByTMDBID(ctx context.Context, tmdbID int64) (*core.Series, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+seriesColumns+" FROM series WHERE tmdb_id = ?", tmdbID)
	sr, err := scanSeries(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: series tmdb %d: %w", tmdbID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get series tmdb %d: %w", tmdbID, err)
	}
	return sr, nil
}

// GetSeriesByStashID returns the series with the given stash-box id, or
// ErrNotFound. It is GetSeriesByTMDBID's adult twin; a blank id matches nothing
// rather than matching every unmatched row, because "" is precisely the value
// the partial unique index excludes.
func (s *Store) GetSeriesByStashID(ctx context.Context, stashID string) (*core.Series, error) {
	if stashID == "" {
		return nil, fmt.Errorf("store: series stash %q: %w", stashID, ErrNotFound)
	}
	row := s.db.QueryRowContext(ctx, "SELECT "+seriesColumns+" FROM series WHERE stash_id = ?", stashID)
	sr, err := scanSeries(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: series stash %q: %w", stashID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get series stash %q: %w", stashID, err)
	}
	return sr, nil
}

// normalizeSeriesProvider derives the provider identity from whichever legacy
// id the series' kind is matched on, when the caller supplied none — the same
// move this function's neighbour makes for Kind, and for the same reason.
//
// It is what makes "every matched row carries a ref" a property of the table
// rather than a habit of its callers: a caller written before 0024 still lands
// a row the ref lookups can find.
func normalizeSeriesProvider(sr *core.Series) {
	if sr.Provider != "" {
		return
	}
	switch {
	case sr.Kind == core.SeriesKindAdult && sr.StashID != "":
		sr.Provider, sr.ProviderRef = core.ProviderStashbox, sr.StashID
	case sr.Kind != core.SeriesKindAdult && sr.TMDBID != 0:
		ref := core.TMDBRef(sr.TMDBID)
		sr.Provider, sr.ProviderRef = ref.Provider, ref.Ref
	}
}

// GetSeriesByProviderRef returns the series one provider identified by ref, or
// ErrNotFound. A blank ref matches nothing, for GetSeriesByStashID's reason:
// "" is precisely the value the partial unique index excludes.
func (s *Store) GetSeriesByProviderRef(ctx context.Context, provider, ref string) (*core.Series, error) {
	if provider == "" || ref == "" {
		return nil, fmt.Errorf("store: series %s/%s: %w", provider, ref, ErrNotFound)
	}
	row := s.db.QueryRowContext(ctx, "SELECT "+seriesColumns+
		" FROM series WHERE provider = ? AND provider_ref = ?", provider, ref)
	sr, err := scanSeries(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: series %s/%s: %w", provider, ref, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get series %s/%s: %w", provider, ref, err)
	}
	return sr, nil
}

// matchExistingSeries finds the row sr is an update of, by whichever provider
// id its kind is matched on, or nil when there is none. An unmatched series
// (no provider id at all) always inserts: two shows the scanner has not
// identified are two shows, not one.
//
// RUNG ORDER MATTERS. The ref rung is first and applies to every kind; the
// stash and tmdb rungs behind it are compatibility aliases for the two
// providers that predate refs. Put the tmdb rung first and a re-fetched TMDB
// series matches on tmdb_id while its ref goes unconsulted — which is fine
// until a provider that writes no tmdb_id comes along, at which point every
// refresh inserts a duplicate instead of updating the row it wrote last time.
func (s *Store) matchExistingSeries(ctx context.Context, sr *core.Series) (*core.Series, error) {
	var (
		existing *core.Series
		err      error
	)
	switch {
	case sr.ProviderRef != "":
		existing, err = s.GetSeriesByProviderRef(ctx, sr.Provider, sr.ProviderRef)
	case sr.Kind == core.SeriesKindAdult && sr.StashID != "":
		existing, err = s.GetSeriesByStashID(ctx, sr.StashID)
	case sr.Kind != core.SeriesKindAdult && sr.TMDBID != 0:
		existing, err = s.GetSeriesByTMDBID(ctx, sr.TMDBID)
	default:
		return nil, nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return existing, nil
}

// ListSeries returns every series ordered by sort title then title.
//
// Every kind, deliberately: this is the raw table, and the callers that must
// not show adult titles are the ones that know who is asking. They narrow with
// ListSeriesByKind rather than having a filter applied here behind their back,
// because a list that silently omits rows is the harder bug — an adult series
// that never appears in a rescan is a lot quieter than one that appears where
// it should not.
func (s *Store) ListSeries(ctx context.Context) ([]core.Series, error) {
	return s.listSeries(ctx, "SELECT "+seriesColumns+" FROM series ORDER BY sort_title, title, year")
}

// ListSeriesByKind returns every series of one core.SeriesKind*, in the same
// order ListSeries uses. It is how a surface that may only show television —
// the TV library screen, the DLNA tree's TV container — asks for exactly that,
// and how the adult module's own screens ask for their side.
func (s *Store) ListSeriesByKind(ctx context.Context, kind string) ([]core.Series, error) {
	return s.listSeries(ctx,
		"SELECT "+seriesColumns+" FROM series WHERE kind = ? ORDER BY sort_title, title, year", kind)
}

func (s *Store) listSeries(ctx context.Context, query string, args ...any) ([]core.Series, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list series: %w", err)
	}
	defer rows.Close()

	out := []core.Series{}
	for rows.Next() {
		sr, err := scanSeries(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan series: %w", err)
		}
		out = append(out, *sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list series: %w", err)
	}
	return out, nil
}

// SeriesIDsByTMDBID is MovieIDsByTMDBID's series twin; see it for why the
// discover screens need it.
func (s *Store) SeriesIDsByTMDBID(ctx context.Context, tmdbIDs []int64) (map[int64]int64, error) {
	return s.idsByTMDBID(ctx, "series", tmdbIDs)
}

// DeleteSeries removes the series and, by foreign-key cascade, its seasons,
// episodes, and episode-file links. Files on disk are untouched.
func (s *Store) DeleteSeries(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM series WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete series %d: %w", id, err)
	}
	return nil
}

func scanSeries(sc scanner) (*core.Series, error) {
	var (
		sr         core.Series
		firstAired string
		addedAt    string
		updatedAt  string
	)
	err := sc.Scan(&sr.ID, &sr.Kind, &sr.Provider, &sr.ProviderRef, &sr.TMDBID, &sr.StashID,
		&sr.TVDBID, &sr.IMDBID,
		&sr.Title, &sr.SortTitle, &sr.Year,
		&sr.Overview, &sr.Status, &sr.Path, &sr.PosterPath, &sr.PosterURL, &sr.Monitored,
		&sr.QualityProfileID, &firstAired, &addedAt, &updatedAt, &sr.LibraryID)
	if err != nil {
		return nil, err
	}
	sr.FirstAired = parseTime(firstAired)
	sr.AddedAt = parseTime(addedAt)
	sr.UpdatedAt = parseTime(updatedAt)
	return &sr, nil
}
