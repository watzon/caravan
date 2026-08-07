package api

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
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
	// Provider is the chain's head, read-only: it is what a client written
	// before chains reads, and the store keeps it in step with Providers.
	Provider string `json:"provider"`
	// Providers is the ordered chain this library identifies new items
	// through, each one of the ids GET /libraries/providers lists. The first
	// that recognizes a title wins a scan; a search asks all of them.
	Providers []string `json:"providers"`
	// IsDefault marks the one library per kind that answers by-kind lookups
	// and receives items added without an explicit target.
	IsDefault bool `json:"is_default"`
	// ItemCount is how many movies and series name this library as theirs —
	// the number the delete guard reports, so the screen can explain a
	// refusal before the user reaches it.
	ItemCount int64 `json:"item_count"`
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
// be distinguishable from "not mentioned in this request". Kind and root path
// are absent because neither is editable.
type libraryPatchRequest struct {
	Name *string `json:"name"`
	// Provider and Providers are two spellings of the same setting: the
	// singular one is still accepted (and read as a chain of one) because it is
	// what every client written before chains sends. Providers wins when both
	// are present.
	Provider         *string   `json:"provider"`
	Providers        *[]string `json:"providers"`
	IsDefault        *bool     `json:"is_default"`
	DLNAVisible      *bool     `json:"dlna_visible"`
	RouteTorrent     *string   `json:"route_torrent"`
	RouteUsenet      *string   `json:"route_usenet"`
	QualityProfileID *int64    `json:"quality_profile_id"`
}

// libraryCreateRequest is the body of POST /libraries.
type libraryCreateRequest struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
	// Provider and Providers read as libraryPatchRequest's do: the singular is
	// a chain of one, and an empty pair defaults to the kind's own provider.
	Provider  string   `json:"provider"`
	Providers []string `json:"providers"`
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

// libraryVisible reports whether this caller may see a library at all.
//
// Every library but one is visible to everybody who can reach this API. The
// adult library is the exception, and not because of what it holds: its row is
// never deleted (store.SetAdultEnabled), so an install that enabled the module
// once and turned it off again would otherwise keep answering with an "Adult"
// pill, its root path and its DLNA state forever. "Off" means the module is
// absent, and a settings screen that still lists its shelf is the trace this
// phase promises not to leave (PLAN phase 9 task 3).
func (s *server) libraryVisible(r *http.Request, kind string) (bool, error) {
	if kind != core.LibraryKindAdult {
		return true, nil
	}
	return s.adultVisible(r)
}

