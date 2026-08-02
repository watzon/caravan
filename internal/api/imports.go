package api

import (
	"net/http"

	"github.com/watzon/caravan/internal/core"
)

// unmatchedJSON is one file parked in the scan-review queue (SPEC §10.1,
// §13): what the scanner found, the parser's best guess, and why it could not
// be matched.
type unmatchedJSON struct {
	ID     int64      `json:"id"`
	Path   string     `json:"path"`
	Size   int64      `json:"size"`
	Reason string     `json:"reason"`
	SeenAt string     `json:"seen_at"`
	Parsed parsedJSON `json:"parsed"`
}

type parsedJSON struct {
	Title      string  `json:"title"`
	Year       int     `json:"year"`
	Season     int     `json:"season"`
	Episodes   []int   `json:"episodes"`
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

// matchRequest is the body of POST /import/queue/{id}/match: the user has told
// Caravan what the file actually is.
type matchRequest struct {
	Type   string `json:"type"`
	TMDBID int64  `json:"tmdb_id"`
}

func (s *server) handleImportQueue(w http.ResponseWriter, r *http.Request) {
	files, err := s.st.ListUnmatchedFiles(r.Context())
	if err != nil {
		s.writeStoreError(w, "list unmatched files", err)
		return
	}

	out := make([]unmatchedJSON, 0, len(files))
	for _, f := range files {
		out = append(out, unmatchedJSON{
			ID:     f.ID,
			Path:   f.Path,
			Size:   f.Size,
			Reason: f.Reason,
			SeenAt: jsonTime(f.SeenAt),
			Parsed: parsedDTO(f.Parsed),
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
	if body.Type != MediaTypeMovie && body.Type != MediaTypeSeries {
		writeError(w, http.StatusBadRequest, "type must be movie or series")
		return
	}
	if body.TMDBID <= 0 {
		writeError(w, http.StatusBadRequest, "tmdb_id is required")
		return
	}

	// Resolve the queue entry here so an unknown id is a 404 regardless of how
	// the manager reports it.
	if _, err := s.st.GetUnmatchedFile(r.Context(), id); err != nil {
		s.writeStoreError(w, "get unmatched file", err)
		return
	}
	if err := s.mgr.MatchUnmatched(r.Context(), id, body.Type, body.TMDBID); err != nil {
		s.writeManagerError(w, "match unmatched file", err)
		return
	}
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
	if err := s.st.DeleteUnmatchedFileByPath(r.Context(), f.Path); err != nil {
		s.writeStoreError(w, "delete unmatched file", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
