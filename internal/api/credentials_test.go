package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// errKeyRejected is what a provider hands back for a credential it does not
// like. It wraps the core sentinel because that is the whole contract the
// credential-health model depends on: internal/api never imports internal/tmdb
// to find out that a key was refused.
var errKeyRejected = fmt.Errorf("tmdb: http 401: Invalid API key: %w", core.ErrMetadataUnauthorized)

// setSetting writes one setting straight to the store, for tests arranging the
// state a first run would have produced.
func setSetting(t *testing.T, st *store.Store, key, value string) {
	t.Helper()
	if err := st.SetSetting(context.Background(), key, value); err != nil {
		t.Fatalf("SetSetting(%s): %v", key, err)
	}
}

// credentialState reads GET /system/status's credential fields.
func credentialState(t *testing.T, h http.Handler) statusResponse {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var status statusResponse
	decodeBody(t, rec, &status)
	return status
}

// wantCode asserts the failure envelope carries a specific machine-readable
// code. The contract the SPA branches on to render a directed empty state
// instead of an error toast.
func wantCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body errorResponse
	decodeBody(t, rec, &body)
	if body.Code != want {
		t.Fatalf("error code = %q, want %q (body %q)", body.Code, want, rec.Body.String())
	}
	if body.Error == "" {
		t.Fatalf("error envelope is empty: %q", rec.Body.String())
	}
}

// TestSystemStatusReportsCredentialStateWithoutUpstreamCalls: the status
// endpoint is polled on a timer, so it must answer from the cached verdict and
// never from TMDB.
func TestSystemStatusReportsCredentialStateWithoutUpstreamCalls(t *testing.T) {
	h, st, mgr := newTestServer(t)

	// Fresh install: no key at all.
	if got := credentialState(t, h).MetadataCredential; got != CredentialAbsent {
		t.Fatalf("metadata_credential = %q, want %q on a fresh database", got, CredentialAbsent)
	}

	// A key nobody has proven wrong reads as ok, and polling never asks TMDB.
	setSetting(t, st, store.SettingTMDBAPIKey, "k")
	for i := 0; i < 5; i++ {
		if got := credentialState(t, h).MetadataCredential; got != CredentialOK {
			t.Fatalf("metadata_credential = %q, want %q", got, CredentialOK)
		}
	}
	if calls := mgr.validatedKeys(); len(calls) != 0 {
		t.Fatalf("status polling made %d upstream validations, want 0: %v", len(calls), calls)
	}
}

