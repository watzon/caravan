package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// downloadJSON is one row of the queue: the engine's live view where it has
// one, backed by the persisted row so a download the engine has forgotten (or
// has not reported yet, right after a restart) is still visible.
type downloadJSON struct {
	// ID is the engine's own handle, and what the queue endpoints address.
	ID     string `json:"id"`
	GrabID int64  `json:"grab_id"`
	Engine string `json:"engine"`
	// Protocol is "torrent" or "usenet", derived from Engine through the one
	// authority on that mapping (clients.ProtocolForEngine). The queue drawer
	// is built from it: a torrent has peers, trackers, a ratio and an upload
	// limit, and a Usenet download has none of those and a file list instead.
	Protocol string `json:"protocol"`
	Name     string `json:"name"`
	State    string `json:"state"`
	// Phase is the sub-step of a multi-stage download ("downloading",
	// "repairing", "extracting"), and "" for an engine that has none. Like the
	// rates it is live-only, so a row the engine is not reporting on has no
	// phase even if it had one before the restart.
	Phase string `json:"phase"`
	// Progress is completion in [0,1].
	Progress    float64 `json:"progress"`
	BytesDone   int64   `json:"bytes_done"`
	Size        int64   `json:"size"`
	DownRate    int64   `json:"down_rate"`
	UpRate      int64   `json:"up_rate"`
	MaxDownRate int64   `json:"max_down_rate"`
	MaxUpRate   int64   `json:"max_up_rate"`
	// ETASeconds is -1 when unknown, including for a row the engine is not
	// currently reporting on.
	ETASeconds int64   `json:"eta_seconds"`
	Ratio      float64 `json:"ratio"`
	SavePath   string  `json:"save_path"`
	Error      string  `json:"error"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

// handleListDownloads renders the queue (PLAN phase 2, task 4).
//
// The persisted rows and the engine's live snapshot are merged rather than
// either being trusted alone: the rows carry what a download was grabbed for
// and survive a restart, the snapshot carries the rates and progress that are
// stale the moment they are written. Nothing is written back here — keeping the
// rows fresh is a background job's work, not a GET's.
const (
	defaultDownloadLimit = 100
	maxDownloadLimit     = 1000
)

type downloadPager interface {
	ListPage(context.Context, int, string) ([]core.DownloadStatus, string, bool, error)
}

func (s *server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	_, hasLimit := query["limit"]
	rawCursor := query.Get("cursor")
	_, hasCursor := query["cursor"]
	if !hasLimit && !hasCursor {
		s.writeLegacyDownloads(w, r)
		return
	}

	limit := defaultDownloadLimit
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxDownloadLimit)
	}
	storedBefore, orphanCursor, err := parseDownloadCursor(rawCursor)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var engine core.Engine
	if s.engine != nil {
		engine = s.engine.Engine()
	}
	pager, ok := engine.(downloadPager)
	if engine == nil || !ok {
		s.writeLegacyDownloads(w, r)
		return
	}

	rows, nextStored, err := s.st.ListDownloadsPage(r.Context(), limit, storedBefore)
	if err != nil {
		s.writeStoreError(w, "list download page", err)
		return
	}
	out := make([]downloadJSON, 0, limit)
	for _, row := range rows {
		dto := storedDownloadDTO(row)
		if status, err := engine.Status(r.Context(), row.EngineID); err == nil {
			applyLiveStatus(&dto, *status)
		}
		dto.Protocol = clients.ProtocolForEngine(dto.Engine)
		out = append(out, dto)
	}

	nextCursor := ""
	if nextStored != 0 {
		nextCursor = "stored:" + strconv.FormatInt(nextStored, 10)
	} else {
		remaining := limit - len(out)
		if remaining == 0 {
			statuses, _, supported, err := pager.ListPage(r.Context(), 1, orphanCursor)
			if err != nil {
				s.writeEngineError(w, "list download page", err)
				return
			}
			if !supported {
				s.writeLegacyDownloads(w, r)
				return
			}
			for _, status := range statuses {
				_, lookupErr := s.st.GetDownloadByEngineID(r.Context(), status.ID)
				if errors.Is(lookupErr, store.ErrNotFound) {
					nextCursor = encodeOrphanCursor(orphanCursor)
					break
				}
				if lookupErr != nil {
					s.writeStoreError(w, "check orphan download", lookupErr)
					return
				}
			}
		} else {
			orphans, nextRaw, err := s.pageOrphans(r.Context(), pager, remaining, orphanCursor)
			if err != nil {
				s.writeEngineError(w, "list download page", err)
				return
			}
			for _, status := range orphans {
				dto := downloadJSON{Engine: status.Engine, ETASeconds: -1}
				if dto.Engine == "" {
					dto.Engine = s.engineName()
				}
				applyLiveStatus(&dto, status)
				dto.Protocol = clients.ProtocolForEngine(dto.Engine)
				out = append(out, dto)
			}
			if nextRaw != "" {
				nextCursor = encodeOrphanCursor(nextRaw)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"downloads": out, "next_cursor": nextCursor})
}

func (s *server) writeLegacyDownloads(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.ListDownloads(r.Context())
	if err != nil {
		s.writeStoreError(w, "list downloads", err)
		return
	}

	live := map[core.DownloadID]core.DownloadStatus{}
	if s.engine != nil {
		if engine := s.engine.Engine(); engine != nil {
			statuses, err := engine.List(r.Context())
			if err != nil {
				s.writeEngineError(w, "list downloads", err)
				return
			}
			for _, st := range statuses {
				live[st.ID] = st
			}
		}
	}

	out := make([]downloadJSON, 0, len(rows)+len(live))
	for _, row := range rows {
		dto := storedDownloadDTO(row)
		if status, ok := live[row.EngineID]; ok {
			applyLiveStatus(&dto, status)
			delete(live, row.EngineID)
		}
		dto.Protocol = clients.ProtocolForEngine(dto.Engine)
		out = append(out, dto)
	}

	orphans := make([]downloadJSON, 0, len(live))
	for _, status := range live {
		dto := downloadJSON{Engine: s.engineName(), ETASeconds: -1}
		applyLiveStatus(&dto, status)
		dto.Protocol = clients.ProtocolForEngine(dto.Engine)
		orphans = append(orphans, dto)
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })

	writeJSON(w, http.StatusOK, map[string]any{"downloads": append(out, orphans...)})
}

func (s *server) pageOrphans(ctx context.Context, pager downloadPager, limit int, cursor string) ([]core.DownloadStatus, string, error) {
	if limit <= 0 {
		statuses, _, supported, err := pager.ListPage(ctx, 1, cursor)
		if err != nil || !supported {
			return nil, "", err
		}
		for _, status := range statuses {
			if _, err := s.st.GetDownloadByEngineID(ctx, status.ID); errors.Is(err, store.ErrNotFound) {
				return []core.DownloadStatus{status}, "", nil
			} else if err != nil {
				return nil, "", err
			}
		}
		return nil, "", nil
	}
	out := make([]core.DownloadStatus, 0, limit)
	raw := cursor
	for len(out) < limit {
		statuses, next, supported, err := pager.ListPage(ctx, limit, raw)
		if err != nil {
			return nil, "", err
		}
		if !supported {
			return nil, "", fmt.Errorf("download: page listing unavailable")
		}
		for _, status := range statuses {
			_, lookupErr := s.st.GetDownloadByEngineID(ctx, status.ID)
			if errors.Is(lookupErr, store.ErrNotFound) {
				out = append(out, status)
				if len(out) == limit {
					return out, next, nil
				}
			} else if lookupErr != nil {
				return nil, "", lookupErr
			}
		}
		if next == "" {
			return out, "", nil
		}
		raw = next
	}
	return out, raw, nil
}

func parseDownloadCursor(raw string) (int64, string, error) {
	if raw == "" {
		return 0, "", nil
	}
	if strings.HasPrefix(raw, "stored:") {
		id, err := strconv.ParseInt(strings.TrimPrefix(raw, "stored:"), 10, 64)
		if err != nil || id <= 0 {
			return 0, "", errors.New("cursor must be a valid stored download cursor")
		}
		return id, "", nil
	}
	if raw == "orphan:start" {
		return 0, "", nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 3 || parts[0] != "orphan" {
		return 0, "", errors.New("cursor must be a tagged download cursor")
	}
	route, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(route) == 0 {
		return 0, "", errors.New("cursor has an invalid orphan route")
	}
	id, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(id) == 0 {
		return 0, "", errors.New("cursor has an invalid orphan id")
	}
	return 0, string(route) + "\x00" + string(id), nil
}

func encodeOrphanCursor(raw string) string {
	if raw == "" {
		return "orphan:start"
	}
	route, id, ok := strings.Cut(raw, "\x00")
	if !ok || route == "" || id == "" {
		return ""
	}
	return "orphan:" + base64.RawURLEncoding.EncodeToString([]byte(route)) + ":" +
		base64.RawURLEncoding.EncodeToString([]byte(id))
}

func storedDownloadDTO(d core.Download) downloadJSON {
	return downloadJSON{
		ID:          string(d.EngineID),
		GrabID:      d.GrabID,
		Engine:      d.Engine,
		Name:        d.Title,
		State:       string(d.State),
		Progress:    d.Progress,
		BytesDone:   d.BytesDone,
		Size:        d.Size,
		MaxDownRate: d.MaxDownRate,
		MaxUpRate:   d.MaxUpRate,
		ETASeconds:  -1,
		SavePath:    d.SavePath,
		Error:       d.Error,
		CreatedAt:   jsonTime(d.CreatedAt),
		UpdatedAt:   jsonTime(d.UpdatedAt),
	}
}

// applyLiveStatus overlays the engine's snapshot. The engine is authoritative
// for everything it reports; the row keeps the fields the engine has no
// opinion about (what the download was grabbed for, when Caravan started it).
func applyLiveStatus(dto *downloadJSON, status core.DownloadStatus) {
	dto.ID = string(status.ID)
	dto.State = string(status.State)
	dto.Phase = string(status.Phase)
	dto.Name = status.Name
	dto.Progress = status.Progress
	dto.BytesDone = status.BytesDone
	dto.Size = status.Size
	dto.DownRate = status.DownRate
	dto.UpRate = status.UpRate
	dto.ETASeconds = status.ETASeconds
	dto.Ratio = status.Ratio
	dto.SavePath = status.SavePath
	dto.Error = status.Error
	// Only when the engine said so: a router names the backend that answered,
	// and a plain engine says nothing, in which case the row (or the provider's
	// name, for an orphan) already holds the answer.
	if status.Engine != "" {
		dto.Engine = status.Engine
	}
}

func (s *server) handlePauseDownload(w http.ResponseWriter, r *http.Request) {
	s.controlDownload(w, r, "pause download", func(engine core.Engine, id core.DownloadID) error {
		return engine.Pause(r.Context(), id)
	})
}

func (s *server) handleResumeDownload(w http.ResponseWriter, r *http.Request) {
	// The teeth of the dirty-eject flow (SPEC §13): after an unclean shutdown
	// every download comes back paused, and it stays that way until
	// POST /system/verify has proved the database. Writing torrent pieces onto
	// a filesystem nobody has checked is how a dirty eject turns into a corrupt
	// library. Pausing, deleting and listing all stay available — this is the
	// one direction that adds writes.
	if s.dirty.Load() {
		writeError(w, http.StatusConflict,
			"verify the library after the unclean shutdown before resuming downloads")
		return
	}
	s.controlDownload(w, r, "resume download", func(engine core.Engine, id core.DownloadID) error {
		return engine.Resume(r.Context(), id)
	})
}

// handleRetryDownload puts a failed download back to work.
//
// A failed Usenet download is not a dead end: it fetched articles, it may have
// repaired them, and whatever stage went wrong is one stage of several. The
// engine re-enters its stage machine from the top and every stage skips work
// that is already done, so a release that failed to unpack is unpacked again
// rather than fetched again.
//
// Only engines that say they can do this are asked. A torrent engine's
// failures are about the swarm and Resume is already the whole answer there,
// so the capability is absent rather than a no-op — the UI reads the same
// refusal and does not offer the button.
func (s *server) handleRetryDownload(w http.ResponseWriter, r *http.Request) {
	// The same guard Resume carries, for the same reason: after an unclean
	// shutdown nothing may start writing to the library's filesystem until
	// POST /system/verify has proved the database (SPEC §13).
	if s.dirty.Load() {
		writeError(w, http.StatusConflict,
			"verify the library after the unclean shutdown before retrying downloads")
		return
	}
	id, ok := downloadID(w, r)
	if !ok {
		return
	}
	engine, ok := s.requireEngine(w)
	if !ok {
		return
	}
	retrier, ok := engine.(core.EngineRetry)
	if !ok {
		writeError(w, http.StatusBadRequest, "download engine cannot retry a failed download")
		return
	}
	if err := retrier.Retry(r.Context(), id); err != nil {
		s.writeDownloadEngineError(w, "retry download", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// controlDownload runs a pause/resume against the engine. It answers 204: the
// engine's post-change state is whatever the next queue poll reports, and
// echoing a guess here would only be wrong sooner.
func (s *server) controlDownload(w http.ResponseWriter, r *http.Request, msg string, do func(core.Engine, core.DownloadID) error) {
	id, ok := downloadID(w, r)
	if !ok {
		return
	}
	engine, ok := s.requireEngine(w)
	if !ok {
		return
	}
	if err := do(engine, id); err != nil {
		s.writeEngineError(w, msg, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteDownload drops a download, and its downloaded data when
// ?deleteData=true.
//
// The library is never touched by this: an imported file is a hardlink or a
// move away from the download data, so removing a download costs seeding at
// worst, never media (SPEC §13).
func (s *server) handleDeleteDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := downloadID(w, r)
	if !ok {
		return
	}
	deleteData := false
	if raw := r.URL.Query().Get("deleteData"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "deleteData must be true or false")
			return
		}
		deleteData = parsed
	}
	engine, ok := s.requireEngine(w)
	if !ok {
		return
	}
	ctx := r.Context()

	// Read the row before it is gone, so the event can name what was removed.
	name := string(id)
	if row, err := s.st.GetDownloadByEngineID(ctx, id); err == nil {
		name = row.Title
	} else if !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, "get download", err)
		return
	}

	if err := engine.Remove(ctx, id, deleteData); err != nil {
		s.writeEngineError(w, "remove download", err)
		return
	}
	if err := s.st.DeleteDownloadByEngineID(ctx, id); err != nil {
		s.writeStoreError(w, "delete download", err)
		return
	}

	detail := "download data kept"
	if deleteData {
		detail = "download data deleted"
	}
	s.logEvent(ctx, &core.Event{
		Category: "download",
		Message:  "Removed download " + name,
		Detail:   detail,
	})
	w.WriteHeader(http.StatusNoContent)
}

// downloadID reads the {id} path value of the queue endpoints. Unlike the
// library's ids it is an engine-native string (an info hash, an nzo_id), so the
// only thing to validate is that it is there.
func downloadID(w http.ResponseWriter, r *http.Request) (core.DownloadID, bool) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid id")
		return "", false
	}
	return core.DownloadID(id), true
}
