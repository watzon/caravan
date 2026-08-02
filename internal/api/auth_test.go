package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/store"
)

const testPassword = "correct horse battery"

// setPassword writes a hash directly, the way a server that already has a
// password set starts up.
func setPassword(t *testing.T, st *store.Store, password string) {
	t.Helper()
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if err := st.SetSetting(context.Background(), store.SettingPasswordHash, hash); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
}

// login exchanges the password for a session cookie, failing the test when the
// login itself fails.
func login(t *testing.T, h http.Handler, password string) *http.Cookie {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", `{"password":`+quote(password)+`}`)
	wantStatus(t, rec, http.StatusOK)
	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("login set no session cookie")
	}
	return cookie
}

func quote(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(out)
}

func sessionCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

// doAuth is `do` with request decoration - a cookie, a header - so the matrix
// below can send the same request with each credential in turn.
func doAuth(t *testing.T, h http.Handler, method, target, body string, decorate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", contentTypeJSON)
	}
	if decorate != nil {
		decorate(r)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// The pre-phase-5 contract: with no password, nothing changes. This is the
// regression that keeps the gate from becoming mandatory by accident.
func TestNoPasswordLeavesTheAPIOpen(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, target := range []string{
		"/api/v1/system/status",
		"/api/v1/settings",
		"/api/v1/library/movies",
		"/api/v1/downloads",
	} {
		rec := do(t, h, http.MethodGet, target, "")
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("GET %s = 401 with no password set; the API must stay open", target)
		}
	}
}

func TestPasswordGateAcceptsSessionCookieAndAPIKey(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	setPassword(t, st, testPassword)
	if err := st.SetSetting(ctx, store.SettingAPIKey, "deadbeef"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	cookie := login(t, h, testPassword)

	tests := []struct {
		name      string
		decorate  func(*http.Request)
		wantStatu int
	}{
		{"no credential", nil, http.StatusUnauthorized},
		{"session cookie", func(r *http.Request) { r.AddCookie(cookie) }, http.StatusOK},
		{"api key header", func(r *http.Request) { r.Header.Set("X-Api-Key", "deadbeef") }, http.StatusOK},
		{"wrong api key", func(r *http.Request) { r.Header.Set("X-Api-Key", "nope") }, http.StatusUnauthorized},
		{"stale cookie", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "not-a-session"})
		}, http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", tc.decorate)
			wantStatus(t, rec, tc.wantStatu)
			if tc.wantStatu == http.StatusUnauthorized {
				wantErrorBody(t, rec)
			}
		})
	}

	// The query-parameter form is the iCal feed's alone. The feed URL is built
	// to be pasted into Google Calendar and a housemate's phone, so honouring
	// it everywhere would make a URL stored in third-party databases a
	// credential for POST /system/shutdown and for moving the library.
	rec := do(t, h, http.MethodGet, "/api/v1/library/movies?apikey=deadbeef", "")
	wantStatus(t, rec, http.StatusUnauthorized)
	rec = do(t, h, http.MethodGet, "/api/v1/calendar.ics?apikey=deadbeef", "")
	wantStatus(t, rec, http.StatusOK)
}

func TestPasswordGateExemptions(t *testing.T) {
	stub := &stubDLNA{}
	h, st, _ := newTestServer(t, WithDLNA(stub))
	ctx := context.Background()
	setPassword(t, st, testPassword)
	if err := st.SetSetting(ctx, store.SettingAPIKey, "deadbeef"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	root := t.TempDir()
	if err := st.SetSetting(ctx, store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	writeFile(t, root, "library/Movies/poster.jpg", "jpegbytes")

	tests := []struct {
		name   string
		method string
		target string
		body   string
		want   int
	}{
		// The login screen and its assets must load before there is a session.
		{"spa index", http.MethodGet, "/", "", http.StatusOK},
		{"spa asset", http.MethodGet, "/assets/app.js", "", http.StatusOK},
		{"spa client route", http.MethodGet, "/settings", "", http.StatusOK},
		// Televisions cannot log in (SPEC §5.1).
		{"dlna device description", http.MethodGet, "/dlna/device.xml", "", http.StatusOK},
		// ...and DLNA album art is served by the image endpoint.
		{"library artwork", http.MethodGet, "/api/v1/images/library/Movies/poster.jpg", "", http.StatusOK},
		// A calendar subscription carries the API key, not a cookie.
		{"ical feed with key", http.MethodGet, "/api/v1/calendar.ics?apikey=deadbeef", "", http.StatusOK},
		// The login endpoint itself, or nobody could ever log in.
		{"login", http.MethodPost, "/api/v1/auth/login", `{"password":"wrong"}`, http.StatusUnauthorized},
		{"logout", http.MethodPost, "/api/v1/auth/logout", "", http.StatusNoContent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.target, tc.body)
			wantStatus(t, rec, tc.want)
		})
	}

	// The exemption is the path, not the endpoint's own check: the iCal feed
	// still refuses a request with no key.
	rec := do(t, h, http.MethodGet, "/api/v1/calendar.ics", "")
	wantStatus(t, rec, http.StatusUnauthorized)

	// And the image hole is only images: a media file under the same root is
	// not reachable through it.
	writeFile(t, root, "library/Movies/movie.mkv", "matroska")
	rec = do(t, h, http.MethodGet, "/api/v1/images/library/Movies/movie.mkv", "")
	wantStatus(t, rec, http.StatusNotFound)
}