// TestMetadataCredentialTurnsInvalidWhenAMetadataCallIsRejected covers the
// second credential-state transition: a key that was fine when it was entered
// and has since been revoked flips the moment anything tries to use it, without
// waiting for someone to press Test.
func TestMetadataCredentialTurnsInvalidWhenAMetadataCallIsRejected(t *testing.T) {
	h, st, mgr := newTestServer(t)
	setSetting(t, st, store.SettingTMDBAPIKey, "revoked")
	provider := &countingProvider{}
	provider.err = errKeyRejected
	mgr.provider = provider

	if got := credentialState(t, h).MetadataCredential; got != CredentialOK {
		t.Fatalf("metadata_credential = %q, want %q before anything fails", got, CredentialOK)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantCode(t, rec, CodeMetadataCredentialInvalid)

	status := credentialState(t, h)
	if status.MetadataCredential != CredentialInvalid {
		t.Fatalf("metadata_credential = %q, want %q after a rejected call",
			status.MetadataCredential, CredentialInvalid)
	}
	if status.MetadataCredentialReason == "" {
		t.Error("an invalid credential reported no reason")
	}
	if strings.Contains(rec.Body.String(), "revoked") ||
		strings.Contains(status.MetadataCredentialReason, "revoked") {
		t.Errorf("the API key leaked into a response: %q / %q",
			rec.Body.String(), status.MetadataCredentialReason)
	}
	if status.MetadataCredentialCheckedAt == "" {
		t.Error("an invalid credential reported no check time")
	}

	// The state is cached, not re-derived: the next poll costs no upstream call
	// and the surface refuses before reaching the provider at all.
	before := provider.searches
	rec = do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantCode(t, rec, CodeMetadataCredentialInvalid)
	if provider.searches != before {
		t.Fatalf("a known-bad key still reached the provider: %d calls, want %d", provider.searches, before)
	}

	// Editing the key clears the verdict: the cache is keyed on the value, so a
	// different key is simply not the key that was rejected.
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"tmdb_api_key":"fresh"}`)
	wantStatus(t, rec, http.StatusOK)
	if got := credentialState(t, h).MetadataCredential; got != CredentialOK {
		t.Fatalf("metadata_credential = %q after an edit, want %q", got, CredentialOK)
	}
}

// TestMetadataTestEndpointProvesTheKey covers the Test button, including the
// first-run shape where the key is proved before it is ever written.
func TestMetadataTestEndpointProvesTheKey(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.validateKeys = map[string]error{"bad": errKeyRejected}

	// Nothing to test: the first-run wizard pressing Test on an empty field.
	rec := do(t, h, http.MethodPost, "/api/v1/settings/metadata/test", `{}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantCode(t, rec, CodeMetadataCredentialAbsent)

	// A key from the body, not the settings table, and nothing is stored by a
	// test, passing or failing.
	rec = do(t, h, http.MethodPost, "/api/v1/settings/metadata/test", `{"api_key":"bad"}`)
	wantStatus(t, rec, http.StatusBadGateway)
	wantCode(t, rec, CodeMetadataCredentialInvalid)
	if strings.Contains(rec.Body.String(), `"bad"`) {
		t.Errorf("the tested key was echoed back: %q", rec.Body.String())
	}
	if stored, _ := st.GetSetting(context.Background(), store.SettingTMDBAPIKey); stored != "" {
		t.Errorf("tmdb_api_key = %q after a test, want the test to store nothing", stored)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/settings/metadata/test", `{"api_key":"good"}`)
	wantStatus(t, rec, http.StatusOK)
	var ok map[string]string
	decodeBody(t, rec, &ok)
	if ok["status"] != "ok" {
		t.Fatalf("test response = %v, want status ok", ok)
	}

	// Saving a key the wizard just proved costs no second upstream call: the
	// verdict is cached against the key's value.
	before := len(mgr.validatedKeys())
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"tmdb_api_key":"good"}`)
	wantStatus(t, rec, http.StatusOK)
	if after := len(mgr.validatedKeys()); after != before {
		t.Fatalf("saving a proven key re-validated it: %d calls, want %d", after, before)
	}
	if got := credentialState(t, h).MetadataCredential; got != CredentialOK {
		t.Fatalf("metadata_credential = %q, want %q", got, CredentialOK)
	}

	// And a key saved without ever being tested is validated exactly once, so
	// the status card is right immediately rather than optimistically.
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"tmdb_api_key":"bad"}`)
	wantStatus(t, rec, http.StatusOK)
	if got := credentialState(t, h).MetadataCredential; got != CredentialInvalid {
		t.Fatalf("metadata_credential = %q after saving a rejected key, want %q", got, CredentialInvalid)
	}
	if calls := mgr.validatedKeys(); calls[len(calls)-1] != "bad" {
		t.Fatalf("last validation was for %q, want the key just saved", calls[len(calls)-1])
	}
}

// TestMetadataTestFallsBackToTheStoredKey mirrors the indexer Test button,
// which proves the configuration already on file.
func TestMetadataTestFallsBackToTheStoredKey(t *testing.T) {
	h, st, mgr := newTestServer(t)
	setSetting(t, st, store.SettingTMDBAPIKey, "stored")

	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/metadata/test", ""), http.StatusOK)
	calls := mgr.validatedKeys()
	if len(calls) != 1 || calls[0] != "stored" {
		t.Fatalf("validated %v, want exactly the stored key", calls)
	}
}

