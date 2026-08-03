package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// libraryJSON is one section of the media library as the Libraries settings
// screen renders it (SPEC §7 `libraries`, PLAN phase 8).
//
// The override fields carry the library's OWN answer, not the resolved one:
// empty and zero mean "this library does not answer, the global setting does",
// which is exactly the distinction the screen draws between an OVERRIDE and a
// GLOBAL DEFAULT. Handing back a resolved value would make every library look
// permanently overridden.
type libraryJSON struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// RootPath is read-only here: it is where the organizer already put the
	// files, and moving the library is POST /system/storage-root/migrate's job.
	RootPath string `json:"root_path"`
	// DLNAVisible advertises this library in the DLNA content tree.
	DLNAVisible bool `json:"dlna_visible"`
	// RouteTorrent and RouteUsenet override the global routing defaults. Empty
	// is no override.
	RouteTorrent string `json:"route_torrent"`
	RouteUsenet  string `json:"route_usenet"`
	// QualityProfileID is the library's default profile for items that name
	// none of their own. Zero is no library default.
	QualityProfileID int64 `json:"quality_profile_id"`
	// Indexers is every configured indexer with this library's answer for it,
	// so the screen renders the whole matrix from one response.
	Indexers []libraryIndexerJSON `json:"indexers"`
}

// libraryIndexerJSON is one row of the per-library indexer matrix.
//
// Enabled and IndexerEnabled are deliberately separate. Enabled is the only
// one this library owns; IndexerEnabled is the indexer's own switch, which a
// library can never reverse (see store.ResolveLibrarySettings). A search
// happens only when both are true, and keeping them apart is what lets the
// screen say "not searched for this library" and "this indexer is off
// everywhere" as the different problems they are.
type libraryIndexerJSON struct {
	IndexerID int64  `json:"indexer_id"`
	Name      string `json:"name"`
	// Type is torznab or newznab, the same value GET /indexers reports.
	Type string `json:"type"`
	// IndexerEnabled is the indexer's global switch, read-only from here.
	IndexerEnabled bool `json:"indexer_enabled"`
	// Enabled is whether this library searches it. True when no override row
	// exists, which is the common case.
	Enabled bool `json:"enabled"`
	// Categories are the categories a search for this library sends: the
	// override when there is one, the indexer's own list otherwise.
	Categories []int `json:"categories"`
	// CategoriesOverridden separates "this library narrowed the list" from
	// "these are the indexer's categories", which look identical otherwise.
	CategoriesOverridden bool `json:"categories_overridden"`
	// DefaultCategories is the indexer's own list, so the screen can show what
	// clearing the override would restore without a second request.
	DefaultCategories []int `json:"default_categories"`
}

// libraryPatchRequest is the body of PATCH /libraries/{id}.
//
// Every field is a pointer because every field has a meaningful zero: an empty
// route and a zero profile id are how an override is cleared, and they have to
// be distinguishable from "not mentioned in this request". Kind, name and root
// path are absent because none of them is editable.
type libraryPatchRequest struct {
	DLNAVisible      *bool   `json:"dlna_visible"`
	RouteTorrent     *string `json:"route_torrent"`
	RouteUsenet      *string `json:"route_usenet"`
	QualityProfileID *int64  `json:"quality_profile_id"`
}

// libraryIndexerRequest is the body of PUT /libraries/{id}/indexers/{indexerID}.
//
// Categories is a plain slice rather than a pointer because JSON already draws
// the distinction the store stores: null (or absent) is "no override, use the
// indexer's categories", and [] is the override "search unfiltered".
type libraryIndexerRequest struct {
	Enabled    *bool `json:"enabled"`
	Categories []int `json:"categories"`
}

func (s *server) handleListLibraries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	libraries, err := s.st.ListLibraries(ctx)
	if err != nil {
		s.writeStoreError(w, "list libraries", err)
		return
	}
	indexers, err := s.st.ListIndexers(ctx)
	if err != nil {
		s.writeStoreError(w, "list indexers", err)
		return
	}

	out := make([]libraryJSON, 0, len(libraries))
	for _, l := range libraries {
		dto, err := s.libraryDTO(ctx, l, indexers)
		if err != nil {
			s.writeStoreError(w, "list library indexers", err)
			return
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out})
}