func TestLoginRejectsTheWrongPassword(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", `{"password":"correct horse batteryy"}`)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantErrorBody(t, rec)
	if cookie := sessionCookieFrom(rec); cookie != nil {
		t.Fatalf("a failed login issued a session: %+v", cookie)
	}
}

func TestLoginWithoutAPasswordSetIsRefused(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", `{"password":"anything"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	if cookie := sessionCookieFrom(rec); cookie != nil {
		t.Fatal("a login against a passwordless server issued a session")
	}
}

func TestSessionCookieIsHttpOnlyAndSameSiteLax(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)

	cookie := login(t, h, testPassword)
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", cookie.SameSite)
	}
	// Secure is deliberately off: Caravan serves plain HTTP on a LAN, and a
	// Secure cookie would never come back.
	if cookie.Secure {
		t.Error("session cookie is Secure; it would never be sent back over plain HTTP")
	}
}

func TestSessionExpires(t *testing.T) {
	h, st, _ := newTestServer(t, WithSessionTTL(20*time.Millisecond))
	setPassword(t, st, testPassword)
	cookie := login(t, h, testPassword)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", func(r *http.Request) { r.AddCookie(cookie) })
	wantStatus(t, rec, http.StatusOK)

	time.Sleep(30 * time.Millisecond)

	rec = doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", func(r *http.Request) { r.AddCookie(cookie) })
	wantStatus(t, rec, http.StatusUnauthorized)
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/auth/logout", "", func(r *http.Request) { r.AddCookie(cookie) })
	wantStatus(t, rec, http.StatusNoContent)

	rec = doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", func(r *http.Request) { r.AddCookie(cookie) })
	wantStatus(t, rec, http.StatusUnauthorized)
}

func TestSetPasswordFlow(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	// Set: no current password to prove, and the API closes behind it.
	rec := do(t, h, http.MethodPost, "/api/v1/settings/password", `{"new_password":"first password"}`)
	wantStatus(t, rec, http.StatusOK)
	var got authResponse
	decodeBody(t, rec, &got)
	if !got.PasswordSet {
		t.Fatal("password_set = false after setting a password")
	}
	// The response re-issues a session, so the browser that just set the
	// password is not immediately locked out.
	setter := sessionCookieFrom(rec)
	if setter == nil {
		t.Fatal("setting a password issued no session")
	}
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/library/movies", ""), http.StatusUnauthorized)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "",
		func(r *http.Request) { r.AddCookie(setter) }), http.StatusOK)

	// The stored value is a hash, not the password.
	stored, err := st.GetSetting(ctx, store.SettingPasswordHash)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if strings.Contains(stored, "first password") || !strings.HasPrefix(stored, "$argon2id$") {
		t.Fatalf("stored password = %q, want an argon2id hash", stored)
	}

	// Change: the current password is required.
	rec = doAuth(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":"wrong","new_password":"second password"}`,
		func(r *http.Request) { r.AddCookie(setter) })
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
	// The old password still works, so the failed change changed nothing.
	login(t, h, "first password")

	rec = doAuth(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":"first password","new_password":"second password"}`,
		func(r *http.Request) { r.AddCookie(setter) })
	wantStatus(t, rec, http.StatusOK)
	changed := sessionCookieFrom(rec)
	if changed == nil {
		t.Fatal("changing the password issued no session")
	}
	// Every other session died with the old password.
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "",
		func(r *http.Request) { r.AddCookie(setter) }), http.StatusUnauthorized)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "",
		func(r *http.Request) { r.AddCookie(changed) }), http.StatusOK)
	login(t, h, "second password")

	// Clear: the API is open again, exactly as it shipped before phase 5.
	rec = doAuth(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":"second password","new_password":""}`,
		func(r *http.Request) { r.AddCookie(changed) })
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &got)
	if got.PasswordSet {
		t.Fatal("password_set = true after clearing the password")
	}
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/library/movies", ""), http.StatusOK)
}