// TestGuardedSurfacesAnswerTypedCredentialErrors: every metadata-needing
// surface names the fix with a code the SPA can branch on, rather than a raw
// 502.
func TestGuardedSurfacesAnswerTypedCredentialErrors(t *testing.T) {
	surfaces := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"search", http.MethodGet, "/api/v1/search?q=dune", ""},
		{"discover home", http.MethodGet, "/api/v1/discover", ""},
		{"discover title", http.MethodGet, "/api/v1/discover/movie/603", ""},
		{"add movie", http.MethodPost, "/api/v1/library/movies", `{"tmdb_id":603}`},
		{"add series", http.MethodPost, "/api/v1/library/series", `{"tmdb_id":1396}`},
	}

	t.Run("absent", func(t *testing.T) {
		for _, surface := range surfaces {
			t.Run(surface.name, func(t *testing.T) {
				h, _, mgr := newTestServer(t)
				// No provider and no key: the first run that skipped the
				// metadata step.
				mgr.addErr = core.ErrNoMetadataProvider
				rec := do(t, h, surface.method, surface.path, surface.body)
				wantStatus(t, rec, http.StatusServiceUnavailable)
				wantCode(t, rec, CodeMetadataCredentialAbsent)
			})
		}
	})

	t.Run("rejected", func(t *testing.T) {
		for _, surface := range surfaces {
			t.Run(surface.name, func(t *testing.T) {
				h, st, mgr := newTestServer(t)
				setSetting(t, st, store.SettingTMDBAPIKey, "revoked")
				mgr.provider = &stubDiscoverProvider{
					stubProvider: stubProvider{err: errKeyRejected},
					err:          errKeyRejected,
				}
				mgr.addErr = errKeyRejected

				rec := do(t, h, surface.method, surface.path, surface.body)
				wantStatus(t, rec, http.StatusServiceUnavailable)
				wantCode(t, rec, CodeMetadataCredentialInvalid)

				if got := credentialState(t, h).MetadataCredential; got != CredentialInvalid {
					t.Fatalf("metadata_credential = %q, want %q", got, CredentialInvalid)
				}
			})
		}
	})
}

// TestProviderFailuresThatAreNotCredentialsStayBadGateway keeps the new code
// from swallowing every upstream problem: an endpoint having a bad day is not a
// reason to send someone to the settings screen.
func TestProviderFailuresThatAreNotCredentialsStayBadGateway(t *testing.T) {
	h, st, mgr := newTestServer(t)
	setSetting(t, st, store.SettingTMDBAPIKey, "k")
	mgr.provider = &stubProvider{err: fmt.Errorf("tmdb: get /search/movie: connection refused")}

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
	wantStatus(t, rec, http.StatusBadGateway)
	var body errorResponse
	decodeBody(t, rec, &body)
	if body.Code != "" {
		t.Fatalf("error code = %q, want none for a non-credential failure", body.Code)
	}
	if got := credentialState(t, h).MetadataCredential; got != CredentialOK {
		t.Fatalf("metadata_credential = %q, want %q — an unreachable TMDB is not a wrong key", got, CredentialOK)
	}
}

