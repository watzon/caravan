package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// The stash-box instance CRUD.
//
// These routes keep the /adult/ URL but live on the admin mux. They are
// metadata credentials (same job as a TMDB key) and Settings → Metadata has
// to edit them before the first adult library exists. That library is the door
// into requireAdult, so putting CRUD behind it made the Add-library warning
// unsatisfiable. Members still cannot reach them: memberAllowed names none of
// these.
//
// The shape is the indexers one, request struct, config() validator,
// has_api_key redaction, a stored-credential test beside a body-only test,
// because the two are the same object: a configured remote with a name, a URL
// and a write-only key.

// stashboxInstanceJSON is one configured endpoint as the settings screen renders
// it. The key is write-only (SPEC §12); HasAPIKey tells the editor whether one
// is already on file.
type stashboxInstanceJSON struct {
	ID int64 `json:"id"`
	// ProviderID is the value stored in every pinned row and in every provider
	// chain. It is read-only: renaming an instance must never re-point the rows
	// pinned to it.
	ProviderID string `json:"provider_id"`
	Name       string `json:"name"`
	Endpoint   string `json:"endpoint"`
	HasAPIKey  bool   `json:"has_api_key"`
	// SceneFilters is this instance's dialect, not the default instance's. A
	// manual match can switch providers, so the picker needs the answer before
	// it draws controls the selected endpoint would refuse.
	SceneFilters *sceneFiltersJSON `json:"scene_filters,omitempty"`
	// LibraryCount and ItemCount are what the delete guard reports, carried on
	// every row so the screen can explain a refusal before the user reaches it.
	LibraryCount int `json:"library_count"`
	ItemCount    int `json:"item_count"`
}

// stashboxInstanceRequest is the body of the create, update and test-config
// routes.
//
// APIKey is a pointer for the indexerRequest reason: omitted or null keeps the
// stored credential, an explicit empty string clears it. Endpoint is a plain
// string because it is immutable after creation. An update either repeats it or
// omits it, and neither of those is a change.
type stashboxInstanceRequest struct {
	Name     string  `json:"name"`
	Endpoint string  `json:"endpoint"`
	APIKey   *string `json:"api_key"`
}

// config validates the body and turns it into a store-ready instance, leaving
// ProviderID for the caller to mint. The returned message is empty when the body
// is valid.
func (b stashboxInstanceRequest) config(apiKey string) (core.StashboxInstance, string) {
	name := strings.TrimSpace(b.Name)
	if name == "" {
		return core.StashboxInstance{}, "name is required"
	}
	endpoint := strings.TrimRight(strings.TrimSpace(b.Endpoint), "/")
	if endpoint == "" {
		return core.StashboxInstance{}, "endpoint is required"
	}
	if err := stashboxEndpointShape(endpoint); err != nil {
		return core.StashboxInstance{}, err.Error()
	}
	return core.StashboxInstance{
		Name:     name,
		Endpoint: endpoint,
		APIKey:   strings.TrimSpace(apiKey),
	}, ""
}

// stashboxEndpointShape refuses an endpoint that could never be called.
//
// It is parsed rather than pattern-matched, for indexerRequest.config's reason:
// the client builds request URLs from this string, and a value it cannot parse
// would fail later, per search, instead of here where the user can fix it.
func stashboxEndpointShape(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("endpoint must be an absolute http or https URL")
	}
	return nil
}

