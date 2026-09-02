package api

import (
	"context"
	"errors"
	"net/http"
	"path"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/parse"
	"github.com/watzon/caravan/internal/store"
)

// unmatchedJSON is one file parked in the scan-review queue (SPEC §10.1,
// §13): what the scanner found, the parser's best guess, and why it could not
// be matched.
type unmatchedJSON struct {
	ID     int64  `json:"id"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Reason string `json:"reason"`
	SeenAt string `json:"seen_at"`
	// LibraryID scopes the manual match: an untied universal-search grab
	// already chose a library, and the review screen pre-selects it. 0 (every
	// scan-parked file) means unscoped.
	LibraryID int64      `json:"library_id"`
	Parsed    parsedJSON `json:"parsed"`
}

type parsedJSON struct {
	Title      string  `json:"title"`
	Year       int     `json:"year"`
	Season     int     `json:"season"`
	Episodes   []int   `json:"episodes"`
	SceneDate  string  `json:"scene_date"`
	Quality    string  `json:"quality"`
	Source     string  `json:"source"`
	Codec      string  `json:"codec"`
	Audio      string  `json:"audio"`
	BitDepth   int     `json:"bit_depth"`
	Group      string  `json:"group"`
	Proper     bool    `json:"proper"`
	Repack     bool    `json:"repack"`
	Edition    string  `json:"edition"`
	Confidence float64 `json:"confidence"`
}

func parsedDTO(p core.ParsedRelease) parsedJSON {
	episodes := p.Episodes
	if episodes == nil {
		episodes = []int{}
	}
	return parsedJSON{
		Title:      p.Title,
		Year:       p.Year,
		Season:     p.Season,
		Episodes:   episodes,
		SceneDate:  jsonTime(p.SceneDate),
		Quality:    p.Quality,
		Source:     p.Source,
		Codec:      p.Codec,
		Audio:      p.Audio,
		BitDepth:   p.BitDepth,
		Group:      p.Group,
		Proper:     p.Proper,
		Repack:     p.Repack,
		Edition:    p.Edition,
		Confidence: p.Confidence,
	}
}

// improveUnmatchedParse repairs download rows created before the importer kept
// the release-title parse. Obfuscated payload names are common for Usenet; the
// enclosing release directory is better evidence only when it scores higher.
func (s *server) improveUnmatchedParse(ctx context.Context, f *core.UnmatchedFile) error {
	if f.LibraryID == 0 ||
		(f.Reason != library.ReasonImport && f.Reason != library.ReasonManualGrab) {
		return nil
	}

	lib, err := s.st.GetLibrary(ctx, f.LibraryID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	parseName := parse.Parse
	if lib.Kind == core.LibraryKindAdult {
		parseName = parse.Scene
	}
	candidate := parseName(path.Base(path.Dir(f.Path)))
	if candidate.Confidence <= f.Parsed.Confidence {
		return nil
	}

	f.Parsed = candidate
	return s.st.UpsertUnmatchedFile(ctx, f)
}

// matchRequest is the body of POST /import/queue/{id}/match: the user has told
// Caravan what the file actually is.
type matchRequest struct {
	Type   string `json:"type"`
	TMDBID int64  `json:"tmdb_id"`
	// Provider and ProviderRef are the general spelling of the same answer,
	// resolved by the rules addRequest's pair follows; see itemRefFrom.
	Provider    string `json:"provider"`
	ProviderRef string `json:"provider_ref"`
}

func (s *server) handleImportQueue(w http.ResponseWriter, r *http.Request) {
	files, err := s.st.ListUnmatchedFiles(r.Context())
	if err != nil {
		s.writeStoreError(w, "list unmatched files", err)
		return
	}

	out := make([]unmatchedJSON, 0, len(files))
	for _, f := range files {
		// A file parked into a library (an untied grab's payload) is as
		// invisible as that library is to this caller.
		if visible, ok := s.unmatchedVisible(w, r, &f); !ok {
			return
		} else if !visible {
			continue
		}
		if err := s.improveUnmatchedParse(r.Context(), &f); err != nil {
			s.writeStoreError(w, "repair unmatched parser guess", err)
			return
		}
		out = append(out, unmatchedJSON{
			ID:        f.ID,
			Path:      f.Path,
			Size:      f.Size,
			Reason:    f.Reason,
			SeenAt:    jsonTime(f.SeenAt),
			LibraryID: f.LibraryID,
			Parsed:    parsedDTO(f.Parsed),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// handleImportMatch resolves one parked file against a provider id and hands
// it to the library manager to import.
func (s *server) handleImportMatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var body matchRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	// body.Type is already the item vocabulary itemRefFrom validates a ref
	// against, so the switch only has the scene gate left to do: naming a scene
	// is adult vocabulary, and a caller who cannot see the module is answered
	// as though the type did not exist.
	switch body.Type {
	case MediaTypeMovie, MediaTypeSeries:
	case MediaTypeScene:
		adult, err := s.gate(r).seesAdult(r.Context())
		if err != nil {
			s.writeStoreError(w, "read library access", err)
			return
		}
		if !adult {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "type must be movie, series, or scene")
		return
	}
	ref, ok := s.itemRefFrom(r.Context(), w, body.Provider, body.ProviderRef, body.TMDBID, body.Type)
	if !ok {
		return
	}

	// Resolve the queue entry here so an unknown id is a 404 regardless of how
	// the manager reports it.
	u, err := s.st.GetUnmatchedFile(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get unmatched file", err)
		return
	}
	if visible, ok := s.unmatchedVisible(w, r, u); !ok {
		return
	} else if !visible {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.mgr.MatchUnmatched(r.Context(), id, body.Type, ref); err != nil {
		s.writeManagerError(w, ref.Provider, "match unmatched file", err)
		return
	}
	// Matching a parked file puts the title in the library, which is what a
	// pending request was asking for. See absorbRequests, including why a
	// non-TMDB ref absorbs nothing.
	s.absorbRequests(r.Context(), body.Type, ref.TMDBID())
	writeJSON(w, http.StatusOK, map[string]string{"status": "matched"})
}

// handleImportDelete drops a file from the review queue without touching it on
// disk. The next scan will park it again unless the user moved or deleted it,
// which is the honest behavior: the filesystem is the source of truth.
func (s *server) handleImportDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	f, err := s.st.GetUnmatchedFile(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get unmatched file", err)
		return
	}
	if visible, ok := s.unmatchedVisible(w, r, f); !ok {
		return
	} else if !visible {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.st.DeleteUnmatchedFileByPath(r.Context(), f.Path); err != nil {
		s.writeStoreError(w, "delete unmatched file", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unmatchedVisible reports whether a parked file may be shown to this caller: a
// file scoped to a library is as visible as that library is. A file with no
// library (every scan-parked one) is unscoped and belongs to nobody in
// particular, so it stays visible. The second return is false when the check
// itself failed and the response has been written.
func (s *server) unmatchedVisible(w http.ResponseWriter, r *http.Request, u *core.UnmatchedFile) (bool, bool) {
	visible, err := s.gate(r).visible(r.Context(), u.LibraryID)
	if err != nil {
		s.writeStoreError(w, "read library access", err)
		return false, false
	}
	return visible, true
}