// TestRescanWithoutAMetadataKeyStillRuns: no key degrades matching, it does
// not stop the scanner.
func TestRescanWithoutAMetadataKeyStillRuns(t *testing.T) {
	h, _, mgr := newTestServer(t)

	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/library/rescan", ""), http.StatusAccepted)

	deadline := time.Now().Add(2 * time.Second)
	for mgr.scanCount.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a rescan without a TMDB key never reached the manager")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// countingProvider records how many searches actually reached the provider, so
// a test can prove a guarded surface refused before spending a round trip.
type countingProvider struct {
	stubProvider
	searches int
}

func (p *countingProvider) SearchMovies(ctx context.Context, q string) ([]core.MovieMeta, error) {
	p.searches++
	return p.stubProvider.SearchMovies(ctx, q)
}

func (p *countingProvider) SearchSeries(ctx context.Context, q string) ([]core.SeriesMeta, error) {
	p.searches++
	return p.stubProvider.SearchSeries(ctx, q)
}

// Per-provider credential state.
//
// "The metadata provider" stopped being singular when libraries gained chains,
// so the verdict did too. What these pin is that generalizing it narrowed
// nothing: the flat TMDB fields still say what they always said, and they say
// it because they are read out of the same map.

// TestProviderCredentialSettingsMatchStoreKeys is the fitness test
// ProviderDescriptor.CredentialSetting's comment names.
//
// core cannot import store, so the settings key is written out as a literal
// there and as a constant in store, and nothing but this makes the two agree. A
// silent disagreement is a credential card reading a settings row nobody writes:
// the key is stored, the state says "absent", and there is no error anywhere.
func TestProviderCredentialSettingsMatchStoreKeys(t *testing.T) {
	if got := core.ProviderCredentialSetting(core.ProviderTMDB); got != store.SettingTMDBAPIKey {
		t.Fatalf("tmdb credential setting = %q, want %q", got, store.SettingTMDBAPIKey)
	}
	if got := core.ProviderCredentialSetting(core.ProviderTheTVDB); got != store.SettingTheTVDBAPIKey {
		t.Fatalf("thetvdb credential setting = %q, want %q", got, store.SettingTheTVDBAPIKey)
	}

	// TheTVDB's PIN is the other half of one credential and is invisible to the
	// loop below, because CredentialSetting names one row. It has to be held to
	// the same three rules by hand: a PIN that could not be written would make a
	// user-supported subscription unusable, an untrimmed one would be sent with
	// the whitespace a paste brought along, and a readable one would put half a
	// credential in a browser response (SPEC §12).
	if !writableSettings[store.SettingTheTVDBPIN] {
		t.Errorf("%q is not writable through PUT /settings", store.SettingTheTVDBPIN)
	}
	if !trimmedSettings[store.SettingTheTVDBPIN] {
		t.Errorf("%q is not trimmed on the way in", store.SettingTheTVDBPIN)
	}
	if publicSettingKeys[store.SettingTheTVDBPIN] {
		t.Errorf("%q is readable through GET /settings — it is half a credential (SPEC §12)",
			store.SettingTheTVDBPIN)
	}

	// Every key the registry names must be one PUT /settings will accept and
	// store trimmed, or the card that offers a field would write somewhere the
	// credential machinery never looks.
	for _, p := range core.Providers() {
		if p.CredentialSetting == "" {
			continue
		}
		if !writableSettings[p.CredentialSetting] {
			t.Errorf("%s: %q is not writable through PUT /settings", p.ID, p.CredentialSetting)
		}
		if !trimmedSettings[p.CredentialSetting] {
			t.Errorf("%s: %q is not trimmed on the way in", p.ID, p.CredentialSetting)
		}
		if publicSettingKeys[p.CredentialSetting] {
			t.Errorf("%s: %q is readable through GET /settings — credentials are write-only (SPEC §12)",
				p.ID, p.CredentialSetting)
		}
	}
}

// The status map carries one entry per credentialed provider and agrees with
// the flat TMDB fields, which are lifted out of it.
func TestSystemStatusReportsPerProviderCredentials(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.validateKeys = map[string]error{"revoked": errKeyRejected}

	// Fresh install: TMDB is in the map and absent, and nothing else is in it.
	status := credentialState(t, h)
	assertCredentialMapAgrees(t, status)
	if got := status.MetadataCredentials[core.ProviderTMDB].State; got != CredentialAbsent {
		t.Fatalf("metadata_credentials[tmdb].state = %q, want %q", got, CredentialAbsent)
	}
	// A keyless provider has no server verdict at all: "Ready" is a fact the
	// client reads off the provider list, and an entry here saying "ok" for a
	// key that does not exist would be this server claiming to have checked
	// something.
	for _, p := range core.Providers() {
		if p.CredentialSetting != "" {
			continue
		}
		if _, ok := status.MetadataCredentials[p.ID]; ok {
			t.Errorf("keyless provider %q has a credential state: %+v", p.ID, status.MetadataCredentials[p.ID])
		}
	}

	// A stored key nobody has proven wrong, then one that was.
	setSetting(t, st, store.SettingTMDBAPIKey, "k")
	assertCredentialMapAgrees(t, credentialState(t, h))
	if got := credentialState(t, h).MetadataCredentials[core.ProviderTMDB].State; got != CredentialOK {
		t.Fatalf("metadata_credentials[tmdb].state = %q, want %q", got, CredentialOK)
	}

	wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings", `{"tmdb_api_key":"revoked"}`), http.StatusOK)
	status = credentialState(t, h)
	assertCredentialMapAgrees(t, status)
	entry := status.MetadataCredentials[core.ProviderTMDB]
	if entry.State != CredentialInvalid || entry.Reason == "" || entry.CheckedAt == "" {
		t.Fatalf("metadata_credentials[tmdb] = %+v, want an invalid verdict with a reason and a time", entry)
	}
	if strings.Contains(entry.Reason, "revoked") {
		t.Errorf("the API key leaked into the credential map: %q", entry.Reason)
	}
}

// assertCredentialMapAgrees is the invariant handleSystemStatus is written to
// keep: the three flat fields are the map's TMDB entry, so a client reading
// either one is reading the same verdict.
func assertCredentialMapAgrees(t *testing.T, status statusResponse) {
	t.Helper()
	entry, ok := status.MetadataCredentials[core.ProviderTMDB]
	if !ok {
		t.Fatalf("metadata_credentials has no tmdb entry: %+v", status.MetadataCredentials)
	}
	if entry.State != status.MetadataCredential ||
		entry.Reason != status.MetadataCredentialReason ||
		entry.CheckedAt != status.MetadataCredentialCheckedAt {
		t.Fatalf("metadata_credentials[tmdb] = %+v, want it to agree with the flat fields %q/%q/%q",
			entry, status.MetadataCredential, status.MetadataCredentialReason,
			status.MetadataCredentialCheckedAt)
	}
}

// A verdict belongs to the provider it was reached about. Sharing one across
// providers is the failure the whole per-provider model exists to prevent: a
// revoked TMDB key would mark a working TheTVDB key bad, and the settings
// screen would send someone to re-enter a credential that works.
func TestACredentialVerdictNeverAnswersForAnotherProvider(t *testing.T) {
	var c metadataCredentials

	// The same key string under two providers. It is the realistic shape of the
	// mistake: nothing stops a person pasting the same value into both cards,
	// and a cache keyed on the value alone would then be one verdict.
	c.record(core.ProviderTMDB, "shared", errKeyRejected)

	if rejected, reason, checkedAt := c.verdict(core.ProviderTMDB, "shared"); !rejected ||
		reason == "" || checkedAt.IsZero() {
		t.Fatalf("tmdb verdict = %v/%q/%v, want the rejection it was told about", rejected, reason, checkedAt)
	}
	if rejected, _, _ := c.verdict("thetvdb", "shared"); rejected {
		t.Fatal("a rejection recorded for tmdb answered for another provider")
	}

	// A pass for one provider leaves the other's rejection standing.
	c.record("thetvdb", "shared", nil)
	if rejected, _, _ := c.verdict(core.ProviderTMDB, "shared"); !rejected {
		t.Fatal("another provider's passing check cleared the tmdb rejection")
	}

	// And the verdict is still about that exact string: an edited key is simply
	// not the key that was refused.
	if rejected, _, _ := c.verdict(core.ProviderTMDB, "edited"); rejected {
		t.Fatal("an edited key inherited the old key's rejection")
	}
}

// soleCredentialedProvider is what decides whether a chain-level failure may be
// attributed at all. Getting it wrong in the permissive direction marks the
// wrong key bad; getting it wrong in the strict direction loses the transition
// that flips a revoked key without waiting for the Test button.
func TestSoleCredentialedProvider(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want string
	}{
		{"nothing ran", nil, ""},
		{"a keyless chain", []string{core.ProviderAniList, core.ProviderTVmaze}, ""},
		{"one credential", []string{core.ProviderTMDB}, core.ProviderTMDB},
		{"one credential beside keyless ones", []string{core.ProviderTMDB, core.ProviderAniList}, core.ProviderTMDB},
		{"the same one twice", []string{core.ProviderTMDB, core.ProviderTMDB}, core.ProviderTMDB},
		// An id nothing implements holds no credential, so it cannot be the one
		// that was refused and cannot make the answer ambiguous either. The row
		// for two registered credentials arrives with TheTVDB, which is the
		// first provider able to make this return "".
		{"an unknown id beside a credential", []string{core.ProviderTMDB, "nope"}, core.ProviderTMDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := soleCredentialedProvider(tt.ids); got != tt.want {
				t.Fatalf("soleCredentialedProvider(%v) = %q, want %q", tt.ids, got, tt.want)
			}
		})
	}
}

