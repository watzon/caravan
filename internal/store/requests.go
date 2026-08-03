package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/watzon/caravan/internal/core"
)

const requestColumns = `id, media_type, tmdb_id, title, year, poster_path, seasons,
	min_availability, status, requested_by, created_at, updated_at`

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

	var (
		existingID           int64
		existingTitle        string
		existingYear         int
		existingPoster       sql.NullString
		existingSeasons      sql.NullString
		existingAvailability string
		existingRequestedBy  int64
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, title, year, poster_path, seasons, min_availability, requested_by FROM requests
		WHERE media_type = ? AND tmdb_id = ? AND status = ?`,
		r.MediaType, r.TMDBID, core.RequestPending).Scan(
		&existingID, &existingTitle, &existingYear, &existingPoster, &existingSeasons,
		&existingAvailability, &existingRequestedBy)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// New request below.
	case err != nil:
		return fmt.Errorf("store: create request for %s %d: %w", r.MediaType, r.TMDBID, err)
	default:
		merged, err := mergeSeasons(existingSeasons, r.Seasons)
		if err != nil {
			return fmt.Errorf("store: merge request %d: %w", existingID, err)
		}
		encoded, err := encodeSeasons(merged)
		if err != nil {
			return fmt.Errorf("store: merge request %d: %w", existingID, err)
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
		if existingTitle != "" {
			r.Title = existingTitle
		}
		if existingYear != 0 {
			r.Year = existingYear
		}
		if existingPoster.String != "" {
			r.PosterPath = existingPoster.String
		}
		if existingAvailability != "" {
			r.MinAvailability = existingAvailability
		}
		// The first asker owns the row, so the UPDATE below leaves requested_by
		// alone. A merge is a second person queueing behind the first, not a
		// handover: rewriting the owner would move somebody else's request out
		// of their own list and into the newcomer's.
		r.RequestedBy = existingRequestedBy
		if _, err := tx.ExecContext(ctx, `
			UPDATE requests SET title = ?, year = ?, poster_path = ?, seasons = ?,
				min_availability = ?, updated_at = ?
			WHERE id = ?`,
			r.Title, r.Year, nullString(r.PosterPath), encoded, r.MinAvailability,
			formatTime(ts), existingID); err != nil {
			return fmt.Errorf("store: merge request %d: %w", existingID, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: merge request %d: %w", existingID, err)
		}
		r.ID = existingID
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
	res, err := tx.ExecContext(ctx, `
		INSERT INTO requests (media_type, tmdb_id, title, year, poster_path, seasons,
			min_availability, status, requested_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.MediaType, r.TMDBID, r.Title, r.Year, nullString(r.PosterPath), encoded,
		r.MinAvailability, r.Status, r.RequestedBy, formatTime(ts), formatTime(ts))
	if err != nil {
		return fmt.Errorf("store: create request for %s %d: %w", r.MediaType, r.TMDBID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create request for %s %d: %w", r.MediaType, r.TMDBID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: create request for %s %d: %w", r.MediaType, r.TMDBID, err)
	}
	r.ID = id
	r.CreatedAt = ts
	r.UpdatedAt = ts
	return nil
}

// GetRequest returns one request, or ErrNotFound.
func (s *Store) GetRequest(ctx context.Context, id int64) (*core.Request, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+requestColumns+" FROM requests WHERE id = ?", id)
	r, err := scanRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: request %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get request %d: %w", id, err)
	}
	return r, nil
}

// ListRequests returns requests newest first. An empty status returns every
// request; otherwise only that status.
func (s *Store) ListRequests(ctx context.Context, status string) ([]core.Request, error) {
	query := "SELECT " + requestColumns + " FROM requests"
	var args []any
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC, id DESC"
	return s.listRequests(ctx, query, args...)
}

// ListRequestsBy returns one account's requests, newest first, filtered by
// status exactly as ListRequests is. Everybody's wishes live in one table, and
// requested_by is the only thing that says whose a row was, so this is what a
// member's requests screen reads.
//
// requestedBy 0 is a real query and not a wildcard: it selects the rows that
// record no account, which is what a pre-accounts or open-server row is.
func (s *Store) ListRequestsBy(ctx context.Context, requestedBy int64, status string) ([]core.Request, error) {
	query := "SELECT " + requestColumns + " FROM requests WHERE requested_by = ?"
	args := []any{requestedBy}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC, id DESC"
	return s.listRequests(ctx, query, args...)
}

