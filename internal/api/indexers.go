package api

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/indexer/catalog"
)

// indexerJSON is a configured search source (SPEC §5.1). Stored credentials
// are write-only; HasAPIKey tells the editor whether one is already present.
type indexerJSON struct {
	ID                 int64    `json:"id"`
	DefinitionID       string   `json:"definition_id"`
	DefinitionSource   string   `json:"definition_source"`
	DefinitionRevision string   `json:"definition_revision"`
	DefinitionDigest   string   `json:"definition_digest"`
	HasSettings        []string `json:"has_settings"`
	Name               string   `json:"name"`
	URL                string   `json:"url"`
	HasAPIKey          bool     `json:"has_api_key"`
	Type               string   `json:"type"`
	Categories         []int    `json:"categories"`
	Priority           int      `json:"priority"`
	Enabled            bool     `json:"enabled"`
	// HealthError is the last failed probe. Empty means the indexer last
	// answered. A non-empty value means search is skipping it.
	HealthError string `json:"health_error"`
	// ConsecutiveFailures is how many probes in a row have failed.
	ConsecutiveFailures int `json:"consecutive_failures"`
}

func indexerDTO(c core.IndexerConfig) indexerJSON {
	return indexerJSON{
		ID:                  c.ID,
		DefinitionID:        c.DefinitionID,
		DefinitionSource:    c.DefinitionSource,
		DefinitionRevision:  c.DefinitionRevision,
		DefinitionDigest:    c.DefinitionDigest,
		HasSettings:         configuredSettingNames(c.Settings),
		Name:                c.Name,
		URL:                 c.URL,
		HasAPIKey:           c.APIKey != "",
		Type:                c.Type,
		Categories:          categoryList(c.Categories),
		Priority:            c.Priority,
		Enabled:             c.Enabled,
		HealthError:         c.HealthError,
		ConsecutiveFailures: c.ConsecutiveFailures,
	}
}

// indexerRequest is the body of POST /indexers and PUT /indexers/{id}. PUT
// replaces the whole configuration apart from the write-only credential:
// omitted or null keeps the stored key, while an explicit empty string clears it.
type indexerRequest struct {
	DefinitionID       string             `json:"definition_id"`
	DefinitionSource   string             `json:"definition_source"`
	DefinitionRevision string             `json:"definition_revision"`
	DefinitionDigest   string             `json:"definition_digest"`
	Settings           *map[string]string `json:"settings"`
	Name               string             `json:"name"`
	URL                string             `json:"url"`
	APIKey             *string            `json:"api_key"`
	Type               string             `json:"type"`
	Categories         []int              `json:"categories"`
	Priority           *int               `json:"priority"`
	Enabled            *bool              `json:"enabled"`
}

// config validates the body and turns it into a store-ready configuration. The
// returned message is empty when the body is valid.
func (b indexerRequest) config(apiKey string) (core.IndexerConfig, string) {
	return b.configWithDefinitions(apiKey, nil)
}