// The Test button names the provider it is testing. A default of TMDB keeps
// every body written before there was a second credentialed provider (and the
// bodyless POST the settings screen sends) meaning exactly what it meant.
func TestMetadataTestNamesItsProvider(t *testing.T) {
	t.Run("an explicit tmdb is the same request as none", func(t *testing.T) {
		h, st, mgr := newTestServer(t)
		setSetting(t, st, store.SettingTMDBAPIKey, "stored")

		wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/metadata/test",
			`{"provider":"tmdb"}`), http.StatusOK)

		want := validateCall{provider: core.ProviderTMDB, key: "stored"}
		if got := mgr.validations(); len(got) != 1 || got[0] != want {
			t.Fatalf("validated %v, want exactly [%v]", got, want)
		}
	})

	// A keyless provider's card has no field on it, so a Test that passed would
	// be reporting the health of nothing.
	t.Run("a keyless provider is refused", func(t *testing.T) {
		h, _, mgr := newTestServer(t)

		rec := do(t, h, http.MethodPost, "/api/v1/settings/metadata/test",
			`{"provider":"anilist","api_key":"whatever"}`)
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
		if got := mgr.validations(); len(got) != 0 {
			t.Fatalf("a keyless provider was validated: %v", got)
		}
	})

	t.Run("an unknown provider is refused", func(t *testing.T) {
		h, _, mgr := newTestServer(t)

		rec := do(t, h, http.MethodPost, "/api/v1/settings/metadata/test",
			`{"provider":"bogus","api_key":"whatever"}`)
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
		if got := mgr.validations(); len(got) != 0 {
			t.Fatalf("an unknown provider was validated: %v", got)
		}
	})

	// First run proves a user-supported TheTVDB pair before either half is
	// stored. The PIN has to travel in the body: the settings Test button can
	// read the stored one, but first run has nothing stored yet.
	t.Run("an unsaved thetvdb pin travels with the key", func(t *testing.T) {
		h, _, mgr := newTestServer(t)

		wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/metadata/test",
			`{"provider":"thetvdb","api_key":"supporter","pin":"1234"}`), http.StatusOK)

		want := validateCall{provider: core.ProviderTheTVDB, key: "supporter", pin: "1234"}
		if got := mgr.validations(); len(got) != 1 || got[0] != want {
			t.Fatalf("validated %v, want exactly [%v]", got, want)
		}
	})

	// A rejection from the Test button lands under the provider that was tested
	// and nowhere else.
	t.Run("a rejection lands under the provider tested", func(t *testing.T) {
		h, st, mgr := newTestServer(t)
		setSetting(t, st, store.SettingTMDBAPIKey, "revoked")
		mgr.validateKeys = map[string]error{core.ProviderTMDB + "/revoked": errKeyRejected}

		rec := do(t, h, http.MethodPost, "/api/v1/settings/metadata/test", `{"provider":"tmdb"}`)
		wantStatus(t, rec, http.StatusBadGateway)
		wantCode(t, rec, CodeMetadataCredentialInvalid)

		status := credentialState(t, h)
		assertCredentialMapAgrees(t, status)
		if got := status.MetadataCredentials[core.ProviderTMDB].State; got != CredentialInvalid {
			t.Fatalf("metadata_credentials[tmdb].state = %q, want %q", got, CredentialInvalid)
		}
		// One entry per credentialed provider and no more: the keyless ones are
		// absent because "Ready" is a fact the client reads off the provider
		// list, not a verdict this server reached.
		if len(status.MetadataCredentials) != credentialedProviderCount() {
			t.Fatalf("credential map = %+v, want only the credentialed providers in it",
				status.MetadataCredentials)
		}
		// The other credential is untouched: nobody entered a TheTVDB key, and a
		// TMDB rejection is not a reason to say anything about it.
		if got := status.MetadataCredentials[core.ProviderTheTVDB].State; got != CredentialAbsent {
			t.Fatalf("metadata_credentials[thetvdb].state = %q, want %q", got, CredentialAbsent)
		}
	})
}

