package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/convert"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

// SettingMode records the deployment mode (SPEC §2) so GET /system/status can
// report it. The serving process writes it at startup from the bootstrap
// config; an unset value reports ModeServer.
const SettingMode = "mode"

// credentialSetSuffix turns a credential's settings key into the public "a key
// is stored" flag beside it. The key itself is write-only (SPEC §12), and the
// settings screen still has to render a card that says whether one is on file.
const credentialSetSuffix = "_set"

// settingTMDBAPIKeySet is that flag as it has always been spelled on the wire.
// publicSettings derives it from the registry now; this names the exact string
// every existing consumer reads, and derives it the same way so the two cannot
// drift apart.
const settingTMDBAPIKeySet = store.SettingTMDBAPIKey + credentialSetSuffix

// Deployment modes reported by GET /system/status.
const (
	ModeServer   = "server"
	ModePortable = "portable"
)

// writableSettings is the allowlist PUT /settings accepts. Settings are a
// key-value table, so without an allowlist a buggy client could quietly fill
// it with keys nothing reads.
//
// store.SettingStorageRoot is deliberately absent. It is the one setting with
// rules attached — it must be absolute, it must name a folder that exists, and
// it must not change while a migration owns both roots — and a generic
// key-value PUT enforces none of them. POST /system/storage-root/repoint is the
// only way in (SPEC §10); see internal/api/storage.go.
var writableSettings = map[string]bool{
	store.SettingTMDBAPIKey:             true,
	store.SettingTheTVDBAPIKey:          true,
	store.SettingTheTVDBPIN:             true,
	store.SettingRSSSyncIntervalMinutes: true,
	store.SettingBacklogIntervalMinutes: true,
	store.SettingRefreshIntervalMinutes: true,
	store.SettingEngineListenPort:       true,
	store.SettingEngineMaxConnections:   true,
	store.SettingEngineMaxDownKBps:      true,
	store.SettingEngineMaxUpKBps:        true,
	store.SettingEngineSeedRatio:        true,
	store.SettingEngineSeedDays:         true,

	store.SettingMaxConcurrentDownloads:       true,
	store.SettingEmbeddedTorrentMaxConcurrent: true,
	store.SettingEmbeddedUsenetMaxConcurrent:  true,
	store.SettingRouteTorrent:                 true,
	store.SettingRouteUsenet:                  true,
	store.SettingTVProfile:                    true,
	store.SettingConvertVideoPreset:           true,
	store.SettingConvertVideoCRF:              true,
	store.SettingConvertAudioBitrateKbps:      true,
	store.SettingDLNAEnabled:                  true,
	store.SettingDLNAFriendlyName:             true,
	store.SettingRecycleRetentionDays:         true,
	store.SettingMovieFolderFormat:            true,
	store.SettingMovieFileFormat:              true,
	store.SettingSeriesFolderFormat:           true,
	store.SettingSeasonFolderFormat:           true,
	store.SettingEpisodeFileFormat:            true,
}

// trimmedSettings are written with their surrounding whitespace removed.
//
// They are the credentials, and the reason is that everything that JUDGES them
// trims: settingValue, the metadata test, and the cached verdict's key are all
// about the trimmed string, while the clients built in cmd/caravan send the
// stored one verbatim. Storing " abc " therefore cached a verdict for "abc" and
// then sent "+abc+" upstream forever — a credential reported healthy while
// nothing worked. Trimming on the way in makes the stored string, the validated
// string and the sent string the same value.
var trimmedSettings = map[string]bool{
	store.SettingTMDBAPIKey:              true,
	store.SettingTheTVDBAPIKey:           true,
	store.SettingTheTVDBPIN:              true,
	store.SettingConvertVideoPreset:      true,
	store.SettingConvertVideoCRF:         true,
	store.SettingConvertAudioBitrateKbps: true,
}

// engineSettingsApplier is implemented by providers that can apply the live
// subset of engine settings after the settings row has been committed.
type engineSettingsApplier interface {
	ApplyEngineSettings(context.Context, map[string]string) error
}