// handleUpdateLibrary edits the settings a library may answer for itself. It is
// a PATCH because the screen saves one card at a time, and because a PUT would
// have to carry root_path and kind — neither of which it may change.
func (s *server) handleUpdateLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body libraryPatchRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	lib, err := s.st.GetLibrary(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get library", err)
		return
	}
	// The routing values are the same values the global settings hold, so they
	// go through the same validator: an id that is gone, disabled or of the
	// wrong protocol is silently ignored at grab time, which is exactly the
	// quiet failure SPEC §13 refuses.
	routes := map[string]string{}
	if body.RouteTorrent != nil {
		routes[store.SettingRouteTorrent] = *body.RouteTorrent
	}
	if body.RouteUsenet != nil {
		routes[store.SettingRouteUsenet] = *body.RouteUsenet
	}
	if err := s.validateRouteSettings(ctx, routes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.QualityProfileID != nil && *body.QualityProfileID != 0 {
		if _, err := s.st.GetQualityProfile(ctx, *body.QualityProfileID); err != nil {
			s.writeStoreError(w, "get quality profile", err)
			return
		}
	}

	if body.DLNAVisible != nil {
		lib.DLNAVisible = *body.DLNAVisible
	}
	if body.RouteTorrent != nil {
		lib.RouteTorrent = *body.RouteTorrent
	}
	if body.RouteUsenet != nil {
		lib.RouteUsenet = *body.RouteUsenet
	}
	if body.QualityProfileID != nil {
		lib.QualityProfileID = *body.QualityProfileID
	}
	if err := s.st.UpdateLibrary(ctx, lib); err != nil {
		s.writeStoreError(w, "update library", err)
		return
	}
	s.writeLibrary(w, r, *lib)
}

// handleSetLibraryIndexer writes one (library, indexer) search override.
//
// Sending enabled with a null categories list is how an override is undone: it
// is what an absent row already means, so the screen's "use the indexer's
// categories" needs no separate verb.
func (s *server) handleSetLibraryIndexer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	indexerID, err := strconv.ParseInt(r.PathValue("indexerID"), 10, 64)
	if err != nil || indexerID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid indexer id")
		return
	}
	var body libraryIndexerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	for _, cat := range body.Categories {
		if cat <= 0 {
			writeError(w, http.StatusBadRequest, "categories must be positive integers")
			return
		}
	}
	ctx := r.Context()

	// Both rows are read first so an override against something that does not
	// exist is a 404 rather than a row pointing at nothing.
	lib, err := s.st.GetLibrary(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get library", err)
		return
	}
	if _, err := s.st.GetIndexer(ctx, indexerID); err != nil {
		s.writeStoreError(w, "get indexer", err)
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	override := core.LibraryIndexer{
		LibraryID: lib.ID, IndexerID: indexerID,
		Enabled: enabled, Categories: body.Categories,
	}
	if err := s.st.SetLibraryIndexer(ctx, &override); err != nil {
		s.writeStoreError(w, "set library indexer", err)
		return
	}
	s.writeLibrary(w, r, *lib)
}

// writeLibrary answers a write with the library's whole state, so the screen
// re-renders the identity, routing and indexer cards from one response instead
// of guessing what the write did to the rest of them.
func (s *server) writeLibrary(w http.ResponseWriter, r *http.Request, lib core.Library) {
	ctx := r.Context()
	indexers, err := s.st.ListIndexers(ctx)
	if err != nil {
		s.writeStoreError(w, "list indexers", err)
		return
	}
	dto, err := s.libraryDTO(ctx, lib, indexers)
	if err != nil {
		s.writeStoreError(w, "list library indexers", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// libraryDTO joins a library with every configured indexer and the overrides it
// stored for them. indexers is passed in so listing every library costs one
// query for the indexer table rather than one per library.
func (s *server) libraryDTO(ctx context.Context, l core.Library, indexers []core.IndexerConfig) (libraryJSON, error) {
	overrides, err := s.st.ListLibraryIndexers(ctx, l.ID)
	if err != nil {
		return libraryJSON{}, err
	}
	byIndexer := make(map[int64]core.LibraryIndexer, len(overrides))
	for _, o := range overrides {
		byIndexer[o.IndexerID] = o
	}

	rows := make([]libraryIndexerJSON, 0, len(indexers))
	for _, ix := range indexers {
		row := libraryIndexerJSON{
			IndexerID:         ix.ID,
			Name:              ix.Name,
			Type:              ix.Type,
			IndexerEnabled:    ix.Enabled,
			Enabled:           true,
			Categories:        categoryList(ix.Categories),
			DefaultCategories: categoryList(ix.Categories),
		}
		if o, ok := byIndexer[ix.ID]; ok {
			row.Enabled = o.Enabled
			if o.Categories != nil {
				row.Categories = categoryList(o.Categories)
				row.CategoriesOverridden = true
			}
		}
		rows = append(rows, row)
	}

	return libraryJSON{
		ID:               l.ID,
		Kind:             l.Kind,
		Name:             l.Name,
		RootPath:         l.RootPath,
		DLNAVisible:      l.DLNAVisible,
		RouteTorrent:     l.RouteTorrent,
		RouteUsenet:      l.RouteUsenet,
		QualityProfileID: l.QualityProfileID,
		Indexers:         rows,
	}, nil
}

// categoryList renders a category list as an array rather than null, which is
// what indexerDTO does for the same reason: the client indexes into it.
func categoryList(cats []int) []int {
	if cats == nil {
		return []int{}
	}
	return cats
}