func (s *server) handleListStashboxInstances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	instances, err := s.st.ListStashboxInstances(ctx)
	if err != nil {
		s.writeStoreError(w, "list stash-box instances", err)
		return
	}

	out := make([]stashboxInstanceJSON, 0, len(instances))
	for _, in := range instances {
		dto, err := s.stashboxInstanceDTO(ctx, in)
		if err != nil {
			s.writeStoreError(w, "count stash-box instance use", err)
			return
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": out})
}

// handleCreateStashboxInstance mints an instance.
//
// The id follows the migration's rule: the first instance on an install takes
// the bare id `stashbox`, which is the id every adult row written before
// instances existed already carries, and every one after it takes
// `stashbox:<slug>`. A fresh install therefore reaches the same state an
// upgraded one is carried into, and neither has a row nothing can resolve.
func (s *server) handleCreateStashboxInstance(w http.ResponseWriter, r *http.Request) {
	var body stashboxInstanceRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	in, ok := s.mintStashboxInstance(ctx, w, body)
	if !ok {
		return
	}
	dto, err := s.stashboxInstanceDTO(ctx, *in)
	if err != nil {
		s.writeStoreError(w, "count stash-box instance use", err)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

// mintStashboxInstance validates a create body, derives the instance's
// permanent id and writes the row, writing its own refusals and reporting
// whether it got that far.
//
// It is a function rather than the handler's body because the id-minting rule
// is the load-bearing part and has to stay in one place: the bare `stashbox` is
// reserved for an install's first instance, whatever creates it, and a second
// copy of that rule is how a fresh install ends up with an id that differs from
// the one an upgrade is carried into.
func (s *server) mintStashboxInstance(ctx context.Context, w http.ResponseWriter,
	body stashboxInstanceRequest,
) (*core.StashboxInstance, bool) {
	apiKey := ""
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	in, msg := body.config(apiKey)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return nil, false
	}

	existing, err := s.st.ListStashboxInstances(ctx)
	if err != nil {
		s.writeStoreError(w, "list stash-box instances", err)
		return nil, false
	}
	if len(existing) == 0 {
		in.ProviderID = core.ProviderStashbox
	} else {
		slug := core.ProviderSlug(in.Name)
		if slug == "" {
			// ProviderSlug is a suggestion, not a guarantee: a name with no
			// character the slug alphabet has room for yields "". Naming the
			// instance again in characters it can spell is the fix; inventing an
			// id here would mint one nobody can recognize in a chain.
			writeError(w, http.StatusBadRequest,
				"name must contain at least one letter or digit")
			return nil, false
		}
		in.ProviderID = core.ProviderStashbox + ":" + slug
	}
	if !s.stashboxInstanceFree(w, existing, in.Name, in.ProviderID, 0) {
		return nil, false
	}

	if err := s.st.UpsertStashboxInstance(ctx, &in); err != nil {
		s.writeStoreError(w, "create stash-box instance", err)
		return nil, false
	}
	return &in, true
}

// handleUpdateStashboxInstance edits the two fields an instance owns after
// creation: its label and its credential.
//
// A new key is proved live before it is written. That is the invariant the
// deleted guardAdultCredentialEdit held from the settings side (the module is
// on only while a credential that was proved sits behind it, and an edit is as
// much a way to break it as a bad enable) moved to the door the credential now
// comes in through.
func (s *server) handleUpdateStashboxInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body stashboxInstanceRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	// Read first so updating an instance that never existed is a 404 rather than
	// a silent insert, and so an omitted credential can preserve the stored one.
	stored, err := s.st.GetStashboxInstance(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get stash-box instance", err)
		return
	}
	apiKey := stored.APIKey
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	// The form round-trips the endpoint as a read-only field, so repeating it is
	// not an edit. Omitting it is not one either.
	if body.Endpoint == "" {
		body.Endpoint = stored.Endpoint
	}
	in, msg := body.config(apiKey)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if in.Endpoint != stored.Endpoint {
		writeError(w, http.StatusBadRequest,
			"an instance's endpoint cannot be changed: every item pinned to it carries a "+
				"UUID only this box minted, and re-pointing it would have the next refresh "+
				"overwrite those rows with whatever the new box holds under the same ids. "+
				"Add an instance for the other box instead.")
		return
	}

	existing, err := s.st.ListStashboxInstances(ctx)
	if err != nil {
		s.writeStoreError(w, "list stash-box instances", err)
		return
	}
	// The provider id is not re-derived from the new name: it is the instance's
	// identity, not one of its fields, so only the name is checked for a clash.
	if !s.stashboxInstanceFree(w, existing, in.Name, "", id) {
		return
	}

	if in.APIKey != "" && in.APIKey != stored.APIKey {
		testCtx, cancel := context.WithTimeout(ctx, credentialCheckTimeout)
		defer cancel()
		if err := s.mgr.ValidateAdultCredential(testCtx, in.Endpoint, in.APIKey); err != nil {
			// The endpoint's own message, for handleTestIndexer's reason: "it did
			// not work" without a reason cannot be acted on. Not logged, and it
			// never carries the key.
			writeCodedError(w, http.StatusBadGateway, CodeAdultCredentialInvalid,
				"stash-box test failed: "+err.Error())
			return
		}
	}

	in.ID = id
	in.ProviderID = stored.ProviderID
	if err := s.st.UpsertStashboxInstance(ctx, &in); err != nil {
		s.writeStoreError(w, "update stash-box instance", err)
		return
	}
	dto, err := s.stashboxInstanceDTO(ctx, in)
	if err != nil {
		s.writeStoreError(w, "count stash-box instance use", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// handleDeleteStashboxInstance removes an instance nothing depends on.
//
// Nothing cascades in the store, so the two counts are the whole guard: a
// library whose chain names this instance would walk to a provider that cannot
// be built, and an item pinned to it would lose the only box that can be asked
// about its refs. Both are recoverable (the rows stay) but neither is something
// to do silently, so the refusal names them and the user moves the items or
// edits the chain first.
func (s *server) handleDeleteStashboxInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	in, err := s.st.GetStashboxInstance(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get stash-box instance", err)
		return
	}
	// Asked before the use counts because it is the broader refusal: the last
	// instance is the module's only way to reach a provider at all, so removing
	// it would leave every adult surface answering 503 with no screen saying
	// why.
	//
	// The question is whether any adult library is switched ON, not whether one
	// exists: this route is absent to a caller who can see none, so in practice
	// the last instance outlives the last active library rather than being
	// deletable during the gap. A library switched off is why an install can
	// hold instances nothing reaches, which is the state that lets an owner
	// tidy them up.
	enabled, err := s.st.AnyActiveLibraryOfKind(ctx, core.LibraryKindAdult)
	if err != nil {
		s.writeStoreError(w, "read libraries", err)
		return
	}
	if enabled {
		instances, err := s.st.ListStashboxInstances(ctx)
		if err != nil {
			s.writeStoreError(w, "list stash-box instances", err)
			return
		}
		if len(instances) <= 1 {
			writeError(w, http.StatusConflict,
				"this is the only stash-box instance and an adult library is switched on; "+
					"add another instance or switch the library off first")
			return
		}
	}

	libraries, items, err := s.stashboxInstanceUse(ctx, in.ProviderID)
	if err != nil {
		s.writeStoreError(w, "count stash-box instance use", err)
		return
	}
	if libraries > 0 || items > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"%s is used by %d %s and %d %s; move them to another instance first",
			in.Name, libraries, plural(libraries, "library", "libraries"),
			items, plural(items, "item", "items")))
		return
	}

	if err := s.st.DeleteStashboxInstance(ctx, id); err != nil {
		s.writeStoreError(w, "delete stash-box instance", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestStashboxInstance asks the box whether it answers with the credential
// on file.
func (s *server) handleTestStashboxInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	in, err := s.st.GetStashboxInstance(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get stash-box instance", err)
		return
	}
	s.testStashboxCredential(w, r, in.Endpoint, in.APIKey)
}

// handleTestStashboxInstanceConfig probes an endpoint and key that are not
// stored yet, which is what the add form needs while the user is still typing,
// before the instance exists to have an id. The indexer categories endpoint is
// body-shaped for the same reason.
func (s *server) handleTestStashboxInstanceConfig(w http.ResponseWriter, r *http.Request) {
	var body stashboxInstanceRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	// The name only labels the row; the form may not have one yet, and a test is
	// about the endpoint and the key.
	if strings.TrimSpace(body.Name) == "" {
		body.Name = "stash-box"
	}
	apiKey := ""
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	in, msg := body.config(apiKey)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	s.testStashboxCredential(w, r, in.Endpoint, in.APIKey)
}

// testStashboxCredential is the one live call both test routes make. It goes
// through ValidateAdultCredential rather than the cached client on purpose. See
// the comment there: a candidate credential is exactly what the cache must not
// hold.
func (s *server) testStashboxCredential(w http.ResponseWriter, r *http.Request, endpoint, apiKey string) {
	ctx, cancel := context.WithTimeout(r.Context(), credentialCheckTimeout)
	defer cancel()
	if err := s.mgr.ValidateAdultCredential(ctx, endpoint, apiKey); err != nil {
		writeCodedError(w, http.StatusBadGateway, CodeAdultCredentialInvalid,
			"stash-box test failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// stashboxInstanceFree reports whether name and providerID are available,
// writing the 409 when either is not. Both columns are UNIQUE, so without this
// a duplicate (a plain user mistake, and two names that fold to one slug are an
// easy one) would surface as a 500. An empty providerID skips that half, which
// is what an update wants: the id is not derived from the new name.
func (s *server) stashboxInstanceFree(w http.ResponseWriter, existing []core.StashboxInstance, name, providerID string, exceptID int64) bool {
	for _, in := range existing {
		if in.ID == exceptID {
			continue
		}
		if in.Name == name {
			writeError(w, http.StatusConflict, "a stash-box instance named "+name+" already exists")
			return false
		}
		if providerID != "" && in.ProviderID == providerID {
			writeError(w, http.StatusConflict,
				"a stash-box instance with the id "+providerID+" already exists; give this one another name")
			return false
		}
	}
	return true
}

func (s *server) stashboxInstanceDTO(ctx context.Context, in core.StashboxInstance) (stashboxInstanceJSON, error) {
	libraries, items, err := s.stashboxInstanceUse(ctx, in.ProviderID)
	if err != nil {
		return stashboxInstanceJSON{}, err
	}
	provider := s.mgr.AdultMetadataFor(ctx, in.ProviderID)
	filters := sceneFiltersJSON{}
	if provider != nil {
		filters = sceneFiltersDTO(core.SceneFiltersOf(provider))
	}
	return stashboxInstanceJSON{
		ID:           in.ID,
		ProviderID:   in.ProviderID,
		Name:         in.Name,
		Endpoint:     in.Endpoint,
		HasAPIKey:    in.APIKey != "",
		SceneFilters: &filters,
		LibraryCount: libraries,
		ItemCount:    items,
	}, nil
}

func (s *server) stashboxInstanceUse(ctx context.Context, providerID string) (libraries, items int, err error) {
	libraries, err = s.st.CountLibrariesUsingProvider(ctx, providerID)
	if err != nil {
		return 0, 0, err
	}
	items, err = s.st.CountItemsPinnedToProvider(ctx, providerID)
	if err != nil {
		return 0, 0, err
	}
	return libraries, items, nil
}

// knownProviderInstance reports whether id names something the registry can
// resolve to a client: a bare compiled-in provider, or a stash-box instance
// that is actually configured.
//
// It is the check a chain element and an item ref both need. An id whose base
// is stash-box but which no row answers to is not a provider that is merely
// unconfigured. It is a name for a box this install has never held, so a chain
// containing it would walk to nothing and a ref pinned to it could never be
// refreshed.
func (s *server) knownProviderInstance(ctx context.Context, id string) (bool, error) {
	if core.ProviderBase(id) != core.ProviderStashbox {
		return true, nil
	}
	if _, err := s.st.GetStashboxInstanceByProviderID(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