// publicSettingKeys is the complete projection of the settings table that may
// appear in GET and PUT /settings responses. Credentials are write-only: a
// browser response is not an acceptable place for a secret, regardless of
// which settings screen wrote it.
//
// New persisted settings are private until they are deliberately added here.
// That makes the secure behaviour the default and avoids a future credential
// accidentally becoming readable because AllSettings grew a new key.
var publicSettingKeys = map[string]bool{
	store.SettingStorageRoot:                  true,
	store.SettingRSSSyncIntervalMinutes:       true,
	store.SettingBacklogIntervalMinutes:       true,
	store.SettingRefreshIntervalMinutes:       true,
	store.SettingEngineListenPort:             true,
	store.SettingEngineMaxConnections:         true,
	store.SettingEngineMaxDownKBps:            true,
	store.SettingEngineMaxUpKBps:              true,
	store.SettingEngineSeedRatio:              true,
	store.SettingEngineSeedDays:               true,
	store.SettingMaxConcurrentDownloads:       true,
	store.SettingEmbeddedTorrentMaxConcurrent: true,
	store.SettingEmbeddedUsenetMaxConcurrent:  true,
	store.SettingRouteTorrent:                 true,
	store.SettingRouteUsenet:                  true,
	store.SettingTVProfile:                    true,
	store.SettingConvertVideoPreset:           true,
	store.SettingConvertVideoCRF:              true,
	store.SettingConvertAudioBitrateKbps:      true,
	store.SettingJellyfinURL:                  true,
	store.SettingJellyfinEnabled:              true,
	store.SettingDLNAEnabled:                  true,
	store.SettingDLNAFriendlyName:             true,
	store.SettingDLNAUUID:                     true,
	store.SettingDLNAUpdateID:                 true,
	store.SettingRecycleRetentionDays:         true,
	store.SettingMovieFolderFormat:            true,
	store.SettingMovieFileFormat:              true,
	store.SettingSeriesFolderFormat:           true,
	store.SettingSeasonFolderFormat:           true,
	store.SettingEpisodeFileFormat:            true,
}

// adultOnlySettings are public settings readable only by a caller the adult
// module is visible to. They are the Stash handoff's non-secret settings and
// the module switch (PLAN phase 11).
//
// The module's promise is to be *absent* when it is off, not merely disabled
// (see requireAdult), and a settings object carrying a stash_url is a module
// announcing itself. Their own endpoints already sit on the adult mux; this is
// the same door on the one other path from the settings table to a response
// body.
var adultOnlySettings = map[string]bool{
	store.SettingAdultEnabled: true,
	store.SettingStashURL:     true,
	store.SettingStashEnabled: true,
}

// publicSettings is the only path from the settings table to a response body.
func (s *server) publicSettings(r *http.Request) (map[string]string, error) {
	stored, err := s.st.AllSettings(r.Context())
	if err != nil {
		return nil, err
	}

	settings := make(map[string]string, len(publicSettingKeys)+1)
	for key := range publicSettingKeys {
		if value, ok := stored[key]; ok {
			settings[key] = value
		}
	}
	// One flag per credentialed provider, from the registry rather than from a
	// list written out here: a provider added later gets its "a key is stored"
	// flag without an edit to this file, which is the same reason the status map
	// and the revalidation below are loops.
	for _, p := range core.Providers() {
		if p.CredentialSetting == "" {
			continue
		}
		settings[p.CredentialSetting+credentialSetSuffix] =
			strconv.FormatBool(stored[p.CredentialSetting] != "")
	}

	visible, err := s.adultVisible(r)
	if err != nil {
		return nil, err
	}
	if visible {
		for key := range adultOnlySettings {
			if value, ok := stored[key]; ok {
				settings[key] = value
			}
		}
	}
	return settings, nil
}

