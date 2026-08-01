package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// indexerJSON is a configured search source (SPEC §5.1).
//
// The API key is echoed back, as GET /settings does with the TMDB key: the API
// is single-user and unauthenticated until phase 5, and the settings screen has
// to be able to render the value the user typed. SPEC §12's rule is that
// credentials stay out of the logs and out of caravan.yaml, which the access
// log (path only, never the query string) and the store both honor.
type indexerJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	APIKey     string `json:"api_key"`
	Type       string `json:"type"`
	Categories []int  `json:"categories"`
	Enabled    bool   `json:"enabled"`
}

func indexerDTO(c core.IndexerConfig) indexerJSON {
	categories := c.Categories
	if categories == nil {
		categories = []int{}
	}
	return indexerJSON{
		ID:         c.ID,
		Name:       c.Name,
		URL:        c.URL,
		APIKey:     c.APIKey,
		Type:       c.Type,
		Categories: categories,
		Enabled:    c.Enabled,
	}
}

// indexerRequest is the body of POST /indexers and PUT /indexers/{id}. PUT
// replaces the whole configuration rather than patching it, which is why every
// field is read on both.
//
// Enabled is a pointer so an omitted flag means "enabled": a user who just
// added an indexer wants it searched, and requiring the flag would make the
// common request longer than it needs to be.
type indexerRequest struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	APIKey     string `json:"api_key"`
	Type       string `json:"type"`
	Categories []int  `json:"categories"`
	Enabled    *bool  `json:"enabled"`
}

// config validates the body and turns it into a store-ready configuration. The
// returned message is empty when the body is valid.
func (b indexerRequest) config() (core.IndexerConfig, string) {
	name := strings.TrimSpace(b.Name)
	if name == "" {
		return core.IndexerConfig{}, "name is required"
	}
	raw := strings.TrimSpace(b.URL)
	if raw == "" {
		return core.IndexerConfig{}, "url is required"
	}
	// Parsed rather than pattern-matched: the client builds request URLs from
	// this string, and a value it cannot parse would fail later, per search,
	// instead of here where the user can fix it.
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return core.IndexerConfig{}, "url must be an http or https URL"
	}
	if b.Type != core.IndexerTypeTorznab && b.Type != core.IndexerTypeNewznab {
		return core.IndexerConfig{}, "type must be torznab or newznab"
	}
	for _, cat := range b.Categories {
		if cat <= 0 {
			return core.IndexerConfig{}, "categories must be positive integers"
		}
	}

	enabled := true
	if b.Enabled != nil {
		enabled = *b.Enabled
	}
	return core.IndexerConfig{
		Name:       name,
		URL:        strings.TrimRight(raw, "/"),
		APIKey:     strings.TrimSpace(b.APIKey),
		Type:       b.Type,
		Categories: b.Categories,
		Enabled:    enabled,
	}, ""
}

func (s *server) handleListIndexers(w http.ResponseWriter, r *http.Request) {
	indexers, err := s.st.ListIndexers(r.Context())
	if err != nil {
		s.writeStoreError(w, "list indexers", err)
		return
	}

	out := make([]indexerJSON, 0, len(indexers))
	for _, c := range indexers {
		out = append(out, indexerDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"indexers": out})
}

func (s *server) handleCreateIndexer(w http.ResponseWriter, r *http.Request) {
	var body indexerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	cfg, msg := body.config()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()

	if !s.indexerNameFree(ctx, w, cfg.Name, 0) {
		return
	}
	if err := s.st.UpsertIndexer(ctx, &cfg); err != nil {
		s.writeStoreError(w, "create indexer", err)
		return
	}
	writeJSON(w, http.StatusCreated, indexerDTO(cfg))
}

func (s *server) handleUpdateIndexer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body indexerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	cfg, msg := body.config()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()

	// Read first so updating an indexer that never existed is a 404 rather than
	// a silent insert.
	if _, err := s.st.GetIndexer(ctx, id); err != nil {
		s.writeStoreError(w, "get indexer", err)
		return
	}
	if !s.indexerNameFree(ctx, w, cfg.Name, id) {
		return
	}
	cfg.ID = id
	if err := s.st.UpsertIndexer(ctx, &cfg); err != nil {
		s.writeStoreError(w, "update indexer", err)
		return
	}
	writeJSON(w, http.StatusOK, indexerDTO(cfg))
}

// handleDeleteIndexer removes the configuration. Cached releases and past
// grabs keep the indexer's denormalized name, so history survives the delete.
func (s *server) handleDeleteIndexer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if _, err := s.st.GetIndexer(r.Context(), id); err != nil {
		s.writeStoreError(w, "get indexer", err)
		return
	}
	if err := s.st.DeleteIndexer(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete indexer", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestIndexer asks the indexer whether it answers with the stored
// credentials (PLAN phase 2, task 1).
//
// A failed test is reported with the indexer's own message, because "it did not
// work" without a reason is useless for fixing a wrong API key or a typo'd URL.
// That message is deliberately not logged: it can quote the request URL, and
// SPEC §12 keeps indexer credentials out of the logs.
func (s *server) handleTestIndexer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	newClient, ok := s.requireIndexerClients(w)
	if !ok {
		return
	}

	cfg, err := s.st.GetIndexer(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get indexer", err)
		return
	}
	if err := newClient(*cfg).Test(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, "indexer test failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// indexerNameFree reports whether name is available, writing a 409 when it is
// not. indexers.name is unique, so without this check a duplicate name — a
// plain user mistake — would surface as a 500.
func (s *server) indexerNameFree(ctx context.Context, w http.ResponseWriter, name string, exceptID int64) bool {
	indexers, err := s.st.ListIndexers(ctx)
	if err != nil {
		s.writeStoreError(w, "list indexers", err)
		return false
	}
	for _, c := range indexers {
		if c.ID != exceptID && c.Name == name {
			writeError(w, http.StatusConflict, "an indexer named "+name+" already exists")
			return false
		}
	}
	return true
}