// credentialedProviderCount is how many entries GET /system/status' credential
// map must carry. It is derived rather than written out so registering a
// provider does not turn a count assertion into a false failure.
func credentialedProviderCount() int {
	n := 0
	for _, p := range core.Providers() {
		if p.CredentialSetting != "" {
			n++
		}
	}
	return n
}

// TheTVDB: the first consumer of the per-provider credential model.
//
// TMDB was the only credential when the model was generalized, so nothing could
// prove that generalizing it meant anything. These are that proof.

// the acceptance assertion of the per-provider model: a rejected TheTVDB key
// marks TheTVDB and nothing else. While "the metadata credential" was one
// field, this rejection would have put the TMDB card (and every Discover
// surface behind it) into the "your key is wrong" state, and the fix offered
// would have been to re-enter a key that works.
func TestARejectedTheTVDBKeyLeavesTMDBHealthy(t *testing.T) {
	h, st, mgr := newTestServer(t)
	setSetting(t, st, store.SettingTMDBAPIKey, "good")
	setSetting(t, st, store.SettingTheTVDBAPIKey, "revoked")
	mgr.validateKeys = map[string]error{core.ProviderTheTVDB + "/revoked": errKeyRejected}

	rec := do(t, h, http.MethodPost, "/api/v1/settings/metadata/test", `{"provider":"thetvdb"}`)
	wantStatus(t, rec, http.StatusBadGateway)
	wantCode(t, rec, CodeMetadataCredentialInvalid)

	status := credentialState(t, h)
	assertCredentialMapAgrees(t, status)
	if got := status.MetadataCredentials[core.ProviderTheTVDB].State; got != CredentialInvalid {
		t.Fatalf("metadata_credentials[thetvdb].state = %q, want %q", got, CredentialInvalid)
	}
	if got := status.MetadataCredentials[core.ProviderTMDB].State; got != CredentialOK {
		t.Fatalf("metadata_credentials[tmdb].state = %q, want it untouched at %q", got, CredentialOK)
	}
	// The flat field is the TMDB entry, so the same thing said the old way.
	if status.MetadataCredential != CredentialOK {
		t.Fatalf("metadata_credential = %q, want the TMDB key still %q",
			status.MetadataCredential, CredentialOK)
	}
	// The Test button asked about the provider whose card it sits on, with the
	// key stored for that provider.
	want := validateCall{provider: core.ProviderTheTVDB, key: "revoked"}
	if got := mgr.validations(); len(got) != 1 || got[0] != want {
		t.Fatalf("validated %v, want exactly [%v]", got, want)
	}
}

