package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"

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
	Name   string `json:"name"`
	State  string `json:"state"`
	// Progress is completion in [0,1].
	Progress  float64 `json:"progress"`
	BytesDone int64   `json:"bytes_done"`
	Size      int64   `json:"size"`
	DownRate  int64   `json:"down_rate"`
	UpRate    int64   `json:"up_rate"`
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
func (s *server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
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
		out = append(out, dto)
	}

	// Anything the engine knows about that Caravan does not is surfaced rather
	// than hidden: a download added out of band, or one whose row was lost, is
	// still the user's data (SPEC §13).
	orphans := make([]downloadJSON, 0, len(live))
	for _, status := range live {
		dto := downloadJSON{Engine: s.engineName(), ETASeconds: -1}
		applyLiveStatus(&dto, status)
		orphans = append(orphans, dto)
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })

	writeJSON(w, http.StatusOK, map[string]any{"downloads": append(out, orphans...)})
}

func storedDownloadDTO(d core.Download) downloadJSON {
	return downloadJSON{
		ID:         string(d.EngineID),
		GrabID:     d.GrabID,
		Engine:     d.Engine,
		Name:       d.Title,
		State:      string(d.State),
		Progress:   d.Progress,
		BytesDone:  d.BytesDone,
		Size:       d.Size,
		MaxDownRate: d.MaxDownRate,
		MaxUpRate:   d.MaxUpRate,
		ETASeconds: -1,
		SavePath:   d.SavePath,
		Error:      d.Error,
		CreatedAt:  jsonTime(d.CreatedAt),
		UpdatedAt:  jsonTime(d.UpdatedAt),
	}
}

// applyLiveStatus overlays the engine's snapshot. The engine is authoritative
// for everything it reports; the row keeps the fields the engine has no
// opinion about (what the download was grabbed for, when Caravan started it).
func applyLiveStatus(dto *downloadJSON, status core.DownloadStatus) {
	dto.ID = string(status.ID)
	dto.State = string(status.State)
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
}

func (s *server) handlePauseDownload(w http.ResponseWriter, r *http.Request) {
	s.controlDownload(w, r, "pause download", func(engine core.Engine, id core.DownloadID) error {
		return engine.Pause(r.Context(), id)
	})
}

func (s *server) handleResumeDownload(w http.ResponseWriter, r *http.Request) {
	s.controlDownload(w, r, "resume download", func(engine core.Engine, id core.DownloadID) error {
		return engine.Resume(r.Context(), id)
	})
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