// handleGetSettings returns the public settings projection.
func (s *server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.publicSettings(r)
	if err != nil {
		s.writeStoreError(w, "read settings", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// handlePutSettings upserts the supplied keys and returns the resulting
// settings. It is a partial update: keys absent from the body keep their
// current value.
func (s *server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if !decodeJSON(w, r, &body) {
		return
	}

	unknown := []string{}
	for key := range body {
		if !writableSettings[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		writeError(w, http.StatusBadRequest, "unknown setting: "+unknown[0])
		return
	}

	if err := validateEngineSettings(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := convert.ResolveEncodingSettings(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateTVProfileSetting(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateDLNASettings(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateRouteSettings(r.Context(), body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRecycleRetention(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := library.ValidateNamingSettings(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Sorted so a partial failure is at least deterministic.
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := body[key]
		if trimmedSettings[key] {
			value = strings.TrimSpace(value)
		}
		if err := s.st.SetSetting(r.Context(), key, value); err != nil {
			s.writeStoreError(w, "write settings", err)
			return
		}
	}

	// TheTVDB's credential is a PAIR: the login consumes the key and, when the
	// subscription is user-supported, the PIN beside it. The loop below can only
	// see the key, because ProviderDescriptor.CredentialSetting names one
	// settings row — so a PIN edit changes what a login sends while leaving that
	// loop nothing to notice, and the cached verdict, which is filed under the
	// key string, survives an edit it is no longer true of.
	//
	// Dropping it first is what makes the recheck a recheck. When the key came in
	// this body too, the loop below then proves the new pair; when it did not,
	// the stored key is what the new PIN will travel with, so it is proved here.
	// A settings read that fails leaves the state optimistic rather than failing
	// a save that has already landed, exactly as revalidateMetadataKey does with
	// an unreachable provider.
	if _, ok := body[store.SettingTheTVDBPIN]; ok {
		s.credentials.forget(core.ProviderTheTVDB)
		if _, keyEdited := body[store.SettingTheTVDBAPIKey]; !keyEdited {
			if key, err := s.settingValue(r.Context(), store.SettingTheTVDBAPIKey); err == nil {
				s.revalidateMetadataKey(r.Context(), core.ProviderTheTVDB, key)
			}
		}
	}

	// A key edit is the first of the two credential-state transitions (PLAN
	// phase 10 task 2). It runs after the write, costs at most one upstream
	// call per edited credential, and is skipped entirely for a key the Test
	// button already proved — see revalidateMetadataKey. Only the keys this body
	// actually carried are checked, so saving an unrelated setting stays free.
	for _, p := range core.Providers() {
		if p.CredentialSetting == "" {
			continue
		}
		if key, ok := body[p.CredentialSetting]; ok {
			s.revalidateMetadataKey(r.Context(), p.ID, strings.TrimSpace(key))
		}
	}

	settings, err := s.publicSettings(r)
	if err != nil {
		s.writeStoreError(w, "read settings", err)
		return
	}
	if applier, ok := s.engine.(engineSettingsApplier); ok {
		if err := applier.ApplyEngineSettings(r.Context(), settings); err != nil {
			s.writeEngineError(w, "apply engine settings", err)
			return
		}
	}
	// The media server re-reads its own keys so the toggle takes effect without
	// a restart. It cannot fail the request: a LAN that will not carry SSDP is
	// reported through GET /dlna, not by rejecting a settings save that already
	// landed.
	if s.dlna != nil {
		s.dlna.Reload(r.Context())
	}
	writeJSON(w, http.StatusOK, settings)
}

// adultEnabledRequest is the body of POST /settings/adult.
type adultEnabledRequest struct {
	// Enabled is a pointer so an absent field is a client bug rather than a
	// silent switch-off, the way monitorRequest treats Monitored. Getting this
	// wrong would be a module that turns itself off on a malformed save.
	Enabled *bool `json:"enabled"`

	// Instance is the stash-box endpoint the enable is made with: the module's
	// FIRST one, in the shape POST /adult/stashbox-instances takes. It travels
	// in this request rather than in a separate call so the whole enable is one
	// decision the server can refuse as a unit — an endpoint that does not
	// answer leaves no instance row and the module off.
	//
	// It is optional, and its absence is not "use what is stored" but "there is
	// already something to use". A module switched off and back on again has its
	// instances still there (nothing deletes them), and re-proving a credential
	// nobody edited would make a switch-on fail because a box is having a bad
	// afternoon. An absent instance with an EMPTY table is the genuine gap, and
	// that is the one refusal below.
	Instance *stashboxInstanceRequest `json:"instance"`
}

// handleSetAdultEnabled flips the server-wide adult switch.
//
// It is a route of its own rather than a key in writableSettings because
// enabling has a consequence a key-value PUT cannot carry out: the first enable
// creates the Adult library row (store.SetAdultEnabled). storage_root is absent
// from that allowlist for the same reason — a setting with rules attached needs
// a door that knows them.
//
// Admin-only, and it must stay that way: memberAllowed does not name it, so a
// member is refused by requireAuth before this runs. It is also the one
// adult-shaped route that cannot sit behind requireAdult, because the gate
// answers 404 while the module is off and turning it on is precisely what this
// is for.
//
// Disabling deletes nothing (see store.SetAdultEnabled): the library row, the
// sites, the scenes and the files all stay, and turning it back on finds them
// as they were.
// Enabling is gated on the module having a stash-box endpoint it can reach.
// The whole request is refused before a single row is written when the body's
// endpoint is rejected, so a failed enable is indistinguishable from one that
// never happened. Disabling never validates: switching a module off must work
// when the credential behind it has expired, which is one of the reasons a
// person switches it off.
//
// The gate is instance-shaped since 0026. An enable that CARRIES an instance is
// the first-run flow — prove the endpoint, create the row, switch on — and an
// enable that carries none is a re-enable, which makes zero upstream calls: the
// instances survived the switch-off, nobody edited them, and failing a switch-on
// because a box is having a bad afternoon would be a refusal about nothing the
// user just did. Only an empty table with no instance in the body is the
// genuine gap.
func (s *server) handleSetAdultEnabled(w http.ResponseWriter, r *http.Request) {
	var body adultEnabledRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}

	if !*body.Enabled {
		// An instance in a disable body is ignored rather than rejected: the
		// settings screen posts the card it is looking at, and a stale field on
		// a switch-off is not a reason to fail.
		if err := s.st.SetAdultEnabled(r.Context(), false); err != nil {
			s.writeStoreError(w, "set adult enabled", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}

	ctx := r.Context()
	if body.Instance != nil {
		if !s.enableWithNewInstance(ctx, w, *body.Instance) {
			return
		}
	} else {
		instances, err := s.st.ListStashboxInstances(ctx)
		if err != nil {
			s.writeStoreError(w, "list stash-box instances", err)
			return
		}
		if len(instances) == 0 {
			writeCodedError(w, http.StatusBadRequest, CodeAdultCredentialAbsent,
				"a stash-box endpoint is required to enable adult content")
			return
		}
	}

	// Ordered so no interruption can leave the module on without the endpoint
	// that was proved for it. SetAdultEnabled is last, and it is the only write
	// here that anything reads as "the module is on".
	if err := s.st.SetAdultEnabled(ctx, true); err != nil {
		s.writeStoreError(w, "set adult enabled", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true})
}

// enableWithNewInstance proves the body's endpoint and creates the instance,
// writing its own refusals. It reports whether the caller may go on to switch
// the module on.
//
// Proved BEFORE the row is written, which is the ordering the whole enable
// hangs off: a rejected credential must leave no instance row behind, or the
// next screen shows an endpoint that has never worked as though it were
// configured. The minting itself is the create handler's own
// (mintStashboxInstance), so the first instance on a fresh install takes the
// same bare id an upgraded install is carried into.
func (s *server) enableWithNewInstance(ctx context.Context, w http.ResponseWriter,
	body stashboxInstanceRequest,
) bool {
	apiKey := ""
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	in, msg := body.config(apiKey)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return false
	}

	testCtx, cancel := context.WithTimeout(ctx, credentialCheckTimeout)
	defer cancel()
	if err := s.mgr.ValidateAdultCredential(testCtx, in.Endpoint, in.APIKey); err != nil {
		// The endpoint's own message, for the same reason the indexer test
		// returns one: "it did not work" without a reason cannot be acted on.
		// It is not logged — see handleTestIndexer.
		writeCodedError(w, http.StatusBadGateway, CodeAdultCredentialInvalid,
			"stash-box test failed: "+err.Error())
		return false
	}

	_, ok := s.mintStashboxInstance(ctx, w, body)
	return ok
}

// validateDLNASettings refuses values the media server would silently reinterpret.
//
// An unparseable dlna_enabled reads as off, and a friendly name that is only
// whitespace falls back to the default — both are quiet surprises, so they are
// rejected here where the user can see them (SPEC §13).
func validateDLNASettings(settings map[string]string) error {
	if raw, ok := settings[store.SettingDLNAEnabled]; ok {
		if _, err := strconv.ParseBool(strings.TrimSpace(raw)); err != nil {
			return fmt.Errorf("invalid %s", store.SettingDLNAEnabled)
		}
	}
	if raw, ok := settings[store.SettingDLNAFriendlyName]; ok {
		name := strings.TrimSpace(raw)
		if name == "" {
			return fmt.Errorf("invalid %s", store.SettingDLNAFriendlyName)
		}
		// The name is carried in the device description and rendered on a TV's
		// device list; anything longer is truncated there and unreadable.
		if len([]rune(name)) > 64 {
			return fmt.Errorf("invalid %s", store.SettingDLNAFriendlyName)
		}
	}
	return nil
}

// validateRouteSettings refuses a per-protocol default that would not route.
//
// The router resolves these ids at grab time and falls back to "nothing
// configured" for one it cannot use, so an id that is gone, disabled, or of
// the wrong protocol would otherwise be accepted here and silently reject
// every grab later. Pointing the torrent default at SABnzbd is the mistake
// worth catching: it looks configured and downloads nothing.
func (s *server) validateRouteSettings(ctx context.Context, settings map[string]string) error {
	for _, route := range []struct {
		key      string
		protocol string
	}{
		{store.SettingRouteTorrent, core.ProtocolTorrent},
		{store.SettingRouteUsenet, core.ProtocolUsenet},
	} {
		raw, ok := settings[route.key]
		if !ok {
			continue
		}
		value := strings.TrimSpace(raw)
		// Empty is "no default": legal everywhere, and the only value usenet
		// has before a client exists.
		if value == "" {
			continue
		}
		if value == store.RouteEmbedded {
			if route.protocol != core.ProtocolTorrent {
				return fmt.Errorf("invalid %s: the embedded engine only handles torrents", route.key)
			}
			continue
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid %s", route.key)
		}
		cfg, err := s.st.GetDownloadClient(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("invalid %s: no download client with id %d", route.key, id)
		}
		if err != nil {
			return fmt.Errorf("invalid %s", route.key)
		}
		t, ok := clients.Lookup(cfg.Type)
		if !ok || t.Protocol != route.protocol {
			return fmt.Errorf("invalid %s: %s does not handle %s releases", route.key, cfg.Name, route.protocol)
		}
	}
	return nil
}

// validateTVProfileSetting refuses a profile id nothing implements. The
// resolver falls back to the safe default at read time, so an unknown id would
// otherwise be stored and silently ignored — the opposite of SPEC §13.
func validateTVProfileSetting(settings map[string]string) error {
	id, ok := settings[store.SettingTVProfile]
	if !ok {
		return nil
	}
	id = strings.TrimSpace(id)
	for _, p := range core.TVProfiles() {
		if p.ID == id {
			return nil
		}
	}
	return fmt.Errorf("invalid %s", store.SettingTVProfile)
}

func validateEngineSettings(settings map[string]string) error {
	for key, value := range settings {
		switch key {
		case store.SettingEngineListenPort:
			port, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || port < 0 || port > 65535 {
				return fmt.Errorf("invalid %s", key)
			}
		case store.SettingEngineMaxConnections, store.SettingEngineSeedDays,
			store.SettingMaxConcurrentDownloads,
			store.SettingEmbeddedTorrentMaxConcurrent,
			store.SettingEmbeddedUsenetMaxConcurrent:
			// Every cap is a count, and zero is unlimited. A negative one would
			// be a ceiling nothing could ever be under.
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return fmt.Errorf("invalid %s", key)
			}
		case store.SettingEngineMaxDownKBps, store.SettingEngineMaxUpKBps:
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil || n < 0 {
				return fmt.Errorf("invalid %s", key)
			}
		case store.SettingEngineSeedRatio:
			ratio, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || ratio < 0 {

				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	return nil
}
func validateRecycleRetention(settings map[string]string) error {
	value, ok := settings[store.SettingRecycleRetentionDays]
	if !ok {
		return nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 || days > 3650 {
		return errors.New("recycle_retention_days must be an integer between 0 and 3650")
	}
	return nil
}

// statusResponse is the payload of GET /system/status.
type statusResponse struct {
	Version       string       `json:"version"`
	Mode          string       `json:"mode"`
	StorageRoot   string       `json:"storage_root"`
	SchemaVersion int          `json:"schema_version"`
	Scanning      bool         `json:"scanning"`
	Counts        statusCounts `json:"counts"`
	// DiskFreeBytes and DiskTotalBytes describe the filesystem holding the
	// storage root. Both zero when no root is set or the filesystem cannot be
	// asked — the UI renders that as "unknown", never as "full".
	DiskFreeBytes  int64 `json:"disk_free_bytes"`
	DiskTotalBytes int64 `json:"disk_total_bytes"`
	// EngineHealth is the download engine's state: "ok", "unconfigured" (no
	// storage root yet, so no engine), or "error" (it failed to start).
	EngineHealth string `json:"engine_health"`
	// MetadataCredential is the TMDB key's health: "absent", "invalid" or "ok"
	// (PLAN phase 10 task 2). It is read from the cached verdict in
	// credentials.go, so polling this endpoint costs no TMDB traffic however
	// often the UI asks.
	MetadataCredential string `json:"metadata_credential"`
	// MetadataCredentialReason is why the credential is invalid, in the
	// provider's own words. Empty for every other state, and never contains the
	// key (SPEC §12).
	MetadataCredentialReason string `json:"metadata_credential_reason,omitempty"`
	// MetadataCredentialCheckedAt is when the cached verdict was reached, empty
	// when the key has never been checked. It is what lets the settings screen
	// say "tested 2 minutes ago" rather than implying a check just ran.
	MetadataCredentialCheckedAt string `json:"metadata_credential_checked_at,omitempty"`
	// MetadataCredentials is every credentialed provider's health, keyed by
	// provider id, since "the metadata provider" stopped being singular.
	//
	// The three flat fields above are this map's TMDB entry, filled from it in
	// handleSystemStatus so the two can never disagree. They stay because every
	// existing consumer reads them, and because TMDB is still the provider the
	// first-run wizard and the Discover surfaces are about.
	//
	// Keyless providers are absent rather than "ok": what a provider needs is a
	// fact the client reads off the provider list, not a verdict this server
	// reached.
	MetadataCredentials map[string]credentialStateJSON `json:"metadata_credentials"`
	// UnhealthyDownloadClients names the external clients the queue poller
	// cannot reach (PLAN phase 6 task 4). Empty is the normal case; a
	// non-empty list is what raises the "client X unreachable" banner. The
	// embedded engine is never in it — it is not a client, and one dead
	// seedbox must not make Caravan look broken.
	UnhealthyDownloadClients []unhealthyClientJSON `json:"unhealthy_download_clients"`
	// StashUnreachable is the adult library handoff's twin of that list: the
	// Stash server the last scan or identity push could not reach, absent while
	// the handoff is healthy (PLAN phase 11 task 4). Absent, too, for a caller
	// the adult module is not visible to — omitempty so a module-off response
	// stays byte-identical to one from an install that never enabled it, exactly
	// as Counts.Sites does.
	//
	// It is a banner and never a blocker: the scan and the push are durable jobs
	// that deliver when Stash comes back, and the import that queued them
	// completed regardless.
	StashUnreachable *stashHealthJSON `json:"stash_unreachable,omitempty"`
	// FFmpegAvailable reports whether ffmpeg and ffprobe are both on PATH.
	// False hides the whole convert-for-TV affordance and degrades the
	// TV-incompatible warning to informational (SPEC §8).
	FFmpegAvailable bool `json:"ffmpeg_available"`
	// NeedsSetup keeps the SPA on first-run until both an administrator exists
	// and the storage step is complete. It is public through this endpoint while
	// the server is still open, so the SPA can choose setup before login.
	NeedsSetup bool `json:"needs_setup"`
	// PasswordSet and ListeningPublicly are the two halves of the nag in
	// SPEC §11: a server reachable from other machines with no login on it.
	// Since accounts replaced the single password, PasswordSet means "this
	// server has at least one account and is therefore gated" - the same
	// question the SPA has always asked of it. Neither is a credential.
	PasswordSet       bool `json:"password_set"`
	ListeningPublicly bool `json:"listening_publicly"`
	// Dirty says the previous session ended without a clean shutdown — a pulled
	// drive, a power cut, a kill -9 (SPEC §2.3). It stays true until
	// POST /system/verify passes, and while it is true downloads refuse to
	// resume. Only portable mode ever sets it.
	Dirty bool `json:"dirty"`
	// Runtime carries process diagnostics when the serving command supplied
	// them. It is absent for in-process tests and embedded servers.
	Runtime *runtimeJSON `json:"runtime,omitempty"`
}

// unhealthyClientJSON is one unreachable download client on GET
// /system/status. It carries no credential — the fields are the ones the
// settings screen already shows, plus the poll's own failure message
// (SPEC §12).
type unhealthyClientJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Error string `json:"error"`
	Since string `json:"since"`
}

type statusCounts struct {
	Movies     int `json:"movies"`
	Series     int `json:"series"`
	MediaFiles int `json:"media_files"`
	Unmatched  int `json:"unmatched"`
	// Wanted is the monitored-but-missing backlog (movies plus episodes),
	// the same list GET /wanted renders.
	Wanted int `json:"wanted"`
	// Converting is the open convert-for-TV queue: queued plus running.
	Converting int `json:"converting"`
	// Sites is the adult library's site count, present only for a caller the
	// module is visible to — omitempty so a module-off response stays
	// byte-identical to one from an install that never enabled it.
	Sites int `json:"sites,omitempty"`
}

// handleSystemStatus reports what the UI needs to render the shell: build
// version, deployment mode, where the library lives, and how much is in it.
//
// The counts come from the list queries rather than COUNT(*) so that SQL stays
// inside the store package. A phase-1 library is small enough that this is a
// non-issue; if it stops being one, the fix is a Count* method in the store,
// not a query here.
func (s *server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	root, err := s.st.GetSetting(ctx, store.SettingStorageRoot)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, "read storage root", err)
		return
	}

	mode, err := s.st.GetSetting(ctx, SettingMode)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, "read mode", err)
		return
	}
	if mode == "" {
		mode = ModeServer
	}

	schemaVersion, err := s.st.SchemaVersion()
	if err != nil {
		s.writeStoreError(w, "read schema version", err)
		return
	}

	movies, err := s.st.ListMovies(ctx)
	if err != nil {
		s.writeStoreError(w, "count movies", err)
		return
	}
	// Television only, for the reason handleListSeries gives: the count on the
	// status card is the television shelf's, and a number that silently
	// included sites would report the adult library to a caller who cannot see
	// it — including on an install where the module is off.
	series, err := s.st.ListSeriesByKind(ctx, core.SeriesKindTV)
	if err != nil {
		s.writeStoreError(w, "count series", err)
		return
	}
	// The adult shelf's count, only for a caller the module is visible to —
	// the same predicate that decides whether the nav item this badge sits on
	// exists at all.
	adultVisible, err := s.adultVisible(r)
	if err != nil {
		s.writeStoreError(w, "resolve adult visibility", err)
		return
	}
	sites := 0
	if adultVisible {
		adult, err := s.st.ListSeriesByKind(ctx, core.SeriesKindAdult)
		if err != nil {
			s.writeStoreError(w, "count sites", err)
			return
		}
		sites = len(adult)
	}
	files, err := s.st.ListMediaFiles(ctx)
	if err != nil {
		s.writeStoreError(w, "count media files", err)
		return
	}
	unmatched, err := s.st.ListUnmatchedFiles(ctx)
	if err != nil {
		s.writeStoreError(w, "count unmatched files", err)
		return
	}
	wantedLists, err := wanted.Compute(ctx, s.st)
	if err != nil {
		s.writeStoreError(w, "compute wanted list", err)
		return
	}
	conversions, err := s.st.ListConversions(ctx, 0)
	if err != nil {
		s.writeStoreError(w, "count conversions", err)
		return
	}
	converting := 0
	for _, c := range conversions {
		if core.ConversionOpen(c.Status) {
			converting++
		}
	}

	users, err := s.st.CountUsers(ctx)
	if err != nil {
		s.writeStoreError(w, "count users", err)
		return
	}

	// One read of the whole map, and the flat TMDB fields are lifted out of it
	// rather than derived a second time: two paths to the same answer are two
	// answers waiting to disagree.
	credentials, err := s.credentialStates(ctx)
	if err != nil {
		s.writeStoreError(w, "read metadata credential", err)
		return
	}
	tmdbCredential := credentials[core.ProviderTMDB]

	var diskFree, diskTotal int64
	if root != "" {
		if free, total, err := diskUsage(root); err == nil {
			diskFree, diskTotal = free, total
		}
		// A failure stays zeros: the storage root being unreadable is the
		// scanner's error to raise loudly, not the status endpoint's.
	}

	writeJSON(w, http.StatusOK, statusResponse{
		Version:       Version,
		Mode:          mode,
		StorageRoot:   root,
		SchemaVersion: schemaVersion,
		Scanning:      s.scanning.Load(),
		Counts: statusCounts{
			Movies:     len(movies),
			Series:     len(series),
			Sites:      sites,
			MediaFiles: len(files),
			Unmatched:  len(unmatched),
			// The shell badge covers both wanted scopes. The Movies page chip is
			// intentionally movie-only, so the two counts can differ.
			Wanted:     len(wantedLists.Movies) + len(wantedLists.Episodes),
			Converting: converting,
		},
		DiskFreeBytes:               diskFree,
		DiskTotalBytes:              diskTotal,
		EngineHealth:                s.engineHealth(),
		MetadataCredential:          tmdbCredential.State,
		MetadataCredentialReason:    tmdbCredential.Reason,
		MetadataCredentialCheckedAt: tmdbCredential.CheckedAt,
		MetadataCredentials:         credentials,
		UnhealthyDownloadClients:    s.unhealthyDownloadClients(),
		StashUnreachable:            s.stashHealth(adultVisible),
		FFmpegAvailable:             s.ffmpegAvailable(),
		NeedsSetup:                  root == "" || users == 0,
		PasswordSet:                 users > 0,
		ListeningPublicly:           listeningPublicly(s.listenAddr),
		Dirty:                       s.dirty.Load(),
		Runtime:                     s.runtimeStatus(),
	})
}

// engineHealth is what the system panel renders next to "Engine". A provider
// that can tell a failed engine from an unbuilt one implements HealthReporter;
// otherwise health is derived from whether an engine exists at all.
func (s *server) engineHealth() string {
	if s.engine == nil {
		return "unconfigured"
	}
	if hr, ok := s.engine.(HealthReporter); ok {
		return hr.Health()
	}
	if s.engine.Engine() == nil {
		return "unconfigured"
	}
	return "ok"
}

// unhealthyDownloadClients is the banner's input: the external clients the
// queue poller cannot reach right now. A provider that does not poll external
// clients — the phase-2 embedded-only wiring, and every test server built
// without one — reports none.
func (s *server) unhealthyDownloadClients() []unhealthyClientJSON {
	out := []unhealthyClientJSON{}
	if s.engine == nil {
		return out
	}
	reporter, ok := s.engine.(DownloadClientHealthReporter)
	if !ok {
		return out
	}
	for _, c := range reporter.UnhealthyDownloadClients() {
		out = append(out, unhealthyClientJSON{
			ID:    c.ID,
			Name:  c.Name,
			Type:  c.Type,
			Error: c.Error,
			Since: jsonTime(c.Since),
		})
	}
	return out
}