// A PIN edit is a credential edit. TheTVDB's login consumes the key and the PIN
// together, so saving a PIN changes the exchange even though the settings row
// ProviderDescriptor.CredentialSetting names did not move, and the verdict on
// file is then about a login this server no longer makes.
func TestATheTVDBPINEditRechecksTheCredential(t *testing.T) {
	h, st, mgr := newTestServer(t)
	setSetting(t, st, store.SettingTheTVDBAPIKey, "supporter-key")

	// Prove the pair, so a verdict is cached against the key string.
	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/metadata/test",
		`{"provider":"thetvdb"}`), http.StatusOK)
	if got := credentialState(t, h).MetadataCredentials[core.ProviderTheTVDB].State; got != CredentialOK {
		t.Fatalf("metadata_credentials[thetvdb].state = %q, want %q", got, CredentialOK)
	}

	// The pair stops working, and the only thing that changed is the PIN.
	mgr.validateKeys = map[string]error{core.ProviderTheTVDB + "/supporter-key": errKeyRejected}
	wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings",
		`{"thetvdb_pin":"9999"}`), http.StatusOK)
	if got := credentialState(t, h).MetadataCredentials[core.ProviderTheTVDB].State; got != CredentialInvalid {
		t.Fatalf("metadata_credentials[thetvdb].state = %q after a PIN edit, want %q — "+
			"the cached verdict was about a login this server no longer makes", got, CredentialInvalid)
	}

	// And the way back out. A mistyped PIN that is corrected has to clear the
	// rejection, or the card would stay red until somebody retyped the key.
	mgr.validateKeys = nil
	wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings",
		`{"thetvdb_pin":"1234"}`), http.StatusOK)
	if got := credentialState(t, h).MetadataCredentials[core.ProviderTheTVDB].State; got != CredentialOK {
		t.Fatalf("metadata_credentials[thetvdb].state = %q after the PIN was fixed, want %q", got, CredentialOK)
	}
}

