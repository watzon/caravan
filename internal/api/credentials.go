package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// Credential health.
//
// A metadata-needing surface in Caravan sits behind a credential. A missing or
// wrong one used to surface as a 503 with a prose message or, once the key was
// wrong rather than absent, as a raw 502 from whatever call failed first.
// Neither told the SPA what to render.
//
// Two things fix that, and they are deliberately separate:
//
//   - A cached verdict. GET /system/status reports "absent", "invalid" or "ok"
//     and never calls the provider to find out, because the status endpoint is
//     polled on a timer and a poll that costs an upstream round trip is a poll
//     that gets rate limited. The verdict is only ever refreshed by something
//     the user did: the test button, a key edit, or a metadata call that came
//     back rejected.
//   - A stable error code. Every guarded surface answers with one of the
//     credential codes below, so the SPA branches on the code and shows the
//     directed empty state instead of an error toast.
//
// The verdict is per-provider, keyed by provider id, because "the metadata
// provider" stopped being singular when libraries gained chains: a rejected
// TMDB key says nothing about a TheTVDB key, and a model that cannot tell them
// apart either marks a working credential bad or lets a broken one read as ok.
// Which providers have a credential at all is core's answer
// (ProviderDescriptor.CredentialSetting), so a provider added later joins the
// status map, the revalidation loop and the "<setting>_set" projection without
// an edit here.

// Metadata credential states reported by GET /system/status.
const (
	// CredentialAbsent means no API key has been entered for the provider.
	CredentialAbsent = "absent"
	// CredentialInvalid means the key on file was rejected, either by the test
	// button, by the check that runs when it is saved, or by a metadata call
	// that answered 401 and could say which provider answered it.
	CredentialInvalid = "invalid"
	// CredentialOK means a key is on file and nothing has rejected it.
	//
	// It is deliberately the optimistic answer for a key that has never been
	// checked: a key nobody has proven wrong is treated as right, because the
	// alternative is either an upstream call the status poll must not make or a
	// fourth state every client would have to render as "probably fine".
	CredentialOK = "ok"
)

// Error codes carried by errorResponse.Code. They are the contract the SPA
// branches on; the messages beside them are for humans and may be reworded.
const (
	// CodeMetadataCredentialAbsent means the surface needs a metadata key and
	// none has been entered. The fix is Settings → Metadata.
	CodeMetadataCredentialAbsent = "metadata_credential_absent"
	// CodeMetadataCredentialInvalid means a key is on file and the provider
	// rejected it.
	CodeMetadataCredentialInvalid = "metadata_credential_invalid"
	// CodeAdultCredentialInvalid means the stash-box endpoint and key handed to
	// the adult enable gate did not work, so nothing was committed.
	CodeAdultCredentialInvalid = "adult_credential_invalid"
	// CodeAdultCredentialAbsent means enabling the adult module was asked for
	// with no stash-box credential to enable it with.
	CodeAdultCredentialAbsent = "adult_credential_absent"
)

// credentialCheckTimeout bounds one live validation. It is well under the
// providers' own 15s-30s client timeouts so a settings save cannot hang on a
// slow provider for longer than a person will wait.
const credentialCheckTimeout = 12 * time.Second

// credentialVerdict is the cached verdict on one provider's API key.
//
// The cache key is the key value itself rather than a generation counter,
// following the stash-box client cache in cmd/caravan: the verdict is a fact
// about that exact string, so a key edited to something else invalidates it by
// simply not matching, and a key edited back to a value that was already
// proven wrong is known to be wrong without asking again.
type credentialVerdict struct {
	// key is the API key rejected is about.
	key string
	// rejected is true when key was refused by the provider.
	rejected bool
	// reason is the provider's own message for the rejection, kept so the
	// settings screen can say *why*. It never contains the key: the provider
	// clients strip the URL from transport errors for that reason.
	reason string
	// checkedAt is when the verdict was reached.
	checkedAt time.Time
}

// metadataCredentials holds one verdict per credentialed provider.
//
// Only a rejection is ever cached. A validation that failed because the network
// was down says nothing about the credential, so it leaves no verdict and the
// state stays optimistic. An unreachable provider is not a wrong API key, and
// telling the user to go fix their key would be a lie.
//
// A keyless provider never appears here, not even as an "ok": that a provider
// needs no credential is a fact about what is compiled in, which the client
// reads off the provider list, not a verdict this server reached about
// anything.
type metadataCredentials struct {
	mu sync.Mutex
	// byProvider maps a provider id to its verdict. A provider with no entry
	// has nothing cached.
	byProvider map[string]credentialVerdict
}