// handleListProviders lists the providers the create form and the chain editor
// may offer.
//
// It is a MERGE of two registries, not one list. The compiled-in descriptors
// (core.Providers) are protocols, and the adult kind is stripped from every one
// of them — the static "Stash-box" descriptor therefore never ships, because
// "stash-box" is a wire dialect and a chain element has to name a catalogue with
// an account and its own UUIDs behind it. Those are the configured instances,
// one descriptor each, and they are added only for a caller the module is
// visible to: a name in a picker is a trace of a module whose promise is absence
// (see libraryVisible).
func (s *server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adult, err := s.adultVisible(r)
	if err != nil {
		s.writeStoreError(w, "read adult settings", err)
		return
	}
	type providerJSON struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Kinds []string `json:"kinds"`
	}
	out := []providerJSON{}
	for _, p := range core.Providers() {
		kinds := make([]string, 0, len(p.Kinds))
		for _, k := range p.Kinds {
			if k == core.LibraryKindAdult {
				continue
			}
			kinds = append(kinds, k)
		}
		if len(kinds) == 0 {
			continue
		}
		out = append(out, providerJSON{ID: p.ID, Name: p.Name, Kinds: kinds})
	}
	if adult {
		instances, err := s.st.ListStashboxInstances(ctx)
		if err != nil {
			s.writeStoreError(w, "list stash-box instances", err)
			return
		}
		for _, in := range instances {
			out = append(out, providerJSON{
				ID:    in.ProviderID,
				Name:  in.Name,
				Kinds: []string{core.LibraryKindAdult},
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// handleCreateLibrary creates a new library beside the seeded ones.
//
// Nothing is written to disk, for AddMovie's reason: an empty folder is a
// library item the scanner cannot see, and the organizer's MkdirAll creates
// the directory the moment the first import needs it.
//
// The new row is created with DLNA sharing off. The DLNA tree carves one
// container per library, so the flag would work — but sharing a library over a
// protocol with no accounts is a decision the owner makes, not one a create
// form makes for them, so it starts down and the Reach card is where it goes
// up.
func (s *server) handleCreateLibrary(w http.ResponseWriter, r *http.Request) {
	var body libraryCreateRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	if body.Kind != core.LibraryKindMovie && body.Kind != core.LibraryKindTV && body.Kind != core.LibraryKindAdult {
		writeError(w, http.StatusBadRequest, "kind must be movie, tv or adult")
		return
	}
	if body.Kind == core.LibraryKindAdult {
		visible, err := s.adultVisible(r)
		if err != nil {
			s.writeStoreError(w, "read adult settings", err)
			return
		}
		if !visible {
			writeError(w, http.StatusBadRequest, "kind must be movie, tv or adult")
			return
		}
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	chain := chainFrom(body.Providers, body.Provider, body.Kind)
	if !s.validProviderChain(ctx, w, chain, body.Kind) {
		return
	}
	root, err := validateLibraryRoot(ctx, s.st, body.RootPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	lib := &core.Library{
		Kind: body.Kind, Name: name, RootPath: root,
		Providers: chain, DLNAVisible: false,
	}
	if err := s.st.CreateLibrary(ctx, lib); err != nil {
		s.writeStoreError(w, "create library", err)
		return
	}
	s.writeLibraryStatus(w, r, *lib, http.StatusCreated)
}

// chainFrom renders a request's two provider spellings as one chain: the list
// when it was sent, the singular id as a chain of one behind it, and the
// kind's own provider when neither was.
func chainFrom(providers []string, provider, kind string) []string {
	if len(providers) > 0 {
		return providers
	}
	if provider == "" {
		provider = core.DefaultProviderForKind(kind)
	}
	if provider == "" {
		return nil
	}
	return []string{provider}
}

// validProviderChain rejects a chain that cannot be walked, writing the
// refusal itself.
//
// Every element must serve the kind, which is what keeps an adult library's
// chain to stash-box ids without a rule that says so: no other compiled-in
// provider serves the adult kind (core.ProviderServes). Duplicates are refused
// because a chain is an ORDER, and an id appearing twice has no second meaning
// — the walk would ask the same provider the same question again.
//
// It is a method taking a context because the third rule is a database
// question: an id whose base is stash-box has to name an instance that is
// actually configured. ProviderServes answers on the base and so accepts
// `stashbox:anything` that is well-formed, which is right for it — which kinds a
// protocol serves is a compiled fact — and leaves "does this box exist here" to
// be asked once, here.
func (s *server) validProviderChain(ctx context.Context, w http.ResponseWriter, chain []string, kind string) bool {
	if len(chain) == 0 {
		writeError(w, http.StatusBadRequest, "at least one provider is required")
		return false
	}
	seen := make(map[string]bool, len(chain))
	for _, id := range chain {
		if seen[id] {
			writeError(w, http.StatusBadRequest, "providers cannot repeat")
			return false
		}
		seen[id] = true
		if !core.ProviderServes(id, kind) {
			writeError(w, http.StatusBadRequest, "provider cannot serve this library kind")
			return false
		}
		known, err := s.knownProviderInstance(ctx, id)
		if err != nil {
			s.writeStoreError(w, "get stash-box instance", err)
			return false
		}
		if !known {
			writeError(w, http.StatusBadRequest, "no stash-box instance named "+id+" is configured")
			return false
		}
	}
	return true
}

// validateLibraryRoot normalizes and checks a new library's root path: a
// clean, forward-slash, storage-root-relative path strictly under library/,
// unique among the existing roots and neither containing nor contained by any
// of them (nesting would make longest-prefix attribution a trap).
func validateLibraryRoot(ctx context.Context, st *store.Store, raw string) (string, error) {
	root := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
	if root == "" {
		return "", errors.New("root_path is required")
	}
	if path.Clean(root) != root || strings.Contains(root, "..") {
		return "", errors.New("root_path must be a clean relative path")
	}
	if !strings.HasPrefix(root, library.LibraryDir+"/") {
		return "", errors.New("root_path must be under library/")
	}
	if root == library.LibraryDir {
		return "", errors.New("root_path cannot be the library directory itself")
	}
	existing, err := st.ListLibraries(ctx)
	if err != nil {
		return "", err
	}
	for _, l := range existing {
		switch {
		case l.RootPath == root:
			return "", errors.New("another library already uses this root")
		case strings.HasPrefix(root, l.RootPath+"/"):
			return "", errors.New("root_path is inside another library's root")
		case strings.HasPrefix(l.RootPath, root+"/"):
			return "", errors.New("root_path contains another library's root")
		}
	}
	return root, nil
}

// handleDeleteLibrary removes an empty, non-default, non-adult library. The
// guards live in store.DeleteLibrary; this maps each refusal to the message
// the screen shows.
func (s *server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	lib, ok := s.getVisibleLibrary(w, r, id)
	if !ok {
		return
	}
	err := s.st.DeleteLibrary(r.Context(), lib.ID)
	switch {
	case errors.Is(err, store.ErrLibraryNotEmpty):
		writeError(w, http.StatusConflict, "the library still has items; move them to another library first")
		return
	case errors.Is(err, store.ErrLibraryIsDefault):
		writeError(w, http.StatusConflict, "the library is its kind's default; make another library the default first")
		return
	case errors.Is(err, store.ErrLibraryIsAdult):
		writeError(w, http.StatusConflict, "adult libraries are managed by the adult module switch")
		return
	case err != nil:
		s.writeStoreError(w, "delete library", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		visible, err := s.libraryVisible(r, l.Kind)
		if err != nil {
			s.writeStoreError(w, "read adult settings", err)
			return
		}
		if !visible {
			continue
		}
		dto, err := s.libraryDTO(ctx, l, indexers)
		if err != nil {
			s.writeStoreError(w, "list library indexers", err)
			return
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out})
}

// getVisibleLibrary is GetLibrary plus libraryVisible, writing the refusal
// itself. A library the caller may not see is 404 rather than 403, for the
// reason requireAdult gives: "this exists and you may not have it" is a worse
// answer than "there is nothing here" on a module whose promise is absence.
func (s *server) getVisibleLibrary(w http.ResponseWriter, r *http.Request, id int64) (*core.Library, bool) {
	lib, err := s.st.GetLibrary(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get library", err)
		return nil, false
	}
	visible, err := s.libraryVisible(r, lib.Kind)
	if err != nil {
		s.writeStoreError(w, "read adult settings", err)
		return nil, false
	}
	if !visible {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	return lib, true
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

	lib, ok := s.getVisibleLibrary(w, r, id)
	if !ok {
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) == "" {
		writeError(w, http.StatusBadRequest, "name cannot be empty")
		return
	}
	var chain []string
	if body.Providers != nil || body.Provider != nil {
		if body.Providers != nil {
			chain = *body.Providers
		} else {
			chain = []string{*body.Provider}
		}
		if !s.validProviderChain(ctx, w, chain, lib.Kind) {
			return
		}
	}
	// Demotion has no meaning of its own: a kind must always have a default,
	// so the flag moves by promoting the successor, never by clearing.
	if body.IsDefault != nil && !*body.IsDefault {
		writeError(w, http.StatusBadRequest, "make another library the default instead")
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

	if body.Name != nil {
		lib.Name = strings.TrimSpace(*body.Name)
	}
	if chain != nil {
		// The head follows the list rather than being set beside it; the store
		// settles the two columns (store.normalizeChain) and this must not
		// hand it a pair that already disagree.
		lib.Providers = chain
		lib.Provider = chain[0]
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
	selectedProfileID := int64(-1)
	if body.QualityProfileID != nil {
		selectedProfileID = *body.QualityProfileID
		if selectedProfileID == 0 {
			lib.QualityProfileID = 0
		}
	}
	if err := s.st.UpdateLibrary(ctx, lib); err != nil {
		s.writeStoreError(w, "update library", err)
		return
	}
	if selectedProfileID > 0 {
		if err := s.st.SetLibraryQualityProfile(ctx, id, selectedProfileID); err != nil {
			s.writeStoreError(w, "set library quality profile", err)
			return
		}
		lib.QualityProfileID = selectedProfileID
	}
	if body.IsDefault != nil && *body.IsDefault && !lib.IsDefault {
		if err := s.st.SetDefaultLibrary(ctx, lib.ID); err != nil {
			s.writeStoreError(w, "set default library", err)
			return
		}
		lib.IsDefault = true
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
	lib, ok := s.getVisibleLibrary(w, r, id)
	if !ok {
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
	s.writeLibraryStatus(w, r, lib, http.StatusOK)
}

func (s *server) writeLibraryStatus(w http.ResponseWriter, r *http.Request, lib core.Library, status int) {
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
	writeJSON(w, status, dto)
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
		// The default is asked for rather than assumed to be the indexer's own
		// list: the Adult library's is the 6000 block instead (see
		// store.DefaultLibraryCategories), and a screen that showed 5000 here
		// would be describing a search that does not happen.
		defaults := store.DefaultLibraryCategories(l.Kind, ix.Categories)
		row := libraryIndexerJSON{
			IndexerID:         ix.ID,
			Name:              ix.Name,
			Type:              ix.Type,
			IndexerEnabled:    ix.Enabled,
			Enabled:           true,
			Categories:        categoryList(defaults),
			DefaultCategories: categoryList(defaults),
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

	count, err := s.st.CountLibraryItems(ctx, l.ID)
	if err != nil {
		return libraryJSON{}, err
	}
	return libraryJSON{
		ID:               l.ID,
		Kind:             l.Kind,
		Name:             l.Name,
		RootPath:         l.RootPath,
		Provider:         l.Provider,
		Providers:        l.ProviderChain(),
		IsDefault:        l.IsDefault,
		ItemCount:        count,
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