// TheTVDB needed no edit to publicSettings to get its "a key is stored" flag:
// the loop that landed with the credential map derives one per credentialed
// descriptor. Neither half of the credential is readable.
func TestTheTVDBKeySetFlagComesFromTheRegistry(t *testing.T) {
	h, st, _ := newTestServer(t)
	const setFlag = store.SettingTheTVDBAPIKey + credentialSetSuffix

	rec := do(t, h, http.MethodGet, "/api/v1/settings", "")
	wantStatus(t, rec, http.StatusOK)
	var settings map[string]string
	decodeBody(t, rec, &settings)
	if settings[setFlag] != "false" {
		t.Fatalf("fresh settings = %v, want %s=false", settings, setFlag)
	}

	setSetting(t, st, store.SettingTheTVDBAPIKey, "licensed-key")
	setSetting(t, st, store.SettingTheTVDBPIN, "1234")

	rec = do(t, h, http.MethodGet, "/api/v1/settings", "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &settings)
	if settings[setFlag] != "true" {
		t.Fatalf("settings = %v, want %s=true", settings, setFlag)
	}
	if _, ok := settings[store.SettingTheTVDBAPIKey]; ok {
		t.Errorf("settings exposed %s: %v", store.SettingTheTVDBAPIKey, settings)
	}
	// The PIN is half a credential and gets no flag and no value: there is one
	// card and one question, "is a credential on file".
	if _, ok := settings[store.SettingTheTVDBPIN]; ok {
		t.Errorf("settings exposed %s: %v", store.SettingTheTVDBPIN, settings)
	}
	if _, ok := settings[store.SettingTheTVDBPIN+credentialSetSuffix]; ok {
		t.Errorf("settings carry a set-flag for the PIN: %v", settings)
	}
}

// Both halves are stored with their surrounding whitespace removed. A pasted
// PIN that kept its trailing space would be sent to /login verbatim while
// everything that judges the credential trimmed it. A card reading healthy
// while nothing works.
func TestTheTVDBCredentialIsTrimmedOnTheWayIn(t *testing.T) {
	h, st, _ := newTestServer(t)

	wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings",
		`{"thetvdb_api_key":"  licensed-key  ","thetvdb_pin":" 1234 "}`), http.StatusOK)

	ctx := context.Background()
	for key, want := range map[string]string{
		store.SettingTheTVDBAPIKey: "licensed-key",
		store.SettingTheTVDBPIN:    "1234",
	} {
		got, err := st.GetSetting(ctx, key)
		if err != nil {
			t.Fatalf("GetSetting(%s): %v", key, err)
		}
		if got != want {
			t.Errorf("stored %s = %q, want %q", key, got, want)
		}
	}
}

// The value that is stored must be the value that was validated and the value
// that is sent upstream. Everything inside internal/api trims a credential
// before judging it, while the clients built in cmd/caravan send the stored
// string verbatim, so storing the untrimmed one cached a verdict about a string
// nothing would ever send.
func TestSettingsPutStoresCredentialsTrimmed(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.validateKeys = map[string]error{"padded": errKeyRejected}

	wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings",
		`{"tmdb_api_key":"  padded  "}`), http.StatusOK)

	stored, err := st.GetSetting(context.Background(), store.SettingTMDBAPIKey)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if stored != "padded" {
		t.Fatalf("tmdb_api_key = %q, want it stored trimmed", stored)
	}
	// And the verdict is about that same string, so the status card is not
	// reporting the health of a key nothing uses.
	if got := credentialState(t, h).MetadataCredential; got != CredentialInvalid {
		t.Fatalf("metadata_credential = %q, want %q for the rejected key that was stored",
			got, CredentialInvalid)
	}
}

// The adult module's own 503s must name the adult credential. An uncoded 503 is
// read by the SPA as a missing TMDB key (web/src/lib/credentials.ts reads a
// bare 503 that way for back-compat), which would send an admin to the wrong
// settings screen to fix a credential that was never the problem.
func TestAdultSurfacesNameTheAdultCredential(t *testing.T) {
	t.Run("site search", func(t *testing.T) {
		h, st, mgr := newTestServer(t)
		enableAdult(t, st)
		mgr.adult = nil

		rec := do(t, h, http.MethodGet, "/api/v1/adult/search?q=brazzers", "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantCode(t, rec, CodeAdultCredentialAbsent)
	})

	t.Run("adult discover", func(t *testing.T) {
		h, st, mgr := newTestServer(t)
		enableAdult(t, st)
		mgr.adult = nil

		rec := do(t, h, http.MethodGet, "/api/v1/adult/discover", "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantCode(t, rec, CodeAdultCredentialAbsent)
	})

	// The reachable one: the requests list renders without any provider at
	// all, so an admin approving a scene asked for yesterday meets this the
	// moment the stash-box credential goes bad.
	t.Run("approving a scene request", func(t *testing.T) {
		h, st, mgr := newTestServer(t)
		enableAdult(t, st)
		mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}

		rec := do(t, h, http.MethodPost, "/api/v1/requests",
			`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact"}`)
		wantStatus(t, rec, http.StatusCreated)
		var created requestJSON
		decodeBody(t, rec, &created)

		mgr.adult = nil
		rec = do(t, h, http.MethodPost,
			fmt.Sprintf("/api/v1/requests/%d/approve", created.ID), "{}")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantCode(t, rec, CodeAdultCredentialAbsent)
	})
}
