package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stash"
	"github.com/watzon/caravan/internal/stash/stashtest"
	"github.com/watzon/caravan/internal/store"
)

// stubStash stands in for the handoff service: it answers the health question
// and counts the times the API told it to forget the answer.
type stubStash struct {
	health stash.Health
	resets int
}

func (s *stubStash) Health() stash.Health { return s.health }

func (s *stubStash) ResetHealth() {
	s.resets++
	s.health = stash.Health{}
}

// adminSession turns the module on and logs an admin in, which is the state
// every Stash-card test starts from.
func adminSession(t *testing.T, h http.Handler, st *store.Store) *http.Cookie {
	t.Helper()
	enableAdult(t, st)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	return login(t, h, testAdmin, testPassword)
}

// The card round-trips: what POST stores is what GET reports, and the three
// keys land in the settings table under the names the handoff reads.
func TestStashConfigRoundTrips(t *testing.T) {
	h, st, _ := newTestServer(t)
	cookie := adminSession(t, h, st)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stash",
		`{"url":"http://stash.lan:9999/","api_key":" secret ","enabled":true}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	var saved stashJSON
	decodeBody(t, rec, &saved)
	// The trailing slash is trimmed on the way in, because the client appends
	// /graphql and a double slash is a 404 on some reverse proxies.
	if saved.URL != "http://stash.lan:9999" || saved.APIKey != "secret" || !saved.Enabled {
		t.Fatalf("saved = %+v", saved)
	}

	rec = doAuth(t, h, http.MethodGet, "/api/v1/adult/stash", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var got stashJSON
	decodeBody(t, rec, &got)
	if got != saved {
		t.Errorf("GET = %+v, want %+v", got, saved)
	}

	values, err := st.AllSettings(context.Background())
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if values[store.SettingStashURL] != "http://stash.lan:9999" ||
		values[store.SettingStashAPIKey] != "secret" ||
		values[store.SettingStashEnabled] != "true" {
		t.Errorf("settings = %+v, want the three stash keys", values)
	}
}

func TestStashConfigRejectsBadInput(t *testing.T) {
	h, st, _ := newTestServer(t)
	cookie := adminSession(t, h, st)

	for _, tc := range []struct{ name, body string }{
		{"not a URL", `{"url":"stash.lan:9999","enabled":false}`},
		{"wrong scheme", `{"url":"ftp://stash.lan","enabled":false}`},
		{"enabled without a URL", `{"url":"","enabled":true}`},
	} {
		rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stash", tc.body, withCookie(cookie))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400 (body %q)", tc.name, rec.Code, rec.Body.String())
		}
	}
	// Nothing was stored by a rejected save.
	values, err := st.AllSettings(context.Background())
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if _, present := values[store.SettingStashURL]; present {
		t.Errorf("a rejected save wrote %q", store.SettingStashURL)
	}
}

// The Test button proves the address and the credential before they are saved,
// exactly as the Jellyfin card's does.
func TestStashTestButtonProvesTheServer(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		RequireAPIKey: true,
		Operations: map[string][]stashtest.Response{
			"Version": {stashtest.Data(`{"version":{"version":"v0.28.1","hash":"abc1234"}}`)},
		},
	})
	t.Cleanup(srv.Close)

	h, st, _ := newTestServer(t)
	cookie := adminSession(t, h, st)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stash/test",
		`{"url":"`+srv.URL()+`","api_key":"secret"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var got stashTestJSON
	decodeBody(t, rec, &got)
	if got.Version != "v0.28.1" || got.Hash != "abc1234" {
		t.Errorf("test result = %+v", got)
	}

	// A failure reports the server's own message rather than a bare "it did not
	// work", and never the credential.
	srv.SetOperation("Version", stashtest.Unauthorized())
	rec = doAuth(t, h, http.MethodPost, "/api/v1/adult/stash/test",
		`{"url":"`+srv.URL()+`","api_key":"wrong"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusBadGateway)
	if body := rec.Body.String(); !strings.Contains(body, "stash test failed") {
		t.Errorf("failed test body = %q, want the reason", body)
	} else if strings.Contains(body, "wrong") {
		t.Errorf("failed test body leaked the API key: %q", body)
	}
}

// A blank field on a test falls back to what is stored, so "test the saved
// configuration" is an empty object.
func TestStashTestFallsBackToStoredConfig(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		RequireAPIKey: true,
		Operations: map[string][]stashtest.Response{
			"Version": {stashtest.Data(`{"version":{"version":"v0.28.1","hash":"abc"}}`)},
		},
	})
	t.Cleanup(srv.Close)

	h, st, _ := newTestServer(t)
	cookie := adminSession(t, h, st)
	rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stash",
		`{"url":"`+srv.URL()+`","api_key":"secret","enabled":true}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	rec = doAuth(t, h, http.MethodPost, "/api/v1/adult/stash/test", `{}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if got := srv.Requests(); len(got) != 1 || got[0].APIKey != "secret" {
		t.Errorf("requests = %+v, want one carrying the stored key", got)
	}
}

// A member is refused before the adult gate even runs: memberAllowed names none
// of the Stash routes, which is the ordinary rule and the safe direction.
func TestStashCardIsAdminOnly(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	grantAdultAccess(t, st, member.ID, true)
	cookie := login(t, h, testMember, testPassword)

	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/adult/stash", ""},
		{http.MethodPost, "/api/v1/adult/stash", `{"url":"http://x.test","enabled":true}`},
		{http.MethodPost, "/api/v1/adult/stash/test", `{}`},
	} {
		rec := doAuth(t, h, route.method, route.path, route.body, withCookie(cookie))
		if rec.Code == http.StatusOK {
			t.Errorf("%s %s = 200 for a granted member, want a refusal", route.method, route.path)
		}
	}
}

// The keys are an adult-module feature, so they must be absent from GET
// /settings for a caller the module is not visible to, not merely unwritable. A
// settings object carrying a stash_url is the module announcing itself on an
// install where it is supposed to be gone.
func TestStashSettingsAreAbsentWhileTheModuleIsOff(t *testing.T) {
	h, st, _ := newTestServer(t)
	cookie := adminSession(t, h, st)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stash",
		`{"url":"http://stash.lan:9999","api_key":"secret","enabled":true}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	// While the module is on, an admin sees them.
	rec = doAuth(t, h, http.MethodGet, "/api/v1/settings", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var on map[string]string
	decodeBody(t, rec, &on)
	if _, present := on[store.SettingStashURL]; !present {
		t.Fatalf("settings = %+v, want the stash card while the module is on", on)
	}

	// Switched off, they are gone, and so is the card's own endpoint.
	setAdultLibrariesActive(t, st, false)
	rec = doAuth(t, h, http.MethodGet, "/api/v1/settings", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var off map[string]string
	decodeBody(t, rec, &off)
	for _, key := range []string{store.SettingStashURL, store.SettingStashAPIKey, store.SettingStashEnabled} {
		if _, present := off[key]; present {
			t.Errorf("settings still carry %q with the module off", key)
		}
	}
	if rec := doAuth(t, h, http.MethodGet, "/api/v1/adult/stash", "", withCookie(cookie)); rec.Code != http.StatusNotFound {
		t.Errorf("GET /adult/stash with the module off = %d, want 404", rec.Code)
	}
}

// And they cannot be planted through the generic settings PUT either: the
// allowlist does not name them, so the write is refused rather than quietly
// creating a card outside its own door.
func TestStashSettingsAreNotWritableThroughPutSettings(t *testing.T) {
	h, st, _ := newTestServer(t)
	cookie := adminSession(t, h, st)

	rec := doAuth(t, h, http.MethodPut, "/api/v1/settings",
		`{"`+store.SettingStashURL+`":"http://evil.test"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusBadRequest)

	values, err := st.AllSettings(context.Background())
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if _, present := values[store.SettingStashURL]; present {
		t.Errorf("PUT /settings wrote %q", store.SettingStashURL)
	}
}

// An unreachable Stash raises a banner on the status endpoint, the same model
// an unreachable download client gets. It is a banner and never a blocker: the
// scan and the push are durable jobs that deliver when Stash returns.
func TestUnreachableStashSurfacesOnSystemStatus(t *testing.T) {
	since := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	svc := &stubStash{health: stash.Health{Error: "connection refused", Since: since}}
	h, st, _ := newTestServer(t, WithStash(svc))
	cookie := adminSession(t, h, st)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var status struct {
		StashUnreachable *stashHealthJSON `json:"stash_unreachable"`
	}
	decodeBody(t, rec, &status)
	if status.StashUnreachable == nil {
		t.Fatalf("status carries no stash_unreachable, body %q", rec.Body.String())
	}
	if status.StashUnreachable.Error != "connection refused" {
		t.Errorf("banner error = %q", status.StashUnreachable.Error)
	}
	if status.StashUnreachable.Since != since.Format(time.RFC3339) {
		t.Errorf("banner since = %q, want %q", status.StashUnreachable.Since, since.Format(time.RFC3339))
	}
}

// A healthy handoff, and a server wired without one, both report nothing. The
// field is absent rather than an empty object, so a client can treat presence
// as the banner condition.
func TestHealthyStashRaisesNoBanner(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{"healthy", []Option{WithStash(&stubStash{})}},
		{"not wired", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := newTestServer(t, tc.opts...)
			cookie := adminSession(t, h, st)
			rec := doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", withCookie(cookie))
			wantStatus(t, rec, http.StatusOK)
			if strings.Contains(rec.Body.String(), "stash_unreachable") {
				t.Errorf("status carries stash_unreachable: %s", rec.Body.String())
			}
		})
	}
}

// The banner is adult-visible only. Telling an install with the module off that
// its Stash is unreachable announces a feature that is supposed to be absent.
func TestStashBannerIsAbsentWhileTheModuleIsOff(t *testing.T) {
	svc := &stubStash{health: stash.Health{Error: "connection refused", Since: time.Now()}}
	h, st, _ := newTestServer(t, WithStash(svc))
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "stash_unreachable") {
		t.Errorf("status carries stash_unreachable with the module off: %s", rec.Body.String())
	}
}

// A URL carrying userinfo is a credential in disguise: Go's client turns
// http://user:pass@stash.lan into an Authorization header, and the stored string
// is then echoed by GET /adult/stash and handed to whatever logs the address the
// handoff is talking to. Stash's own credential is the field next to this one.
func TestStashConfigRejectsAURLCarryingCredentials(t *testing.T) {
	h, st, _ := newTestServer(t)
	cookie := adminSession(t, h, st)

	for _, path := range []string{"/api/v1/adult/stash", "/api/v1/adult/stash/test"} {
		rec := doAuth(t, h, http.MethodPost, path,
			`{"url":"http://user:hunter2@stash.lan:9999","api_key":"secret","enabled":true}`,
			withCookie(cookie))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400 for a URL with userinfo (body %q)",
				path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Errorf("POST %s echoed the password back: %q", path, rec.Body.String())
		}
	}
	values, err := st.AllSettings(context.Background())
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if got := values[store.SettingStashURL]; got != "" {
		t.Errorf("stored url = %q, want nothing written", got)
	}
}

// The handoff remembers its verdict rather than probing for it, so the settings
// screen is the only place that can tell it the question changed. Without this
// the banner outlives the fix: a user who corrects the address, or switches the
// handoff off, keeps being told their Stash is unreachable.
func TestSavingTheStashCardForgetsAStaleBanner(t *testing.T) {
	svc := &stubStash{health: stash.Health{Error: "connection refused", Since: time.Now()}}
	h, st, _ := newTestServer(t, WithStash(svc))
	cookie := adminSession(t, h, st)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stash",
		`{"url":"http://stash.lan:9999","api_key":"secret","enabled":true}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if svc.resets != 1 {
		t.Fatalf("resets after a changed card = %d, want 1", svc.resets)
	}

	// An identical save is not a change, so it is not a reason to hide a genuine
	// outage.
	rec = doAuth(t, h, http.MethodPost, "/api/v1/adult/stash",
		`{"url":"http://stash.lan:9999","api_key":"secret","enabled":true}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if svc.resets != 1 {
		t.Errorf("resets after an unchanged save = %d, want it left alone", svc.resets)
	}

	// Switching the handoff off is a change, and answers the banner.
	rec = doAuth(t, h, http.MethodPost, "/api/v1/adult/stash",
		`{"url":"http://stash.lan:9999","api_key":"secret","enabled":false}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if svc.resets != 2 {
		t.Errorf("resets after switching the handoff off = %d, want 2", svc.resets)
	}
}

// A successful test is the only probe in the system, so it is the one place a
// stale banner can be cleared the moment the user proves it wrong.
func TestASuccessfulStashTestForgetsAStaleBanner(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"Version": {stashtest.Data(`{"version":{"version":"v0.28.1","hash":"abc"}}`)},
		},
	})
	t.Cleanup(srv.Close)

	svc := &stubStash{health: stash.Health{Error: "connection refused", Since: time.Now()}}
	h, st, _ := newTestServer(t, WithStash(svc))
	cookie := adminSession(t, h, st)

	// A failing test proves nothing, so it clears nothing.
	srv.SetOperation("Version", stashtest.Unauthorized())
	rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stash/test",
		`{"url":"`+srv.URL()+`","api_key":"wrong"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusBadGateway)
	if svc.resets != 0 {
		t.Fatalf("resets after a failed test = %d, want 0", svc.resets)
	}

	srv.SetOperation("Version", stashtest.Data(`{"version":{"version":"v0.28.1","hash":"abc"}}`))
	rec = doAuth(t, h, http.MethodPost, "/api/v1/adult/stash/test",
		`{"url":"`+srv.URL()+`","api_key":"secret"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if svc.resets != 1 {
		t.Errorf("resets after a successful test = %d, want 1", svc.resets)
	}

	// And the banner is gone from the status endpoint without a restart.
	rec = doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "stash_unreachable") {
		t.Errorf("status still carries stash_unreachable: %s", rec.Body.String())
	}
}