func (b indexerRequest) configWithDefinitions(apiKey string, lookup LocalDefinitionLookup, exactLookups ...ExactLocalDefinitionLookup) (core.IndexerConfig, string) {
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
	if parsed.User != nil {
		return core.IndexerConfig{}, "url must not include credentials"
	}
	if b.Type != core.IndexerTypeTorznab && b.Type != core.IndexerTypeNewznab {
		return core.IndexerConfig{}, "type must be torznab or newznab"
	}
	definitionID := strings.TrimSpace(b.DefinitionID)
	source, revision, digest := strings.TrimSpace(b.DefinitionSource), strings.TrimSpace(b.DefinitionRevision), strings.TrimSpace(b.DefinitionDigest)
	definitionRef, definitionRefErr := cardigann.ParseDefinitionRef(definitionID)
	if definitionID != "" && definitionRefErr != nil {
		return core.IndexerConfig{}, "unknown local indexer definition"
	}
	exactCount := 0
	for _, value := range []string{source, revision, digest} {
		if value != "" {
			exactCount++
		}
	}
	var exact ExactLocalDefinitionLookup
	if len(exactLookups) > 0 {
		exact = exactLookups[0]
	}
	if definitionID != "" && b.Type != core.IndexerTypeTorznab {
		return core.IndexerConfig{}, "local definitions must use torznab"
	}
	requiresExactPin := definitionID != "" && definitionRef.Source != cardigann.BuiltinSource && definitionRef.Source != "user" && definitionRef.Source != cardigann.ManagedSource
	if requiresExactPin && exactCount != 3 {
		return core.IndexerConfig{}, "immutable external definitions require an exact source, revision, and digest"
	}
	if exactCount != 0 && (definitionID == "" || exactCount != 3) {
		return core.IndexerConfig{}, "exact definition pins require definition_id, source, revision, and digest"
	}
	if exactCount == 3 && definitionRef.Source != source {
		return core.IndexerConfig{}, "definition source does not match definition_id"
	}
	if exactCount == 3 && (exact == nil || !knownExactLocalDefinition(definitionID, source, revision, digest, exact)) {
		return core.IndexerConfig{}, "exact local indexer definition is not active"
	}
	if definitionID != "" && exactCount == 0 && !knownLocalDefinition(definitionID, lookup) {
		return core.IndexerConfig{}, "unknown local indexer definition"
	}
	settings, problem := validatedIndexerSettingsWithExact(definitionID, source, revision, digest, b.Settings, lookup, exact)
	if problem != "" {
		return core.IndexerConfig{}, problem
	}
	for _, cat := range b.Categories {
		if cat <= 0 {
			return core.IndexerConfig{}, "categories must be positive integers"
		}
	}
	priority := core.IndexerDefaultPriority
	if b.Priority != nil {
		if *b.Priority < 0 {
			return core.IndexerConfig{}, "priority must be zero or greater"
		}
		priority = *b.Priority
	}

	enabled := true
	if b.Enabled != nil {
		enabled = *b.Enabled
	}
	return core.IndexerConfig{
		DefinitionID:       definitionID,
		DefinitionSource:   source,
		DefinitionRevision: revision,
		DefinitionDigest:   digest,
		Settings:           settings,
		Name:               name,
		URL:                strings.TrimRight(raw, "/"),
		APIKey:             strings.TrimSpace(apiKey),
		Type:               b.Type,
		Categories:         b.Categories,
		Priority:           priority,
		Enabled:            enabled,
	}, ""
}

func knownLocalDefinitionID(id string) bool {
	return knownLocalDefinition(id, nil)
}

func knownLocalDefinition(id string, lookup LocalDefinitionLookup) bool {
	_, ok := localDefinitionSchema(id, lookup)
	return ok
}

func knownExactLocalDefinition(id, source, revision, digest string, lookup ExactLocalDefinitionLookup) bool {
	_, ok := lookup(id, source, revision, digest)
	return ok
}

func localDefinition(id string) (catalog.Definition, bool) {
	for _, definition := range catalog.All() {
		if definition.DefinitionID == id {
			return definition, true
		}
	}
	return catalog.Definition{}, false
}

const (
	maxIndexerSettingNameBytes  = 128
	maxIndexerSettingValueBytes = 16 * 1024
)

func localDefinitionSchema(id string, lookup LocalDefinitionLookup) (LocalDefinitionSchema, bool) {
	if lookup != nil {
		return lookup(id)
	}
	definition, ok := localDefinition(id)
	if !ok {
		return LocalDefinitionSchema{}, false
	}
	names := make([]string, 0, len(definition.Settings))
	for _, setting := range definition.Settings {
		names = append(names, setting.Name)
	}
	return LocalDefinitionSchema{Settings: names}, true
}

func validatedIndexerSettings(definitionID string, settings *map[string]string, lookups ...LocalDefinitionLookup) (map[string]string, string) {
	if settings == nil {
		return map[string]string{}, ""
	}
	allowed := map[string]bool{}
	if definitionID != "" {
		var lookup LocalDefinitionLookup
		if len(lookups) > 0 {
			lookup = lookups[0]
		}
		definition, ok := localDefinitionSchema(definitionID, lookup)
		if !ok {
			return nil, "unknown local indexer definition"
		}
		for _, setting := range definition.Settings {
			allowed[setting] = true
		}
	}
	return filteredIndexerSettings(*settings, allowed)
}

