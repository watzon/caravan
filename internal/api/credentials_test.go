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
// code — the contract the SPA branches on to render a directed empty state
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

// TestSystemStatusReportsCredentialStateWithoutUpstreamCalls is the acceptance
// criterion in PLAN phase 10: the status endpoint is polled on a timer, so it
// must answer from the cached verdict and never from TMDB.
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

// TestMetadataCredentialTurnsInvalidWhenAMetadataCallIsRejected is the second
// transition in PLAN phase 10 task 2: a key that was fine when it was entered
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

// TestMetadataTestEndpointProvesTheKey covers the Test button (PLAN phase 10
// task 4), including the first-run shape where the key is proved before it is
// ever written.
func TestMetadataTestEndpointProvesTheKey(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.validateKeys = map[string]error{"bad": errKeyRejected}

	// Nothing to test: the first-run wizard pressing Test on an empty field.
	rec := do(t, h, http.MethodPost, "/api/v1/settings/metadata/test", `{}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantCode(t, rec, CodeMetadataCredentialAbsent)

	// A key from the body, not the settings table — and nothing is stored by a
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

// TestGuardedSurfacesAnswerTypedCredentialErrors is PLAN phase 10 task 3: every
// metadata-needing surface names the fix with a code the SPA can branch on,
// rather than a raw 502.
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

// TestRescanWithoutAMetadataKeyStillRuns is the other half of PLAN phase 10
// task 3: no key degrades matching, it does not stop the scanner.
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

// ---------------------------------------------------------------------------
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
		// for two REGISTERED credentials arrives with TheTVDB, which is the
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
// every body written before there was a second credentialed provider — and the
// bodyless POST the settings screen sends — meaning exactly what it meant.
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
		if len(status.MetadataCredentials) != 1 {
			t.Fatalf("credential map = %+v, want only the credentialed providers in it",
				status.MetadataCredentials)
		}
	})
}

// ---------------------------------------------------------------------------
// Adult enable gating (PLAN phase 10 task 5).
//
// Turning the module on is one decision, and the server refuses it as a unit: a
// stash-box credential that does not work leaves the endpoint, the key and
// adult_enabled exactly as they were. Cancel changes nothing because a failed
// enable already changed nothing.

// adultSettings reads the two stash-box settings and the module switch.
func adultSettings(t *testing.T, st *store.Store) (endpoint, key string, enabled bool) {
	t.Helper()
	ctx := context.Background()
	settings, err := st.AllSettings(ctx)
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	enabled, err = st.AdultEnabled(ctx)
	if err != nil {
		t.Fatalf("AdultEnabled: %v", err)
	}
	return settings[store.SettingStashboxEndpoint], settings[store.SettingStashboxAPIKey], enabled
}

func TestAdultEnableLeavesEverythingOffWhenTheCredentialFails(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		validateErr error
		wantStatus  int
		wantCode    string
		// wantValidated is whether the endpoint should have been contacted at
		// all. A malformed endpoint is refused before any request is made.
		wantValidated bool
	}{
		{
			name:       "no credential anywhere",
			body:       `{"enabled":true}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeAdultCredentialAbsent,
		},
		{
			name:       "blank key",
			body:       `{"enabled":true,"stashbox_api_key":"  "}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeAdultCredentialAbsent,
		},
		{
			name:       "endpoint that could never be dialled",
			body:       `{"enabled":true,"stashbox_endpoint":"/graphql","stashbox_api_key":"k"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:          "the endpoint rejects the key",
			body:          `{"enabled":true,"stashbox_endpoint":"https://stashdb.org/graphql","stashbox_api_key":"nope"}`,
			validateErr:   fmt.Errorf("stashbox: SearchSites: unauthorized"),
			wantStatus:    http.StatusBadGateway,
			wantCode:      CodeAdultCredentialInvalid,
			wantValidated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, mgr := newTestServer(t)
			mgr.adultCredentialErr = tt.validateErr

			rec := do(t, h, http.MethodPost, "/api/v1/settings/adult", tt.body)
			wantStatus(t, rec, tt.wantStatus)
			if tt.wantCode != "" {
				wantCode(t, rec, tt.wantCode)
			} else {
				wantErrorBody(t, rec)
			}

			endpoint, key, enabled := adultSettings(t, st)
			if enabled {
				t.Error("adult_enabled is on after a failed enable")
			}
			if endpoint != "" || key != "" {
				t.Errorf("a failed enable stored endpoint=%q key=%q, want nothing", endpoint, key)
			}
			if got := len(mgr.adultCredentials()) > 0; got != tt.wantValidated {
				t.Errorf("credential contacted = %v, want %v", got, tt.wantValidated)
			}
		})
	}
}

