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

// Credential health (PLAN phase 10 tasks 2-4).
//
// Every metadata-needing surface in Caravan sits behind one credential — the
// TMDB API key — and before this phase a missing or wrong one surfaced as a
// 503 with a prose message or, once the key was wrong rather than absent, as a
// raw 502 from whatever call happened to fail first. Neither told the SPA what
// to render.
//
// Two things fix that, and they are deliberately separate:
//
//   - A cached verdict. GET /system/status reports "absent", "invalid" or "ok"
//     and never calls TMDB to find out, because the status endpoint is polled
//     on a timer and a poll that costs an upstream round trip is a poll that
//     gets rate limited. The verdict is only ever refreshed by something the
//     user did: the test button, a key edit, or a metadata call that came back
//     rejected.
//   - A stable error code. Every guarded surface answers with one of the
//     credential codes below, so the SPA branches on the code and shows the
//     directed empty state instead of an error toast.

// Metadata credential states reported by GET /system/status.
const (
	// CredentialAbsent means no TMDB API key has been entered.
	CredentialAbsent = "absent"
	// CredentialInvalid means the key on file was rejected, either by the test
	// button, by the check that runs when it is saved, or by a metadata call
	// that answered 401.
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
	// CodeMetadataCredentialAbsent means the surface needs the TMDB key and
	// none has been entered — the fix is Settings → Metadata.
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

// metadataCredential is the cached verdict on one TMDB API key.
//
// The cache key is the key value itself rather than a generation counter,
// following the stash-box client cache in cmd/caravan: the verdict is a fact
// about that exact string, so a key edited to something else invalidates it by
// simply not matching, and a key edited back to a value that was already
// proven wrong is known to be wrong without asking again.
//
// Only a rejection is ever cached. A validation that failed because the network
// was down says nothing about the credential, so it leaves no verdict and the
// state stays optimistic — an unreachable TMDB is not a wrong API key, and
// telling the user to go fix their key would be a lie.
type metadataCredential struct {
	mu sync.Mutex
	// key is the API key rejected is about. Empty means nothing is cached.
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

// record stores the verdict of a live check of key. A nil err clears any
// rejection; core.ErrMetadataUnauthorized records one; anything else is an
// upstream problem and leaves the cache alone.
func (c *metadataCredential) record(key string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case err == nil:
		c.key, c.rejected, c.reason, c.checkedAt = key, false, "", time.Now()
	case errors.Is(err, core.ErrMetadataUnauthorized):
		c.key, c.rejected, c.reason, c.checkedAt = key, true, err.Error(), time.Now()
	}
}

// verdict reports whether key is known to have been rejected, and why.
func (c *metadataCredential) verdict(key string) (rejected bool, reason string, checkedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.key != key {
		return false, "", time.Time{}
	}
	return c.rejected, c.reason, c.checkedAt
}

// metadataCredentialState is the cached health of the TMDB key: one settings
// read, never an upstream call.
func (s *server) metadataCredentialState(ctx context.Context) (state, reason string, checkedAt time.Time, err error) {
	key, err := s.settingValue(ctx, store.SettingTMDBAPIKey)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if key == "" {
		return CredentialAbsent, "", time.Time{}, nil
	}
	rejected, reason, checkedAt := s.credentials.verdict(key)
	if rejected {
		return CredentialInvalid, reason, checkedAt, nil
	}
	return CredentialOK, "", checkedAt, nil
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

// metadataProvider resolves the provider for a surface that cannot work
// without it, answering the typed 503 the SPA turns into a directed empty state
// and reporting false when there is nothing to call.
//
// Absence is asked of the manager rather than of the settings table because the
// manager is what would do the work: it holds the provider, and in the serving
// process that provider exists exactly when the key does. What the settings
// table is consulted for is the cached verdict — refusing a key already known
// to be rejected, so the SPA gets its empty state without Caravan spending a
// round trip proving something it was told an hour ago.
func (s *server) metadataProvider(w http.ResponseWriter, r *http.Request) (core.MetadataProvider, bool) {
	provider := s.mgr.Metadata()
	if provider == nil {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialAbsent,
			"no metadata provider configured")
		return nil, false
	}

	key, err := s.settingValue(r.Context(), store.SettingTMDBAPIKey)
	if err != nil {
		s.writeStoreError(w, "read metadata credential", err)
		return nil, false
	}
	if rejected, _, _ := s.credentials.verdict(key); rejected {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialInvalid,
			"the TMDB API key was rejected")
		return nil, false
	}
	return provider, true
}