func filteredIndexerSettings(settings map[string]string, allowed map[string]bool) (map[string]string, string) {
	names := make([]string, 0, len(settings))
	for name := range settings {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make(map[string]string, len(settings))
	for _, rawName := range names {
		name, value := strings.TrimSpace(rawName), settings[rawName]
		if name == "" || len(name) > maxIndexerSettingNameBytes || !allowed[name] {
			return nil, "unknown local indexer setting"
		}
		if _, duplicate := out[name]; duplicate {
			return nil, "duplicate local indexer setting"
		}
		if len(value) > maxIndexerSettingValueBytes {
			return nil, "local indexer setting is too large"
		}
		out[name] = value
	}
	return out, ""
}

func validatedIndexerSettingsWithExact(definitionID, source, revision, digest string, settings *map[string]string, lookup LocalDefinitionLookup, exact ExactLocalDefinitionLookup) (map[string]string, string) {
	if source == "" && revision == "" && digest == "" {
		return validatedIndexerSettings(definitionID, settings, lookup)
	}
	if settings == nil {
		return map[string]string{}, ""
	}
	if exact == nil {
		return nil, "exact local indexer definition is not active"
	}
	schema, ok := exact(definitionID, source, revision, digest)
	if !ok {
		return nil, "exact local indexer definition is not active"
	}
	allowed := make(map[string]bool, len(schema.Settings))
	for _, setting := range schema.Settings {
		allowed[setting] = true
	}
	return filteredIndexerSettings(*settings, allowed)
}

func configuredSettingNames(settings map[string]string) []string {
	out := make([]string, 0, len(settings))
	for name, value := range settings {
		if strings.TrimSpace(name) != "" && value != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// handleIndexerCatalog serves the add-indexer directory. kind selects torrent,
// usenet, or generic; q filters that list. An omitted kind returns everything.
func (s *server) handleIndexerCatalog(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	q := r.URL.Query().Get("q")

	var defs []catalog.Definition
	switch kind {
	case "":
		if strings.TrimSpace(q) == "" {
			defs = catalog.All()
		} else {
			defs = append(defs, catalog.Search(catalog.KindGeneric, q)...)
			defs = append(defs, catalog.Search(catalog.KindUsenet, q)...)
			defs = append(defs, catalog.Search(catalog.KindTorrent, q)...)
		}
	case catalog.KindTorrent, catalog.KindUsenet, catalog.KindGeneric:
		defs = catalog.Search(kind, q)
	default:
		writeError(w, http.StatusBadRequest, "kind must be torrent, usenet, or generic")
		return
	}
	if defs == nil {
		defs = []catalog.Definition{}
	}
	if s.localDefinitions != nil {
		available := defs[:0]
		for _, definition := range defs {
			if definition.DefinitionID == "" {
				available = append(available, definition)
				continue
			}
			if _, ok := s.localDefinitions(definition.DefinitionID); ok {
				available = append(available, definition)
			}
		}
		defs = available
	}
	inventory, err := catalog.Inventory(s.catalogExecutionStatuses(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load indexer inventory")
		return
	}
	inventory = filterIndexerInventory(inventory, kind, q)
	writeJSON(w, http.StatusOK, map[string]any{"definitions": defs, "inventory": inventory})
}

func (s *server) catalogExecutionStatuses(ctx context.Context) []catalog.ExecutionStatus {
	statuses := make([]catalog.ExecutionStatus, 0)
	if s.localDefinitions != nil {
		for _, definition := range catalog.All() {
			if definition.Kind != catalog.KindTorrent || definition.DefinitionID == "" {
				continue
			}
			if _, ok := s.localDefinitions(definition.DefinitionID); !ok {
				continue
			}
			definitionID := definition.DefinitionID
			source := "builtin"
			if strings.Contains(definitionID, ":") {
				source, _, _ = strings.Cut(definitionID, ":")
			} else {
				definitionID = "builtin:" + definitionID
			}
			metadataID := definition.MetadataID
			if metadataID == "" {
				metadataID = definition.ID
			}
			if !catalog.HasMetadataID(metadataID) {
				continue
			}
			statuses = append(statuses, catalog.ExecutionStatus{MetadataID: metadataID, DefinitionID: definitionID, State: catalog.InventoryStateVerified, Source: source, Unsupported: []string{}, BaseURLs: append([]string(nil), definition.URLs...), Addable: true})
		}
	}
	statuses = append(statuses, s.definitionInventoryStatuses...)
	return append(statuses, s.packCatalogExecutionStatuses(ctx)...)
}

func filterIndexerInventory(inventory []catalog.InventoryEntry, kind, query string) []catalog.InventoryEntry {
	if kind == catalog.KindUsenet || kind == catalog.KindGeneric {
		return []catalog.InventoryEntry{}
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return inventory
	}
	out := make([]catalog.InventoryEntry, 0)
	for _, entry := range inventory {
		if strings.Contains(strings.ToLower(entry.ID), query) ||
			strings.Contains(strings.ToLower(entry.Name), query) ||
			strings.Contains(strings.ToLower(entry.Description), query) {
			out = append(out, entry)
		}
	}
	return out
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

// handleIndexerFeed exposes one stored local definition as a Torznab feed.
// It is auth-exempt at the middleware layer only so Torznab clients may use
// their standard ?apikey= parameter; authentication is scoped to this handler.
func (s *server) handleIndexerFeed(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeIndexerFeed(w, r) {
		return
	}
	if s.indexers == nil {
		writeError(w, http.StatusServiceUnavailable, "indexer client is not configured")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cfg, err := s.st.GetIndexer(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get indexer feed", err)
		return
	}
	if cfg.DefinitionID == "" {
		writeError(w, http.StatusNotFound, "indexer does not have a local feed")
		return
	}
	cardigann.NewClientTorznabHandler(cfg.Name, s.indexers(*cfg)).ServeHTTP(w, r)
}

func (s *server) authorizeIndexerFeed(w http.ResponseWriter, r *http.Request) bool {
	user, ok, err := s.resolveUser(r)
	if err != nil {
		s.writeStoreError(w, "resolve indexer feed caller", err)
		return false
	}
	if !ok {
		ok, err = s.apiKeyMatches(r, r.URL.Query().Get("apikey"))
		if err != nil {
			s.writeStoreError(w, "authenticate indexer feed", err)
			return false
		}
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if user.Role != "" && user.Role != core.RoleAdmin {
		writeError(w, http.StatusForbidden, "admins only")
		return false
	}
	return true
}

func (s *server) handleCreateIndexer(w http.ResponseWriter, r *http.Request) {
	var body indexerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	apiKey := ""
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	cfg, msg := body.configWithDefinitions(apiKey, s.localDefinitions, s.exactLocalDefinitions)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()
	if !s.persistedExactDefinitionPin(ctx, cfg) {
		writeError(w, http.StatusBadRequest, "exact local indexer definition is not active")
		return
	}

	if !s.indexerNameFree(ctx, w, cfg.Name, 0) {
		return
	}
	if cfg.Enabled && !s.proveIndexer(ctx, w, cfg) {
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
	ctx := r.Context()

	// Read first so updating an indexer that never existed is a 404 rather than
	// a silent insert, and so an omitted credential can preserve the stored one.
	stored, err := s.st.GetIndexer(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get indexer", err)
		return
	}
	definitionChanged := strings.TrimSpace(body.DefinitionID) != strings.TrimSpace(stored.DefinitionID) || strings.TrimSpace(body.DefinitionSource) != stored.DefinitionSource || strings.TrimSpace(body.DefinitionRevision) != stored.DefinitionRevision || strings.TrimSpace(body.DefinitionDigest) != stored.DefinitionDigest
	apiKey := stored.APIKey
	if definitionChanged {
		apiKey = ""
	}
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	cfg, msg := body.configWithDefinitions(apiKey, s.localDefinitions, s.exactLocalDefinitions)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if !s.persistedExactDefinitionPin(ctx, cfg) {
		writeError(w, http.StatusBadRequest, "exact local indexer definition is not active")
		return
	}

	if !s.indexerNameFree(ctx, w, cfg.Name, id) {
		return
	}
	cfg.ID = id
	if !definitionChanged {
		if body.Settings == nil {
			cfg.Settings = stored.Settings
		} else {
			// Stored setting values are write-only, so the edit form can
			// only resend values the user retyped. Keep every stored
			// setting the request does not mention.
			merged := make(map[string]string, len(stored.Settings)+len(cfg.Settings))
			for name, value := range stored.Settings {
				merged[name] = value
			}
			for name, value := range cfg.Settings {
				merged[name] = value
			}
			cfg.Settings = merged
		}
	}
	if cfg.Enabled {
		if !s.proveIndexer(ctx, w, cfg) {
			return
		}
		cfg.HealthError = ""
		cfg.ConsecutiveFailures = 0
	} else {
		cfg.HealthError = stored.HealthError
		cfg.ConsecutiveFailures = stored.ConsecutiveFailures
		cfg.LastHealthAt = stored.LastHealthAt
	}
	if err := s.st.UpsertIndexer(ctx, &cfg); err != nil {
		s.writeStoreError(w, "update indexer", err)
		return
	}
	writeJSON(w, http.StatusOK, indexerDTO(cfg))
}

// persistedExactDefinitionPin closes the runtime/store seam before a configured
// indexer is written. Managed definitions are admitted only by the exact
// verified runtime cache. Other immutable sources additionally require their
// signed-pack receipt and active lifecycle row. Legacy builtin and owner-local
// user definitions have no exact pin and are unchanged.
func (s *server) persistedExactDefinitionPin(ctx context.Context, cfg core.IndexerConfig) bool {
	if cfg.DefinitionSource == "" && cfg.DefinitionRevision == "" && cfg.DefinitionDigest == "" {
		return true
	}

	revision, err := s.st.GetDefinitionPackRevision(ctx, cfg.DefinitionSource, cfg.DefinitionRevision)
	if err != nil || !revision.Active || revision.InstallState != core.DefinitionPackInstalled {
		return false
	}
	entries, err := s.st.ListDefinitionPackEntries(ctx, cfg.DefinitionSource, cfg.DefinitionRevision)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.DefinitionRef == cfg.DefinitionID && entry.Digest == cfg.DefinitionDigest && entry.State == core.DefinitionPackEntryRunnableUnverified {
			return true
		}
	}
	return false
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
		_ = s.st.RecordIndexerHealth(r.Context(), cfg.ID, err)
		writeError(w, http.StatusBadGateway, indexerProbeError(*cfg, err))
		return
	}
	_ = s.st.RecordIndexerHealth(r.Context(), cfg.ID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStoredIndexerCategories fetches the category tree an already stored
// indexer advertises, including its write-only stored credentials.
func (s *server) handleStoredIndexerCategories(w http.ResponseWriter, r *http.Request) {
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
	s.writeIndexerCategories(w, r.Context(), newClient(*cfg))
}

// handleIndexerCategories fetches the category tree an indexer advertises.
// The configuration comes from the body rather than a stored row because the
// settings form needs the tree while the user is still typing — before the
// indexer exists to have an id.
func (s *server) handleIndexerCategories(w http.ResponseWriter, r *http.Request) {
	newClient, ok := s.requireIndexerClients(w)
	if !ok {
		return
	}

	var body indexerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	// The name only labels error messages here; the form may not have one yet.
	if strings.TrimSpace(body.Name) == "" {
		body.Name = "indexer"
	}
	apiKey := ""
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	cfg, problem := body.configWithDefinitions(apiKey, s.localDefinitions, s.exactLocalDefinitions)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	s.writeIndexerCategories(w, r.Context(), newClient(cfg))
}

func (s *server) writeIndexerCategories(w http.ResponseWriter, ctx context.Context, client IndexerClient) {
	categories, err := client.Categories(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch categories failed: "+err.Error())
		return
	}
	if categories == nil {
		categories = []core.IndexerCategory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
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

const indexerHealthTimeout = 10 * time.Second

// proveIndexer talks to the indexer before a create or an enable. A site
// homepage is not a Torznab feed; failing here is how that stays out of
// search instead of stalling every fan-out.
func (s *server) proveIndexer(ctx context.Context, w http.ResponseWriter, cfg core.IndexerConfig) bool {
	newClient, ok := s.requireIndexerClients(w)
	if !ok {
		return false
	}
	probe, cancel := context.WithTimeout(ctx, indexerHealthTimeout)
	defer cancel()
	if err := newClient(cfg).Test(probe); err != nil {
		writeError(w, http.StatusBadGateway, indexerProbeError(cfg, err))
		return false
	}
	return true
}

// handleTestIndexerConfig probes a configuration from the body rather than a
// stored row, so the add form can fail before anything is written.
func (s *server) handleTestIndexerConfig(w http.ResponseWriter, r *http.Request) {
	newClient, ok := s.requireIndexerClients(w)
	if !ok {
		return
	}
	var body indexerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		body.Name = "indexer"
	}
	apiKey := ""
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	cfg, problem := body.configWithDefinitions(apiKey, s.localDefinitions, s.exactLocalDefinitions)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	probe, cancel := context.WithTimeout(r.Context(), indexerHealthTimeout)
	defer cancel()
	if err := newClient(cfg).Test(probe); err != nil {
		writeError(w, http.StatusBadGateway, indexerProbeError(cfg, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func indexerProbeError(cfg core.IndexerConfig, err error) string {
	msg := redactIndexerMessage(cfg, err.Error())
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "forbidden") || strings.Contains(lower, "http 403") {
		// A local adapter scrapes the site itself, so a 403 there is the
		// tracker refusing the request — feed guidance would be wrong.
		if cfg.DefinitionID != "" {
			return "indexer test failed: " + msg + ". The site refused the request, likely with anti-bot protection (such as a Cloudflare challenge) that Caravan cannot pass."
		}
		return "indexer test failed: the configured URL is a website, not a Torznab or Newznab feed. Paste a Jackett or Prowlarr URL."
	}
	return "indexer test failed: " + msg
}

func redactIndexerMessage(cfg core.IndexerConfig, message string) string {
	return cfg.RedactSecrets(message)
}