func TestAdultEnableCommitsTheCredentialItProved(t *testing.T) {
	h, st, mgr := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/settings/adult",
		`{"enabled":true,"stashbox_endpoint":"https://stashdb.org/graphql","stashbox_api_key":"k"}`)
	wantStatus(t, rec, http.StatusOK)

	// The credential in the request is the one that was tested — not whatever
	// happened to be stored.
	want := adultCredential{endpoint: "https://stashdb.org/graphql", key: "k"}
	if got := mgr.adultCredentials(); len(got) != 1 || got[0] != want {
		t.Fatalf("validated %v, want exactly [%v]", got, want)
	}

	endpoint, key, enabled := adultSettings(t, st)
	if !enabled {
		t.Error("adult_enabled is off after a passing enable")
	}
	if endpoint != want.endpoint || key != want.key {
		t.Errorf("stored endpoint=%q key=%q, want %q / %q", endpoint, key, want.endpoint, want.key)
	}
	// The enable created the library row the module needs, exactly as before.
	if _, err := st.GetLibraryByKind(context.Background(), core.LibraryKindAdult); err != nil {
		t.Errorf("GetLibraryByKind: %v", err)
	}
}

// A blank endpoint is legal and means the TPDB preset — pasting a key is the
// whole configuration for the default provider.
func TestAdultEnableAcceptsTheDefaultEndpoint(t *testing.T) {
	h, st, mgr := newTestServer(t)

	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/adult",
		`{"enabled":true,"stashbox_endpoint":"","stashbox_api_key":"k"}`), http.StatusOK)

	if got := mgr.adultCredentials(); len(got) != 1 || got[0].endpoint != "" {
		t.Fatalf("validated %v, want the blank endpoint forwarded as the preset", got)
	}
	if _, _, enabled := adultSettings(t, st); !enabled {
		t.Error("adult_enabled is off after a passing enable")
	}
}

// Re-enabling a module that was configured once needs no credential in the
// body: the stored one is tested instead.
func TestAdultEnableFallsBackToTheStoredCredential(t *testing.T) {
	h, st, mgr := newTestServer(t)
	setSetting(t, st, store.SettingStashboxEndpoint, "https://stashdb.org/graphql")
	setSetting(t, st, store.SettingStashboxAPIKey, "stored")

	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/adult", `{"enabled":true}`), http.StatusOK)

	want := adultCredential{endpoint: "https://stashdb.org/graphql", key: "stored"}
	if got := mgr.adultCredentials(); len(got) != 1 || got[0] != want {
		t.Fatalf("validated %v, want exactly [%v]", got, want)
	}
}

// Switching a module off must work when the credential behind it has expired,
// which is one of the reasons a person switches it off.
func TestAdultDisableNeverValidates(t *testing.T) {
	h, st, mgr := newTestServer(t)
	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/adult",
		`{"enabled":true,"stashbox_api_key":"k"}`), http.StatusOK)

	mgr.adultCredentialErr = fmt.Errorf("stashbox: SearchSites: unauthorized")
	before := len(mgr.adultCredentials())

	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/adult", `{"enabled":false}`), http.StatusOK)

	if _, _, enabled := adultSettings(t, st); enabled {
		t.Error("adult_enabled survived a disable")
	}
	if after := len(mgr.adultCredentials()); after != before {
		t.Fatalf("disabling made %d validations, want none", after-before)
	}
}

