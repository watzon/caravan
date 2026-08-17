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
	// Icon names the glyph the navigation draws for this library. Empty means
	// "the kind's default", which the client resolves — see core.Library.Icon
	// for why the server keeps no list of icon names.
	Icon string `json:"icon"`
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
	// Active is the library's master switch: false is dormant for everyone,
	// admins included, and deletes nothing.
	Active bool `json:"active"`
	// Restricted narrows the library to the accounts named in its access
	// roster, plus admins. False is every account.
	Restricted bool `json:"restricted"`
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
	// Icon is the glyph name, empty to go back to the kind's default. It is
	// validated for SHAPE only (validLibraryIcon), so a client can ship a new
	// glyph without a server release.
	Icon *string `json:"icon"`
	// Provider and Providers are two spellings of the same setting: the
	// singular one is still accepted (and read as a chain of one) because it is
	// what every client written before chains sends. Providers wins when both
	// are present.
	Provider  *string   `json:"provider"`
	Providers *[]string `json:"providers"`
	IsDefault *bool     `json:"is_default"`
	// Active is the master switch: false is dormant for everyone, admins
	// included, and deletes nothing.
	//
	// `restricted` is deliberately NOT here. It is written only by PUT
	// /libraries/{id}/access, together with the roster it applies to, because
	// the two are one decision — one door per invariant, so a PATCH cannot
	// leave a library restricted to nobody.
	Active           *bool   `json:"active"`
	DLNAVisible      *bool   `json:"dlna_visible"`
	RouteTorrent     *string `json:"route_torrent"`
	RouteUsenet      *string `json:"route_usenet"`
	QualityProfileID *int64  `json:"quality_profile_id"`
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