// ListPendingRequestsForTMDBIDs returns the pending requests among the given
// provider ids, across both media types. It is what decorates a discover list:
// the caller keys the result by (MediaType, TMDBID) itself, because a movie and
// a series can share a TMDB id.
func (s *Store) ListPendingRequestsForTMDBIDs(ctx context.Context, tmdbIDs []int64) ([]core.Request, error) {
	if len(tmdbIDs) == 0 {
		return []core.Request{}, nil
	}
	args := []any{core.RequestPending}
	for _, id := range tmdbIDs {
		args = append(args, id)
	}
	return s.listRequests(ctx,
		"SELECT "+requestColumns+" FROM requests WHERE status = ? AND tmdb_id IN ("+
			placeholders(len(tmdbIDs))+")", args...)
}

func (s *Store) listRequests(ctx context.Context, query string, args ...any) ([]core.Request, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list requests: %w", err)
	}
	defer rows.Close()

	out := []core.Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan request: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list requests: %w", err)
	}
	return out, nil
}

// SetRequestStatus moves a request to a new status. Setting the status of an
// absent request is ErrNotFound.
func (s *Store) SetRequestStatus(ctx context.Context, id int64, status string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE requests SET status = ?, updated_at = ? WHERE id = ?",
		status, formatTime(now()), id)
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
	res, err := s.db.ExecContext(ctx, `
		UPDATE requests SET status = ?, updated_at = ?
		WHERE media_type = ? AND tmdb_id = ? AND status = ?`,
		core.RequestApproved, formatTime(now()), mediaType, tmdbID, core.RequestPending)
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

	var (
		id      int64
		seasons sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, seasons FROM requests
		WHERE media_type = ? AND tmdb_id = ? AND status = ?`,
		core.MediaTypeSeries, tmdbID, core.RequestPending).Scan(&id, &seasons)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: grant request seasons for series %d: %w", tmdbID, err)
	}

	asked, err := decodeSeasons(seasons)
	if err != nil {
		return 0, fmt.Errorf("store: grant request %d: %w", id, err)
	}
	remaining := outstandingSeasons(asked, ungranted)

	ts := formatTime(now())
	var granted int64
	if len(remaining) == 0 {
		if _, err := tx.ExecContext(ctx,
			"UPDATE requests SET status = ?, updated_at = ? WHERE id = ?",
			core.RequestApproved, ts, id); err != nil {
			return 0, fmt.Errorf("store: grant request %d: %w", id, err)
		}
		granted = 1
	} else {
		encoded, err := encodeSeasons(remaining)
		if err != nil {
			return 0, fmt.Errorf("store: grant request %d: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE requests SET seasons = ?, updated_at = ? WHERE id = ?",
			encoded, ts, id); err != nil {
			return 0, fmt.Errorf("store: grant request %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: grant request %d: %w", id, err)
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

func scanRequest(sc scanner) (*core.Request, error) {
	var (
		r          core.Request
		posterPath sql.NullString
		seasons    sql.NullString
		created    string
		updated    string
	)
	if err := sc.Scan(&r.ID, &r.MediaType, &r.TMDBID, &r.Title, &r.Year, &posterPath,
		&seasons, &r.MinAvailability, &r.Status, &r.RequestedBy, &created, &updated); err != nil {
		return nil, err
	}
	r.PosterPath = posterPath.String
	decoded, err := decodeSeasons(seasons)
	if err != nil {
		return nil, err
	}
	r.Seasons = decoded
	r.CreatedAt = parseTime(created)
	r.UpdatedAt = parseTime(updated)
	return &r, nil
}

// mergeSeasons unions a stored season list with an incoming one. A nil list on
// either side means "the whole title", which absorbs the other.
func mergeSeasons(stored sql.NullString, incoming []int) ([]int, error) {
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

func encodeSeasons(seasons []int) (any, error) {
	if seasons == nil {
		return nil, nil
	}
	b, err := json.Marshal(seasons)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func decodeSeasons(col sql.NullString) ([]int, error) {
	if !col.Valid || col.String == "" {
		return nil, nil
	}
	var out []int
	if err := json.Unmarshal([]byte(col.String), &out); err != nil {
		return nil, fmt.Errorf("decode seasons %q: %w", col.String, err)
	}
	return out, nil
}

// nullString stores the empty string as SQL NULL, which is what the nullable
// poster_path column means by "no artwork".
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