// record stores the verdict of a live check of providerID's key. A nil err
// clears any rejection; core.ErrMetadataUnauthorized records one; anything else
// is an upstream problem and leaves the cache alone.
func (c *metadataCredentials) record(providerID, key string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var v credentialVerdict
	switch {
	case err == nil:
		v = credentialVerdict{key: key, checkedAt: time.Now()}
	case errors.Is(err, core.ErrMetadataUnauthorized):
		v = credentialVerdict{key: key, rejected: true, reason: err.Error(), checkedAt: time.Now()}
	default:
		return
	}
	if c.byProvider == nil {
		c.byProvider = make(map[string]credentialVerdict)
	}
	c.byProvider[providerID] = v
}

// forget drops whatever is cached for providerID, so the next check actually
// asks instead of believing what is on file.
//
// It exists for the one credential that is not a single string. TheTVDB's login
// consumes a key AND, for a user-supported subscription, a PIN, while this
// cache is keyed on the key alone, so editing the PIN changes the exchange
// without changing the cache key, and the verdict left behind is about a login
// this server no longer makes. That is stale in both directions: a repaired PIN
// would stay "invalid" until somebody retyped the key, and a broken one would
// go on reading "ok".
func (c *metadataCredentials) forget(providerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.byProvider, providerID)
}

// verdict reports whether providerID's key is known to have been rejected, and
// why. A verdict recorded for one provider never answers for another: that is
// the whole point of keying the cache by id.
func (c *metadataCredentials) verdict(providerID, key string) (rejected bool, reason string, checkedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	v, ok := c.byProvider[providerID]
	if !ok || v.key != key {
		return false, "", time.Time{}
	}
	return v.rejected, v.reason, v.checkedAt
}

// credentialStateJSON is one provider's credential health as GET /system/status
// reports it.
type credentialStateJSON struct {
	State string `json:"state"`
	// Reason is the provider's own words for a rejection, empty for every other
	// state and never containing the key (SPEC §12).
	Reason string `json:"reason,omitempty"`
	// CheckedAt is when the cached verdict was reached, empty when the key has
	// never been checked.
	CheckedAt string `json:"checked_at,omitempty"`
}

// metadataCredentialState is the cached health of one provider's API key: one
// settings read, never an upstream call.
//
// A keyless provider is never asked, and answers with the zero value if it is:
// "Ready" is a client-side fact about a provider that needs nothing entered,
// not a verdict this server reached. See credentialStates, which is what builds
// the map and which skips them.
func (s *server) metadataCredentialState(ctx context.Context, providerID string) (credentialStateJSON, error) {
	setting := core.ProviderCredentialSetting(providerID)
	if setting == "" {
		return credentialStateJSON{}, nil
	}
	key, err := s.settingValue(ctx, setting)
	if err != nil {
		return credentialStateJSON{}, err
	}
	if key == "" {
		return credentialStateJSON{State: CredentialAbsent}, nil
	}
	rejected, reason, checkedAt := s.credentials.verdict(providerID, key)
	if rejected {
		return credentialStateJSON{State: CredentialInvalid, Reason: reason, CheckedAt: jsonTime(checkedAt)}, nil
	}
	return credentialStateJSON{State: CredentialOK, CheckedAt: jsonTime(checkedAt)}, nil
}