func TestSetPasswordRejectsAShortPassword(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/settings/password", `{"new_password":"short"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

// Changing the password is inside the gate: an unauthenticated caller must not
// be able to take the server over.
func TestSetPasswordRequiresASession(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)

	rec := do(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":"`+testPassword+`","new_password":"a new password"}`)
	wantStatus(t, rec, http.StatusUnauthorized)
	// ...and the old password still works.
	login(t, h, testPassword)
}

// SPEC §12: credentials never leave the server and never reach the logs.
func TestPasswordHashNeverLeavesTheServer(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h, testPassword)
	// A failed login and a failed change are the paths most likely to log what
	// they were given.
	do(t, h, http.MethodPost, "/api/v1/auth/login", `{"password":"wrong password"}`)
	doAuth(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":"`+testPassword+`","new_password":"another password"}`,
		func(r *http.Request) { r.AddCookie(cookie) })

	hash, err := st.GetSetting(context.Background(), store.SettingPasswordHash)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}

	rec := doAuth(t, h, http.MethodGet, "/api/v1/settings", "", func(r *http.Request) { r.AddCookie(cookie) })
	wantStatus(t, rec, http.StatusUnauthorized) // the change above revoked this session
	fresh := login(t, h, "another password")
	rec = doAuth(t, h, http.MethodGet, "/api/v1/settings", "", func(r *http.Request) { r.AddCookie(fresh) })
	wantStatus(t, rec, http.StatusOK)

	var settings map[string]string
	decodeBody(t, rec, &settings)
	if _, ok := settings[store.SettingPasswordHash]; ok {
		t.Fatal("GET /settings returned the password hash")
	}
	if strings.Contains(rec.Body.String(), "$argon2id$") {
		t.Fatalf("GET /settings body contains a hash: %s", rec.Body.String())
	}

	for _, secret := range []string{hash, testPassword, "wrong password", "another password"} {
		if strings.Contains(logged.String(), secret) {
			t.Fatalf("a credential reached the logs: %s", logged.String())
		}
	}
}

// PUT /settings must not be a back door onto the password.
func TestPutSettingsRefusesThePasswordHash(t *testing.T) {
	h, st, _ := newTestServer(t)

	rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"password_hash":"$argon2id$whatever"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	if _, err := st.GetSetting(context.Background(), store.SettingPasswordHash); err == nil {
		t.Fatal("PUT /settings wrote the password hash")
	}
}

func TestStatusReportsAuthFlags(t *testing.T) {
	h, st, _ := newTestServer(t, WithListenAddr(":8677"))

	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var status statusResponse
	decodeBody(t, rec, &status)
	if status.PasswordSet {
		t.Error("password_set = true on a fresh server")
	}
	if !status.ListeningPublicly {
		t.Error("listening_publicly = false for a wildcard bind")
	}

	setPassword(t, st, testPassword)
	cookie := login(t, h, testPassword)
	rec = doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", func(r *http.Request) { r.AddCookie(cookie) })
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &status)
	if !status.PasswordSet {
		t.Error("password_set = false after setting a password")
	}
}

func TestListeningPublicly(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{":8677", true},
		{"0.0.0.0:8677", true},
		{"[::]:8677", true},
		{"192.168.1.10:8677", true},
		{"127.0.0.1:8677", false},
		{"[::1]:8677", false},
		{"localhost:8677", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := listeningPublicly(tc.addr); got != tc.want {
			t.Errorf("listeningPublicly(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestPasswordHashing(t *testing.T) {
	hash, err := hashPassword(testPassword)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if !verifyPassword(hash, testPassword) {
		t.Fatal("the right password does not verify")
	}
	if verifyPassword(hash, testPassword+"!") {
		t.Fatal("the wrong password verifies")
	}

	// Per-password salt: the same password never hashes to the same string.
	other, err := hashPassword(testPassword)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if other == hash {
		t.Fatal("two hashes of the same password are identical; the salt is not per-password")
	}

	// A hash the server cannot parse verifies nothing, rather than everything.
	for _, bad := range []string{
		"", "plaintext", "$argon2id$", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=99$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$aGFzaA",
	} {
		if verifyPassword(bad, testPassword) || verifyPassword(bad, "") {
			t.Errorf("malformed hash %q verified", bad)
		}
	}
}

func TestSessionStoreRevocation(t *testing.T) {
	sessions := newSessionStore()
	first, err := sessions.issue(time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	second, err := sessions.issue(time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if first == second {
		t.Fatal("two sessions share a token")
	}
	if !sessions.valid(first) || !sessions.valid(second) {
		t.Fatal("a fresh session is not valid")
	}
	if sessions.valid("") {
		t.Fatal("the empty token is valid")
	}

	sessions.revoke(first)
	if sessions.valid(first) {
		t.Fatal("a revoked session is still valid")
	}
	if !sessions.valid(second) {
		t.Fatal("revoking one session revoked another")
	}

	sessions.revokeAll()
	if sessions.valid(second) {
		t.Fatal("revokeAll left a session alive")
	}
}