// handleListProviders lists the providers the create form and the chain editor
// may offer.
//
// It is a MERGE of two registries, not one list. The compiled-in descriptors
// (core.Providers) are protocols, and the adult kind is stripped from every one
// of them — the static "Stash-box" descriptor therefore never ships, because
// "stash-box" is a wire dialect and a chain element has to name a catalogue with
// an account and its own UUIDs behind it. Those are the configured instances,
// one descriptor each, and they are added only for a caller who can see an
// adult library: a name in a picker is a trace of a shelf whose promise is
// absence.
//
// An admin who could CREATE an adult library still sees no instance here until
// one exists, and that costs nothing: the instance routes are behind the same
// gate, so an install with no adult library has no instance to offer either.
// The picker fills itself in the order the bootstrap runs — library, then
// endpoint, then the chain editor that names it.
func (s *server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adult, err := s.gate(r).seesAdult(ctx)
	if err != nil {
		s.writeStoreError(w, "read library access", err)
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
//
// Creating an ADULT library is how the adult module is turned on, and this
// route is the only door into /adult. Stash-box instance CRUD sits on the
// admin mux so an endpoint can be added first — Settings → Metadata is
// reachable without a library, and the Add-library form points there.
// The row is born RESTRICTED — to the admins alone until somebody is named —
// which is what the module's own switch used to guarantee. Nothing here asks
// whether an endpoint is configured: the screen warns, and a library whose
// chain resolves to no box parks its scans rather than failing them (see
// library.adultChain).
func (s *server) handleCreateLibrary(w http.ResponseWriter, r *http.Request) {
	var body libraryCreateRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	if !core.ValidLibraryKind(body.Kind) {
		writeError(w, http.StatusBadRequest, "kind must be movie, tv, anime or adult")
		return
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
		Restricted: body.Kind == core.LibraryKindAdult,
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
//
// That third rule has exactly one exception, and it is what makes the module
// bootstrappable: on an install with NO stash-box instance at all, the bare
// legacy id is accepted. The instance routes live under /adult, which is absent
// until an adult library exists, so the first library necessarily predates the
// first box — and `stashbox` is the id the first instance ever created is
// minted with (mintStashboxInstance), so the chain is a forward reference to
// the endpoint about to be configured rather than a name for a box this install
// has never held. A qualified `stashbox:something` gets no such benefit: that
// names a particular box, and there is nothing for it to resolve into later.
func (s *server) validProviderChain(ctx context.Context, w http.ResponseWriter, chain []string, kind string) bool {
	if len(chain) == 0 {
		writeError(w, http.StatusBadRequest, "at least one provider is required")
		return false
	}
	bootstrap := false
	if kind == core.LibraryKindAdult {
		instances, err := s.st.ListStashboxInstances(ctx)
		if err != nil {
			s.writeStoreError(w, "list stash-box instances", err)
			return false
		}
		bootstrap = len(instances) == 0
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
		if bootstrap && id == core.ProviderStashbox {
			continue
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

// handleDeleteLibrary removes an empty, non-default library. The guards live in
// store.DeleteLibrary; this maps each refusal to the message the screen shows.
//
// An adult library is deleted under the same two guards as any other. Which
// leaves one install unable to delete: exactly one adult library, its kind's
// default, switched off. That is ErrLibraryIsDefault doing its job and not a
// gap to close — every by-kind lookup needs an answer, and `active=0` is
// already the "off" that deletion was never the right spelling of.
func (s *server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	lib, ok := s.manageableLibrary(w, r, id)
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

	gate := s.gate(r)
	out := make([]libraryJSON, 0, len(libraries))
	for _, l := range libraries {
		// A library the caller may not have is dropped rather than greyed: a row
		// carrying a name, a root path and a DLNA state is exactly the trace
		// that "this shelf is not here for you" promises not to leave.
		//
		// An INACTIVE one is kept, because this is the management surface and
		// the row carries `active: false` for the screen to grey it with. It is
		// the only list the toggle that undoes dormancy can be reached from, and
		// this route is admin-only (routePolicies, and memberAllowed names none
		// of it) — so the row never reaches somebody a restriction hid it from.
		visible, err := gate.manages(ctx, l)
		if err != nil {
			s.writeStoreError(w, "read library access", err)
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

// visibleLibrary resolves a library for a CONTENT route — searching it,
// grabbing into it, adding to it, moving into it, reading its parked files —
// writing the refusal itself.
//
// A library the caller may not see is 404 rather than 403, for the reason
// requireAdult gives: "this exists and you may not have it" is a worse answer
// than "there is nothing here" on a shelf whose promise is absence. A library
// that is not there at all gets the identical answer, so the two cannot be told
// apart from outside.
func (s *server) visibleLibrary(w http.ResponseWriter, r *http.Request, id int64) (*core.Library, bool) {
	gate := s.gate(r)
	lib, ok, err := gate.library(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get library", err)
		return nil, false
	}
	if ok {
		visible, err := gate.allows(r.Context(), lib)
		if err != nil {
			s.writeStoreError(w, "read library access", err)
			return nil, false
		}
		if visible {
			return &lib, true
		}
	}
	writeError(w, http.StatusNotFound, "not found")
	return nil, false
}

// manageableLibrary resolves a library for an admin MANAGEMENT route — the
// settings card behind it: PATCH, DELETE, the per-indexer matrix.
//
// It is a separate door from visibleLibrary on purpose, and the two must never
// be swapped. Content routes ask "may this caller have what is on the shelf";
// management routes ask "may this caller work the shelf's switches", and those
// diverge the moment a library can be switched off: the toggle that undoes
// `active=0` has to stay reachable, or an owner who hid a library from
// themselves has hidden the only way back.
//
// So it admits an INACTIVE library, which is the whole difference: `active=0`
// is dormant for everyone including the admin who set it, and every content
// route answers 404 for it — but PATCH, DELETE, the indexer matrix and the
// access card still reach it, because those are the switches, and a switch you
// cannot get back to is a trapdoor.
//
// Restriction it does NOT bypass on its own; core.LibraryVisible already lets
// an admin past that, and every caller of this is admin-only (routeAdmin, and
// memberAllowed names none of them). A member reaching here is a routing bug,
// and the gate's own answer is the right one for it.
func (s *server) manageableLibrary(w http.ResponseWriter, r *http.Request, id int64) (*core.Library, bool) {
	gate := s.gate(r)
	lib, ok, err := gate.library(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get library", err)
		return nil, false
	}
	if ok {
		visible, err := gate.manages(r.Context(), lib)
		if err != nil {
			s.writeStoreError(w, "read library access", err)
			return nil, false
		}
		if visible {
			return &lib, true
		}
	}
	writeError(w, http.StatusNotFound, "not found")
	return nil, false
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

	lib, ok := s.manageableLibrary(w, r, id)
	if !ok {
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) == "" {
		writeError(w, http.StatusBadRequest, "name cannot be empty")
		return
	}
	if body.Icon != nil && !validLibraryIcon(*body.Icon) {
		writeError(w, http.StatusBadRequest, "icon must be up to 32 letters")
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
	if body.Icon != nil {
		lib.Icon = *body.Icon
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
	// Its own writer, after UpdateLibrary rather than inside it, because it is
	// its own decision: everything above is a setting, and this is whether the
	// library exists for anybody at all (store.SetLibraryActive).
	if body.Active != nil && *body.Active != lib.Active {
		if err := s.st.SetLibraryActive(ctx, lib.ID, *body.Active); err != nil {
			s.writeStoreError(w, "set library active", err)
			return
		}
		lib.Active = *body.Active
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
	lib, ok := s.manageableLibrary(w, r, id)
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
		Icon:             l.Icon,
		RootPath:         l.RootPath,
		Provider:         l.Provider,
		Providers:        l.ProviderChain(),
		IsDefault:        l.IsDefault,
		Active:           l.Active,
		Restricted:       l.Restricted,
		ItemCount:        count,
		DLNAVisible:      l.DLNAVisible,
		RouteTorrent:     l.RouteTorrent,
		RouteUsenet:      l.RouteUsenet,
		QualityProfileID: l.QualityProfileID,
		Indexers:         rows,
	}, nil
}

// validLibraryIcon checks the SHAPE of an icon name and nothing else: letters
// only, at most 32 of them, and empty is fine because empty is how a library
// goes back to its kind's default glyph.
//
// It deliberately does not check the name against a list. The glyphs live in
// the SPA, and a server-side allow-list would be a second copy of that list
// which goes stale the first time a glyph is added — the client already falls
// back to the kind default for a name it does not recognise, so an unknown name
// costs nothing. What the shape rule buys is that the value stays a bare
// identifier: no markup, no path, no separator a future consumer could read as
// structure.
func validLibraryIcon(icon string) bool {
	if len(icon) > 32 {
		return false
	}
	for i := 0; i < len(icon); i++ {
		c := icon[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// categoryList renders a category list as an array rather than null, which is
// what indexerDTO does for the same reason: the client indexes into it.
func categoryList(cats []int) []int {
	if cats == nil {
		return []int{}
	}
	return cats
}