// credentialStates is every credentialed provider's health, keyed by provider
// id. It is what GET /system/status reports, and the flat TMDB fields beside it
// are read out of this same map so the two cannot disagree.
//
// Keyless providers are absent rather than "ok" (see metadataCredentialState)
// which also keeps the payload bounded by the number of keys a person can enter
// rather than by the size of the registry.
func (s *server) credentialStates(ctx context.Context) (map[string]credentialStateJSON, error) {
	out := make(map[string]credentialStateJSON)
	for _, p := range core.Providers() {
		if p.CredentialSetting == "" {
			continue
		}
		state, err := s.metadataCredentialState(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out[p.ID] = state
	}
	return out, nil
}

// providerName is the registry's label for an id, empty for an id the registry
// does not carry. It is what puts the provider's own name in the messages
// below, so a rejected TheTVDB key does not send someone to the TMDB card.
func providerName(id string) string {
	for _, p := range core.Providers() {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}

// credentialRejectedMessage says whose key was refused.
//
// An unknown or keyless id leaves it deliberately vague, because that is what
// the caller knew: naming a provider there would be the same guess
// noteMetadataFailure refuses to make.
func credentialRejectedMessage(providerID string) string {
	if name := providerName(providerID); name != "" {
		return "the " + name + " API key was rejected"
	}
	return "a metadata API key was rejected"
}

// soleCredentialedProvider names the one provider among ids that holds a
// credential, or "" when none or more than one does.
//
// It is how a chain-level failure is attributed: a chain of one credentialed
// provider leaves nothing to work out, while a chain of two knows a key was
// refused and not which, and "" is the honest answer to that.
func soleCredentialedProvider(ids []string) string {
	found := ""
	for _, id := range ids {
		if core.ProviderCredentialSetting(id) == "" {
			continue
		}
		if found != "" && found != id {
			return ""
		}
		found = id
	}
	return found
}

// settingValue reads one setting, treating "never set" as the empty string.
// It is the read half of every credential check in this file.
func (s *server) settingValue(ctx context.Context, key string) (string, error) {
	value, err := s.st.GetSetting(ctx, key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// metadataProvider resolves the provider for a surface that cannot work without
// it, answering the typed 503 the SPA turns into a directed empty state and
// reporting false when there is nothing to call.
//
// Absence is asked of the manager rather than of the settings table because the
// manager is what would do the work: it holds the provider, and in the serving
// process that provider exists exactly when the key does. What the settings
// table is consulted for is the cached verdict, refusing a key already known to
// be rejected, so the SPA gets its empty state without Caravan spending a round
// trip proving something it was told an hour ago.
func (s *server) metadataProvider(w http.ResponseWriter, r *http.Request) (core.MetadataProvider, bool) {
	provider := s.mgr.Metadata()
	if provider == nil {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialAbsent,
			"no metadata provider configured")
		return nil, false
	}

	if s.tmdbKeyRejected(w, r) {
		return nil, false
	}
	return provider, true
}

// credentialRejected reports whether any of the named providers holds a key
// that has already been refused, writing the typed 503 itself.
//
// It is separate from metadataProvider because the verdict and the provider are
// separate questions once a library can be chained to something other than
// TMDB: a per-library search has to ask "is this chain's key bad" without
// asking "is TMDB configured", and answers the second for itself by walking the
// chain (see handleSearch). A store read that fails counts as refused. The
// response has been written either way, and the caller must stop.
//
// Keyless ids on the list are skipped rather than refused: a chain is a list of
// providers, not a list of credentials, and the ones that need nothing entered
// can never be the reason a search is refused.
func (s *server) credentialRejected(w http.ResponseWriter, r *http.Request, providerIDs []string) bool {
	ctx := r.Context()
	for _, id := range providerIDs {
		setting := core.ProviderCredentialSetting(id)
		if setting == "" {
			continue
		}
		key, err := s.settingValue(ctx, setting)
		if err != nil {
			s.writeStoreError(w, "read metadata credential", err)
			return true
		}
		if rejected, _, _ := s.credentials.verdict(id, key); rejected {
			writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialInvalid,
				credentialRejectedMessage(id))
			return true
		}
	}
	return false
}

// tmdbKeyRejected is credentialRejected for the surfaces that are TMDB's alone.
//
// Discover is the one that matters: its curated shelves are TMDB list ids, so
// there is no chain to consult and nothing else to ask. It stays a named
// function rather than an inline slice so those surfaces say which provider
// they are about.
func (s *server) tmdbKeyRejected(w http.ResponseWriter, r *http.Request) bool {
	return s.credentialRejected(w, r, []string{core.ProviderTMDB})
}

// noteMetadataFailure records a rejected credential seen by a live metadata
// call, and reports whether that is what it was.
//
// This is the second of the two credential-state transitions: a key that was
// valid when it was entered and has since been revoked flips to invalid the
// first time anything tries to use it, without waiting for someone to press
// Test.
//
// providerID is whose credential the caller can prove the failure was about. An
// empty id (or one that holds no credential) records nothing while still
// reporting true, and that refusal is the point: a call that walked a chain
// knows a key was refused but not which one, and marking a key bad on a guess
// sends someone to re-enter a credential that works while the broken one goes
// on reading "ok". The typed error code the caller writes stays right either
// way; only the attribution is withheld.
//
// The key is re-read here rather than threaded through every call site because
// the error path is rare and a settings read is cheap; the request's own
// context is not used because a caller that gave up is exactly when this is
// most worth recording.
func (s *server) noteMetadataFailure(providerID string, err error) bool {
	if !errors.Is(err, core.ErrMetadataUnauthorized) {
		return false
	}
	setting := core.ProviderCredentialSetting(providerID)
	if setting == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	key, readErr := s.settingValue(ctx, setting)
	if readErr != nil || key == "" {
		return true
	}
	s.credentials.record(providerID, key, err)
	return true
}

// writeMetadataError reports a failed metadata call, turning a rejected
// credential into the typed answer the guarded surfaces use and leaving every
// other failure as the bad gateway it was.
//
// providerIDs are the providers the call could have reached. A chain, for the
// per-library search. The rejection is attributed only when exactly one of them
// is credentialed, which is the difference between knowing whose key was
// refused and guessing; see soleCredentialedProvider and noteMetadataFailure.
func (s *server) writeMetadataError(w http.ResponseWriter, providerIDs []string, msg string, err error) {
	// A chain with nothing configured on it is the absent-credential answer,
	// not a bad gateway. Before per-library search this could not be reached
	// here (the caller had already resolved a provider) but a chain resolves
	// inside the manager now, so "no provider" arrives as an error rather than
	// as a nil, and the SPA needs the same directed empty state either way.
	if errors.Is(err, core.ErrNoMetadataProvider) {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialAbsent,
			"no metadata provider configured")
		return
	}
	culprit := soleCredentialedProvider(providerIDs)
	if s.noteMetadataFailure(culprit, err) {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialInvalid,
			credentialRejectedMessage(culprit))
		return
	}
	s.log.Error(msg, "error", err)
	writeError(w, http.StatusBadGateway, msg)
}