// noteMetadataFailure records a rejected credential seen by a live metadata
// call, and reports whether that is what it was.
//
// This is the second of the two transitions in PLAN phase 10 task 2: a key that
// was valid when it was entered and has since been revoked flips to invalid the
// first time anything tries to use it, without waiting for someone to press
// Test.
//
// The key is re-read here rather than threaded through every call site because
// the error path is rare and a settings read is cheap; the request's own
// context is not used because a caller that gave up is exactly when this is
// most worth recording.
func (s *server) noteMetadataFailure(err error) bool {
	if !errors.Is(err, core.ErrMetadataUnauthorized) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	key, readErr := s.settingValue(ctx, store.SettingTMDBAPIKey)
	if readErr != nil || key == "" {
		return true
	}
	s.credentials.record(key, err)
	return true
}

// writeMetadataError reports a failed metadata call, turning a rejected
// credential into the typed answer the guarded surfaces use and leaving every
// other failure as the bad gateway it was.
func (s *server) writeMetadataError(w http.ResponseWriter, msg string, err error) {
	if s.noteMetadataFailure(err) {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialInvalid,
			"the TMDB API key was rejected")
		return
	}
	s.log.Error(msg, "error", err)
	writeError(w, http.StatusBadGateway, msg)
}

// metadataTestRequest is the body of POST /settings/metadata/test.
type metadataTestRequest struct {
	// APIKey is the key to prove. Empty means "the one already saved", which
	// is what the settings screen's Test button sends and what the indexer test
	// does by construction (it reads the stored row).
	//
	// First run sends a key that has not been saved yet, deliberately: the
	// wizard proves the key before it writes it, so a fresh install never
	// stores a credential it knows is wrong.
	APIKey string `json:"api_key"`
}

// handleMetadataTest proves a TMDB API key against TMDB (PLAN phase 10 task 4),
// mirroring POST /indexers/{id}/test.
//
// The verdict is cached against the key that was tested, so testing a key in
// the first-run wizard and then saving it costs one upstream call, not two: the
// save finds a verdict for that exact string and believes it.
//
// The response never echoes the key, and neither does the log line — the
// provider's message can quote a request, and SPEC §12 keeps credentials out of
// both.
func (s *server) handleMetadataTest(w http.ResponseWriter, r *http.Request) {
	var body metadataTestRequest
	// A bodyless POST is legitimate here: "test what is saved".
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}

	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		stored, err := s.settingValue(r.Context(), store.SettingTMDBAPIKey)
		if err != nil {
			s.writeStoreError(w, "read tmdb api key", err)
			return
		}
		key = stored
	}
	if key == "" {
		writeCodedError(w, http.StatusBadRequest, CodeMetadataCredentialAbsent,
			"no TMDB API key to test")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), credentialCheckTimeout)
	defer cancel()

	if err := s.mgr.ValidateMetadataKey(ctx, key); err != nil {
		s.credentials.record(key, err)
		if errors.Is(err, core.ErrMetadataUnauthorized) {
			writeCodedError(w, http.StatusBadGateway, CodeMetadataCredentialInvalid,
				"metadata test failed: "+err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "metadata test failed: "+err.Error())
		return
	}
	s.credentials.record(key, nil)
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
// A key that already carries a verdict — the wizard tested it a moment ago — is
// believed, so this costs nothing on the path the UI actually takes.
func (s *server) revalidateMetadataKey(ctx context.Context, key string) {
	if key == "" {
		return
	}
	if _, _, checkedAt := s.credentials.verdict(key); !checkedAt.IsZero() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, credentialCheckTimeout)
	defer cancel()

	s.credentials.record(key, s.mgr.ValidateMetadataKey(ctx, key))
}