// The enable gate is not the only way in. Both stash-box settings are writable
// through PUT /settings, so the credential of a module that is ALREADY on can
// be replaced or blanked there — and the invariant SPEC §10.2 states is that
// the switch is on only while a credential that was proved sits behind it, not
// that it was proved once.
func TestSettingsPutCannotBreakTheCredentialOfALiveAdultModule(t *testing.T) {
	// enabled arranges a module that is on with a working credential, the way
	// the enable gate leaves it.
	enabled := func(t *testing.T) (http.Handler, *store.Store, *stubManager) {
		t.Helper()
		h, st, mgr := newTestServer(t)
		wantStatus(t, do(t, h, http.MethodPost, "/api/v1/settings/adult",
			`{"enabled":true,"stashbox_endpoint":"https://stashdb.org/graphql","stashbox_api_key":"good"}`),
			http.StatusOK)
		return h, st, mgr
	}

	t.Run("clearing the key is refused", func(t *testing.T) {
		h, st, _ := enabled(t)

		rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"stashbox_api_key":""}`)
		wantStatus(t, rec, http.StatusBadRequest)
		wantCode(t, rec, CodeAdultCredentialAbsent)

		endpoint, key, on := adultSettings(t, st)
		if key != "good" || endpoint != "https://stashdb.org/graphql" || !on {
			t.Fatalf("stored endpoint=%q key=%q enabled=%v, want the credential untouched",
				endpoint, key, on)
		}
	})

	t.Run("a rejected replacement is refused", func(t *testing.T) {
		h, st, mgr := enabled(t)
		mgr.adultCredentialErr = fmt.Errorf("stashbox: SearchSites: unauthorized")

		rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"stashbox_api_key":"typo"}`)
		wantStatus(t, rec, http.StatusBadGateway)
		wantCode(t, rec, CodeAdultCredentialInvalid)
		if strings.Contains(rec.Body.String(), "typo") {
			t.Errorf("the rejected key was echoed back: %q", rec.Body.String())
		}

		if _, key, _ := adultSettings(t, st); key != "good" {
			t.Fatalf("stashbox_api_key = %q, want the working key kept", key)
		}
	})

	t.Run("an endpoint edit is validated against the stored key", func(t *testing.T) {
		h, _, mgr := enabled(t)
		before := len(mgr.adultCredentials())

		wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings",
			`{"stashbox_endpoint":"https://fansdb.cc/graphql"}`), http.StatusOK)

		got := mgr.adultCredentials()
		if len(got) != before+1 {
			t.Fatalf("validations = %d, want one more than %d", len(got), before)
		}
		want := adultCredential{endpoint: "https://fansdb.cc/graphql", key: "good"}
		if got[len(got)-1] != want {
			t.Fatalf("validated %v, want %v — the pair the write would leave behind",
				got[len(got)-1], want)
		}
	})

	// With the module off there is nothing running against the credential and
	// the enable gate still has to prove whatever is stored, so configuring it
	// ahead of time stays a free, upstream-free write.
	t.Run("the module being off costs no upstream call", func(t *testing.T) {
		h, _, mgr := newTestServer(t)

		wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings",
			`{"stashbox_api_key":"later"}`), http.StatusOK)

		if got := mgr.adultCredentials(); len(got) != 0 {
			t.Fatalf("a settings write with the module off validated %v, want nothing", got)
		}
	})
}

// The value that is stored must be the value that was validated and the value
// that is sent upstream. Everything inside internal/api trims a credential
// before judging it, while the clients built in cmd/caravan send the stored
// string verbatim — so storing the untrimmed one cached a verdict about a
// string nothing would ever send.
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

// The adult module's own 503s must name the ADULT credential. An uncoded 503 is
// read by the SPA as a missing TMDB key (web/src/lib/credentials.ts reads a bare
// 503 that way for back-compat), which would send an admin to the wrong settings
// screen to fix a credential that was never the problem.
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
