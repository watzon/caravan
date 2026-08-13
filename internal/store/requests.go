package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// requestColumns remains for the scene-request lookup in adult.go. That query
// is migrated with its owning domain; listRequests still scans it through Bun.
const requestColumns = `id, media_type, tmdb_id, stash_id, title, year, poster_path, seasons,
	min_availability, status, requested_by, created_at, updated_at`

// requestIdentity is the (column, value) pair that identifies a request of one
// media type: a TMDB id for a movie or a series, a stash-box id for a scene. It
// is what CreateRequest looks an existing pending row up by, and it mirrors the
// pair of partial unique indexes the table enforces the same rule with — so the
// merge below finds exactly the row an INSERT would have collided with.
func requestIdentity(r *core.Request) (string, any) {
	if r.MediaType == core.MediaTypeScene {
		return "stash_id", r.StashID
	}
	return "tmdb_id", r.TMDBID
}

// CreateRequest records a wish for a title that is not in the library.
//
// It merges rather than duplicates: when a pending request already exists for
// the same (MediaType, TMDBID), the two season lists are unioned into that row
// and its id is written back to r. Two people asking for the same show, or one
// person asking for season 2 after season 1, is the normal case — a second row
// would give the approver two things to approve for one title.
//
// A nil Seasons list means the whole title, and it wins over any named seasons
// already on the row: "all of it" is not narrowed by a later "season 3".
//
// RequestedBy records who asked. A merge leaves the existing row's asker alone
// and writes it back to r, so the caller answers with the truth rather than
// with itself. The rest of the row's description — title, year, artwork, the
// availability a movie is held for — belongs to that asker too: a merge fills a
// field nobody has filled and overwrites nothing.
func (s *Store) CreateRequest(ctx context.Context, r *core.Request) error {
	if r.Status == "" {
		r.Status = core.RequestPending
	}
	ts := now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: create request: %w", err)
	}
	defer tx.Rollback()

	var existing requestStoreModel
	idColumn, idValue := requestIdentity(r)
	err = tx.NewSelect().Model(&existing).
		Column("id", "title", "year", "poster_path", "seasons", "min_availability", "requested_by").
		Where("media_type = ?", r.MediaType).
		Where(idColumn+" = ?", idValue).
		Where("status = ?", core.RequestPending).
		Scan(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// New request below.
	case err != nil:
		return fmt.Errorf("store: create request for %s %d: %w", r.MediaType, r.TMDBID, err)
	default:
		merged, err := mergeSeasons(existing.Seasons, r.Seasons)
		if err != nil {
			return fmt.Errorf("store: merge request %d: %w", existing.ID, err)
		}
		encoded, err := encodeSeasons(merged)
		if err != nil {
			return fmt.Errorf("store: merge request %d: %w", existing.ID, err)
		}
		// The first asker's row is theirs, so a merge fills a field only when
		// nobody has filled it — it never overwrites one. POST /requests is
		// member-allowed and its body is free text, and a merge that rewrote
		// the description would let one housemate put words in another's
		// mouth, under their name, in the admin's approval queue. The same
		// rule keeps a later request from merging toward "wait longer" behind
		// the first asker's back.
		//
		// Seasons are the exception, and the reason a merge exists: they union.
		if existing.Title != "" {
			r.Title = existing.Title
		}
		if existing.Year != 0 {
			r.Year = existing.Year
		}
		if existing.PosterPath != nil && *existing.PosterPath != "" {
			r.PosterPath = *existing.PosterPath
		}
		if existing.MinAvailability != "" {
			r.MinAvailability = existing.MinAvailability
		}
		// The first asker owns the row, so the UPDATE below leaves requested_by
		// alone. A merge is a second person queueing behind the first, not a
		// handover: rewriting the owner would move somebody else's request out
		// of their own list and into the newcomer's.
		r.RequestedBy = existing.RequestedBy
		model := &requestStoreModel{
			ID: existing.ID, Title: r.Title, Year: r.Year, PosterPath: stringPointer(r.PosterPath),
			Seasons: encoded, MinAvailability: r.MinAvailability, UpdatedAt: formatTime(ts),
		}
		if _, err := tx.NewUpdate().Model(model).
			Column("title", "year", "poster_path", "seasons", "min_availability", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("store: merge request %d: %w", existing.ID, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: merge request %d: %w", existing.ID, err)
		}
		r.ID = existing.ID
		r.Seasons = merged
		r.Status = core.RequestPending
		r.UpdatedAt = ts
		return nil
	}

	r.Seasons = normalizeSeasons(r.Seasons)
	encoded, err := encodeSeasons(r.Seasons)
	if err != nil {
		return fmt.Errorf("store: create request for %s %d: %w", r.MediaType, r.TMDBID, err)
	}
	model := requestStoreModelFromCore(r, encoded, formatTime(ts))
	if err := tx.NewInsert().Model(model).Returning("id").Scan(ctx); err != nil {
		return fmt.Errorf("store: create request for %s %d: %w", r.MediaType, r.TMDBID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: create request for %s %d: %w", r.MediaType, r.TMDBID, err)
	}
	r.ID = model.ID
	r.CreatedAt = ts
	r.UpdatedAt = ts
	return nil
}

// GetRequest returns one request, or ErrNotFound.
func (s *Store) GetRequest(ctx context.Context, id int64) (*core.Request, error) {
	var model requestStoreModel
	err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: request %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get request %d: %w", id, err)
	}
	r, err := model.coreRequest()
	if err != nil {
		return nil, fmt.Errorf("store: get request %d: %w", id, err)
	}
	return &r, nil
}