// metadataTestRequest is the body of POST /settings/metadata/test.
type metadataTestRequest struct {
	// Provider names whose key is being proved. Empty means TMDB, which keeps a
	// bodyless POST and every body written before there was a second
	// credentialed provider meaning exactly what it meant.
	Provider string `json:"provider"`

	// APIKey is the key to prove. Empty means "the one already saved", which
	// is what the settings screen's Test button sends and what the indexer test
	// does by construction (it reads the stored row).
	//
	// First run sends a key that has not been saved yet, deliberately: the
	// wizard proves the key before it writes it, so a fresh install never
	// stores a credential it knows is wrong.
	APIKey string `json:"api_key"`

	// PIN is TheTVDB's subscriber PIN. Empty means "use the stored one".
	PIN string `json:"pin"`
}

// handleMetadataTest proves one provider's API key against that provider,
// mirroring POST /indexers/{id}/test.
//
// The verdict is cached against the provider and the key that was tested, so
// testing a key in the first-run wizard and then saving it costs one upstream
// call, not two: the save finds a verdict for that exact pair and believes it.
//
// A provider id nothing implements, and one that needs no key at all, are both
// refused as bad requests rather than answered "ok": there is no credential
// behind either, and a Test button that passes for a card with no field on it
// would be reporting the health of nothing.
//
// The response never echoes the key, and neither does the log line. The
// provider's message can quote a request, and SPEC §12 keeps credentials out
// of both.
func (s *server) handleMetadataTest(w http.ResponseWriter, r *http.Request) {
	var body metadataTestRequest
	// A bodyless POST is legitimate here: "test what is saved".
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}

	providerID := strings.TrimSpace(body.Provider)
	if providerID == "" {
		providerID = core.ProviderTMDB
	}
	setting := core.ProviderCredentialSetting(providerID)
	if setting == "" {
		writeError(w, http.StatusBadRequest, "provider "+providerID+" has no API key to test")
		return
	}

	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		stored, err := s.settingValue(r.Context(), setting)
		if err != nil {
			s.writeStoreError(w, "read metadata api key", err)
			return
		}
		key = stored
	}
	if key == "" {
		writeCodedError(w, http.StatusBadRequest, CodeMetadataCredentialAbsent,
			"no "+providerName(providerID)+" API key to test")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), credentialCheckTimeout)
	defer cancel()

	if err := s.mgr.ValidateMetadataKey(ctx, providerID, key, strings.TrimSpace(body.PIN)); err != nil {
		s.credentials.record(providerID, key, err)
		if errors.Is(err, core.ErrMetadataUnauthorized) {
			writeCodedError(w, http.StatusBadGateway, CodeMetadataCredentialInvalid,
				"metadata test failed: "+err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "metadata test failed: "+err.Error())
		return
	}
	s.credentials.record(providerID, key, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// revalidateMetadataKey runs the one live check a key edit is allowed to cost.
//
// It is called after the settings write, not before it: a key the provider
// dislikes is still the key the user asked to store, and refusing the save
// would leave them with no way to correct a typo in the field they are looking
// at. What the check buys is that the state on the status card is right
// immediately, rather than staying optimistically "ok" until something fails.
//
// A key that already carries a verdict (the wizard tested it a moment ago) is
// believed, so this costs nothing on the path the UI actually takes.
func (s *server) revalidateMetadataKey(ctx context.Context, providerID, key string) {
	if key == "" {
		return
	}
	if _, _, checkedAt := s.credentials.verdict(providerID, key); !checkedAt.IsZero() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, credentialCheckTimeout)
	defer cancel()

	s.credentials.record(providerID, key, s.mgr.ValidateMetadataKey(ctx, providerID, key, ""))
}