// ListRequests returns requests newest first. An empty status returns every
// request; otherwise only that status.
func (s *Store) ListRequests(ctx context.Context, status string) ([]core.Request, error) {
	query := s.db.NewSelect().Model((*requestStoreModel)(nil))
	if status != "" {
		query = query.Where("status = ?", status)
	}
	return s.listRequestModels(ctx, query.Order("created_at DESC", "id DESC"))
}

// ListRequestsBy returns one account's requests, newest first, filtered by
// status exactly as ListRequests is. Everybody's wishes live in one table, and
// requested_by is the only thing that says whose a row was, so this is what a
// member's requests screen reads.
//
// requestedBy 0 is a real query and not a wildcard: it selects the rows that
// record no account, which is what a pre-accounts or open-server row is.
func (s *Store) ListRequestsBy(ctx context.Context, requestedBy int64, status string) ([]core.Request, error) {
	query := s.db.NewSelect().Model((*requestStoreModel)(nil)).Where("requested_by = ?", requestedBy)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	return s.listRequestModels(ctx, query.Order("created_at DESC", "id DESC"))
}

// ListPendingRequestsForTMDBIDs returns the pending requests among the given
// provider ids, across both media types. It is what decorates a discover list:
// the caller keys the result by (MediaType, TMDBID) itself, because a movie and
// a series can share a TMDB id.
func (s *Store) ListPendingRequestsForTMDBIDs(ctx context.Context, tmdbIDs []int64) ([]core.Request, error) {
	if len(tmdbIDs) == 0 {
		return []core.Request{}, nil
	}
	query := s.db.NewSelect().Model((*requestStoreModel)(nil)).
		Where("status = ?", core.RequestPending).
		Where("tmdb_id IN (?)", bun.In(tmdbIDs))
	return s.listRequestModels(ctx, query)
}

func (s *Store) listRequestModels(ctx context.Context, query *bun.SelectQuery) ([]core.Request, error) {
	models := make([]requestStoreModel, 0)
	if err := query.Model(&models).Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list requests: %w", err)
	}
	return requestModelsToCore(models)
}

// listRequests is retained for adult.go's scene-specific lookup until that
// domain is migrated. The ordinary request lists above use typed Bun builders.
func (s *Store) listRequests(ctx context.Context, query string, args ...any) ([]core.Request, error) {
	models := make([]requestStoreModel, 0)
	if err := s.db.NewRaw(query, args...).Scan(ctx, &models); err != nil {
		return nil, fmt.Errorf("store: list requests: %w", err)
	}
	return requestModelsToCore(models)
}

func requestModelsToCore(models []requestStoreModel) ([]core.Request, error) {
	out := make([]core.Request, 0, len(models))
	for i := range models {
		r, err := models[i].coreRequest()
		if err != nil {
			return nil, fmt.Errorf("store: scan request: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// SetRequestStatus moves a request to a new status. Setting the status of an
// absent request is ErrNotFound.
func (s *Store) SetRequestStatus(ctx context.Context, id int64, status string) error {
	res, err := s.db.NewUpdate().Model((*requestStoreModel)(nil)).
		Set("status = ?", status).
		Set("updated_at = ?", formatTime(now())).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: set request %d status: %w", id, err)
	}
	return affectedOne(res, "set request status", id)
}

// ApproveRequestsFor absorbs the pending request for a title that has just
// reached the library, and reports how many rows it moved. Adding a title is
// the whole point of requesting it, so however it got added — approval, the
// add-to-library button, a match from the scan-review queue — the wish is
// granted and the request stops asking.
//
// It is a no-op with no error when nothing is pending, which is the common
// case: most library additions were never requested.
func (s *Store) ApproveRequestsFor(ctx context.Context, mediaType string, tmdbID int64) (int64, error) {
	res, err := s.db.NewUpdate().Model((*requestStoreModel)(nil)).
		Set("status = ?", core.RequestApproved).
		Set("updated_at = ?", formatTime(now())).
		Where("media_type = ?", mediaType).
		Where("tmdb_id = ?", tmdbID).
		Where("status = ?", core.RequestPending).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: approve requests for %s %d: %w", mediaType, tmdbID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: approve requests for %s %d: %w", mediaType, tmdbID, err)
	}
	return n, nil
}

// GrantRequestSeasons resolves the pending request for a series against an
// addition that did not cover every season, and reports how many rows it
// approved.
//
// ungranted names the seasons the addition left behind. A request asking for
// none of them has been granted in full and is approved exactly as
// ApproveRequestsFor would; one that still names an ungranted season is
// narrowed to what is outstanding and stays pending, so a partial grant is
// recorded rather than silently closed. A request for the whole title is
// narrowed to the ungranted seasons, which is the only honest reading of "all
// of it" once part of it has arrived.
func (s *Store) GrantRequestSeasons(ctx context.Context, tmdbID int64, ungranted []int) (int64, error) {
	if len(ungranted) == 0 {
		return s.ApproveRequestsFor(ctx, core.MediaTypeSeries, tmdbID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: grant request seasons for series %d: %w", tmdbID, err)
	}
	defer tx.Rollback()

	var request requestStoreModel
	err = tx.NewSelect().Model(&request).
		Column("id", "seasons").
		Where("media_type = ?", core.MediaTypeSeries).
		Where("tmdb_id = ?", tmdbID).
		Where("status = ?", core.RequestPending).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: grant request seasons for series %d: %w", tmdbID, err)
	}

	asked, err := decodeSeasons(request.Seasons)
	if err != nil {
		return 0, fmt.Errorf("store: grant request %d: %w", request.ID, err)
	}
	remaining := outstandingSeasons(asked, ungranted)

	ts := formatTime(now())
	var granted int64
	if len(remaining) == 0 {
		if _, err := tx.NewUpdate().Model((*requestStoreModel)(nil)).
			Set("status = ?", core.RequestApproved).
			Set("updated_at = ?", ts).
			Where("id = ?", request.ID).
			Exec(ctx); err != nil {
			return 0, fmt.Errorf("store: grant request %d: %w", request.ID, err)
		}
		granted = 1
	} else {
		encoded, err := encodeSeasons(remaining)
		if err != nil {
			return 0, fmt.Errorf("store: grant request %d: %w", request.ID, err)
		}
		if _, err := tx.NewUpdate().Model((*requestStoreModel)(nil)).
			Set("seasons = ?", encoded).
			Set("updated_at = ?", ts).
			Where("id = ?", request.ID).
			Exec(ctx); err != nil {
			return 0, fmt.Errorf("store: grant request %d: %w", request.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: grant request %d: %w", request.ID, err)
	}
	return granted, nil
}

// outstandingSeasons is what a request still asks for once ungranted is all
// that is left to get. A nil asked list means the whole title, so everything
// ungranted is still outstanding.
func outstandingSeasons(asked, ungranted []int) []int {
	if len(asked) == 0 {
		return normalizeSeasons(ungranted)
	}
	var out []int
	for _, n := range asked {
		if slices.Contains(ungranted, n) {
			out = append(out, n)
		}
	}
	return normalizeSeasons(out)
}

func requestStoreModelFromCore(r *core.Request, seasons *string, timestamp string) *requestStoreModel {
	return &requestStoreModel{
		ID: r.ID, MediaType: r.MediaType, TMDBID: r.TMDBID, StashID: r.StashID,
		Title: r.Title, Year: r.Year, PosterPath: stringPointer(r.PosterPath), Seasons: seasons,
		MinAvailability: r.MinAvailability, Status: r.Status, RequestedBy: r.RequestedBy,
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}
}

func (m *requestStoreModel) coreRequest() (core.Request, error) {
	r := core.Request{
		ID: m.ID, MediaType: m.MediaType, TMDBID: m.TMDBID, StashID: m.StashID,
		Title: m.Title, Year: m.Year, MinAvailability: m.MinAvailability,
		Status: m.Status, RequestedBy: m.RequestedBy,
	}
	if m.PosterPath != nil {
		r.PosterPath = *m.PosterPath
	}
	decoded, err := decodeSeasons(m.Seasons)
	if err != nil {
		return core.Request{}, err
	}
	r.Seasons = decoded
	r.CreatedAt = parseTime(m.CreatedAt)
	r.UpdatedAt = parseTime(m.UpdatedAt)
	return r, nil
}

// mergeSeasons unions a stored season list with an incoming one. A nil list on
// either side means "the whole title", which absorbs the other.
func mergeSeasons(stored *string, incoming []int) ([]int, error) {
	existing, err := decodeSeasons(stored)
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 || len(incoming) == 0 {
		return nil, nil
	}
	return normalizeSeasons(append(existing, incoming...)), nil
}

// normalizeSeasons sorts and deduplicates a season list so a stored row and a
// comparison never depend on the order the client sent. An empty list is nil:
// "no seasons at all" is not a request anyone can mean, so it reads as the
// whole title, exactly like a missing list.
func normalizeSeasons(seasons []int) []int {
	if len(seasons) == 0 {
		return nil
	}
	out := slices.Clone(seasons)
	slices.Sort(out)
	return slices.Compact(out)
}

func encodeSeasons(seasons []int) (*string, error) {
	if seasons == nil {
		return nil, nil
	}
	b, err := json.Marshal(seasons)
	if err != nil {
		return nil, err
	}
	encoded := string(b)
	return &encoded, nil
}

func decodeSeasons(col *string) ([]int, error) {
	if col == nil || *col == "" {
		return nil, nil
	}
	var out []int
	if err := json.Unmarshal([]byte(*col), &out); err != nil {
		return nil, fmt.Errorf("decode seasons %q: %w", *col, err)
	}
	return out, nil
}
