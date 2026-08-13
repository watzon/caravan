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

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

const (
	testPassword = "correct horse battery"
	testAdmin    = "admin"
	testMember   = "housemate"
)

// createUser writes an account directly, the way a server that already has
// accounts starts up.
func createUser(t *testing.T, st *store.Store, username, password, role string) *core.User {
	t.Helper()
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	u := &core.User{Username: username, PasswordHash: hash, Role: role}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser(%q): %v", username, err)
	}
	return u
}

// setPassword closes an open server the way it used to be closed: one admin,
// named "admin", with this password.
func setPassword(t *testing.T, st *store.Store, password string) *core.User {
	t.Helper()
	return createUser(t, st, testAdmin, password, core.RoleAdmin)
}

// login exchanges a username and password for a session cookie, failing the
// test when the login itself fails.
func login(t *testing.T, h http.Handler, username, password string) *http.Cookie {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":`+quote(username)+`,"password":`+quote(password)+`}`)
	wantStatus(t, rec, http.StatusOK)
	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("login set no session cookie")
	}
	return cookie
}

// withCookie decorates a request with a session cookie, which is what most of
// the tests below need from doAuth.
func withCookie(c *http.Cookie) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(c) }
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

// The pre-RBAC contract: with no accounts, nothing changes. This is the
// regression that keeps the gate from becoming mandatory by accident — an
// upgrade must not lock a household out of its own media box.
func TestNoUsersLeavesTheAPIOpen(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, target := range []string{
		"/api/v1/system/status",
		"/api/v1/settings",
		"/api/v1/library/movies",
		"/api/v1/downloads",
		// Admin-only once accounts exist; open here, like everything else.
		"/api/v1/users",
	} {
		rec := do(t, h, http.MethodGet, target, "")
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Fatalf("GET %s = %d with no accounts; the API must stay open", target, rec.Code)
		}
	}

	// And the caller is an implicit admin, not a nobody — holding every seeded
	// shelf, because an open install hides nothing from anybody.
	rec := do(t, h, http.MethodGet, "/api/v1/auth/me", "")
	wantStatus(t, rec, http.StatusOK)
	var me meResponse
	decodeBody(t, rec, &me)
	if me.Username != "" || me.Role != core.RoleAdmin || !me.Open || me.Adult {
		t.Fatalf("auth/me on an open server = %+v, want an anonymous open admin", me)
	}
	kinds := map[string]bool{}
	for _, l := range me.Libraries {
		kinds[l.Kind] = true
	}
	if !kinds[core.LibraryKindMovie] || !kinds[core.LibraryKindTV] {
		t.Fatalf("open admin sees %+v, want both seeded libraries", me.Libraries)
	}
}

func TestPasswordGateAcceptsSessionCookieAndAPIKey(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	setPassword(t, st, testPassword)
	if err := st.SetSetting(ctx, store.SettingAPIKey, "deadbeef"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	cookie := login(t, h, testAdmin, testPassword)

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
		{"login", http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"wrong"}`, http.StatusUnauthorized},
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

// A rejected login must not say which half was wrong: a message that
// distinguishes "no such user" from "wrong password" is a list of who lives
// here, handed out one guess at a time.
func TestLoginRejectionsAreIndistinguishable(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)

	tests := []struct {
		name string
		body string
	}{
		{"wrong password", `{"username":"admin","password":"correct horse batteryy"}`},
		{"unknown username", `{"username":"nobody","password":"correct horse battery"}`},
		{"no username at all", `{"password":"correct horse battery"}`},
	}
	var messages []string
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/auth/login", tc.body)
			wantStatus(t, rec, http.StatusUnauthorized)
			wantErrorBody(t, rec)
			if cookie := sessionCookieFrom(rec); cookie != nil {
				t.Fatalf("a failed login issued a session: %+v", cookie)
			}
			var body errorResponse
			decodeBody(t, rec, &body)
			messages = append(messages, body.Error)
		})
	}
	for _, got := range messages {
		if got != messages[0] {
			t.Fatalf("failure messages differ (%q vs %q); the reply names which half was wrong",
				messages[0], got)
		}
	}
	if messages[0] != invalidCredentials {
		t.Fatalf("failure message = %q, want %q", messages[0], invalidCredentials)
	}
}

// Capitalising your own name is not a wrong password.
func TestLoginIsCaseInsensitiveOnTheUsername(t *testing.T) {
	h, st, _ := newTestServer(t)
	createUser(t, st, "Chris", testPassword, core.RoleAdmin)

	login(t, h, "chris", testPassword)
	login(t, h, "CHRIS", testPassword)
}

func TestLoginWithoutAnyAccountsIsRefused(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"anything"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	if cookie := sessionCookieFrom(rec); cookie != nil {
		t.Fatal("a login against an open server issued a session")
	}
}

func TestSessionCookieIsHttpOnlyAndSameSiteLax(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)

	cookie := login(t, h, testAdmin, testPassword)
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
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	time.Sleep(30 * time.Millisecond)

	rec = doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusUnauthorized)
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/auth/logout", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusNoContent)

	rec = doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusUnauthorized)
}

// POST /settings/password changes the caller's own password and nobody else's,
// and turns out only their own other browsers.
func TestSetPasswordChangesOnlyTheCallersOwnAccount(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	admin := setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)

	// Two browsers signed in as the admin, and one as the housemate.
	first := login(t, h, testAdmin, testPassword)
	second := login(t, h, testAdmin, testPassword)
	member := login(t, h, testMember, testPassword)

	// The current password is required.
	rec := doAuth(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":"wrong","new_password":"second password"}`, withCookie(first))
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
	// The old password still works, so the failed change changed nothing.
	login(t, h, testAdmin, testPassword)

	rec = doAuth(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":`+quote(testPassword)+`,"new_password":"second password"}`,
		withCookie(first))
	wantStatus(t, rec, http.StatusOK)
	var got authResponse
	decodeBody(t, rec, &got)
	if !got.PasswordSet {
		t.Fatal("password_set = false after changing a password")
	}
	changed := sessionCookieFrom(rec)
	if changed == nil {
		t.Fatal("changing the password issued no session")
	}

	// The browser that made the change stays logged in; the admin's other
	// browser is turned out.
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", withCookie(changed)),
		http.StatusOK)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", withCookie(second)),
		http.StatusUnauthorized)
	// The housemate never signed anything and is still signed in.
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(member)),
		http.StatusOK)
	login(t, h, testAdmin, "second password")
	login(t, h, testMember, testPassword)

	// The stored value is a hash, not the password.
	stored, err := st.GetUser(ctx, admin.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if strings.Contains(stored.PasswordHash, "second password") ||
		!strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("stored password = %q, want an argon2id hash", stored.PasswordHash)
	}
}

func TestSetPasswordRejectsAShortPassword(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":`+quote(testPassword)+`,"new_password":"short"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

// An open server has no account to change: whoever is calling is an admin, but
// an anonymous one. Refusing here is what keeps this route "change MY password"
// rather than a second, unguarded way to create the first user.
func TestSetPasswordOnAnOpenServerHasNothingToChange(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/settings/password", `{"new_password":"first password"}`)
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
	login(t, h, testAdmin, testPassword)
}

// SPEC §12: credentials never leave the server and never reach the logs.
func TestPasswordHashNeverLeavesTheServer(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	h, st, _ := newTestServer(t)
	admin := setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)
	// A failed login and a failed change are the paths most likely to log what
	// they were given.
	do(t, h, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"wrong password"}`)
	doAuth(t, h, http.MethodPost, "/api/v1/settings/password",
		`{"current_password":"`+testPassword+`","new_password":"another password"}`,
		withCookie(cookie))

	stored, err := st.GetUser(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	rec := doAuth(t, h, http.MethodGet, "/api/v1/settings", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusUnauthorized) // the change above revoked this session
	fresh := login(t, h, testAdmin, "another password")
	rec = doAuth(t, h, http.MethodGet, "/api/v1/settings", "", withCookie(fresh))
	wantStatus(t, rec, http.StatusOK)

	var settings map[string]string
	decodeBody(t, rec, &settings)
	if _, ok := settings[store.SettingPasswordHash]; ok {
		t.Fatal("GET /settings returned the password hash")
	}
	if strings.Contains(rec.Body.String(), "$argon2id$") {
		t.Fatalf("GET /settings body contains a hash: %s", rec.Body.String())
	}

	// Nor does the account listing, which is the new place a hash could leak.
	rec = doAuth(t, h, http.MethodGet, "/api/v1/users", "", withCookie(fresh))
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "$argon2id$") {
		t.Fatalf("GET /users body contains a hash: %s", rec.Body.String())
	}

	for _, secret := range []string{stored.PasswordHash, testPassword, "wrong password", "another password"} {
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
	cookie := login(t, h, testAdmin, testPassword)
	rec = doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &status)
	if !status.PasswordSet {
		t.Error("password_set = false once an account exists")
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
	// Two browsers for account 7, one for account 9.
	first, err := sessions.issue(7, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	second, err := sessions.issue(7, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	other, err := sessions.issue(9, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if first == second || first == other {
		t.Fatal("two sessions share a token")
	}

	// A live session reports whose it is, which is the whole point of the map.
	if userID, ok, err := sessions.valid(first); err != nil || !ok || userID != 7 {
		t.Fatalf("valid(first) = %d, %v, %v; want 7, true, nil", userID, ok, err)
	}
	if userID, ok, err := sessions.valid(other); err != nil || !ok || userID != 9 {
		t.Fatalf("valid(other) = %d, %v, %v; want 9, true, nil", userID, ok, err)
	}
	if _, ok, err := sessions.valid(""); err != nil || ok {
		t.Fatalf("the empty token is valid: ok=%v err=%v", ok, err)
	}

	sessions.revoke(first)
	if _, ok, err := sessions.valid(first); err != nil || ok {
		t.Fatalf("a revoked session is still valid: ok=%v err=%v", ok, err)
	}
	if _, ok, err := sessions.valid(second); err != nil || !ok {
		t.Fatalf("revoking one session revoked another: ok=%v err=%v", ok, err)
	}

	// revokeUser is per-account: everything of account 7's goes, and account
	// 9 never notices.
	if err := sessions.revokeUser(7); err != nil {
		t.Fatalf("revokeUser: %v", err)
	}
	if _, ok, err := sessions.valid(second); err != nil || ok {
		t.Fatalf("revokeUser left the account's other session alive: ok=%v err=%v", ok, err)
	}
	if _, ok, err := sessions.valid(other); err != nil || !ok {
		t.Fatalf("revokeUser ended somebody else's session: ok=%v err=%v", ok, err)
	}
}

func TestSessionSurvivesServerRestart(t *testing.T) {
	h1, st, mgr := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h1, testAdmin, testPassword)

	// A new process is a new handler over the same database, the way Air
	// rebuilds `caravan serve` without wiping the store.
	h2 := NewServer(st, mgr, nil)
	rec := doAuth(t, h2, http.MethodGet, "/api/v1/auth/me", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	// Logout on the new process must revoke the durable row, not just the
	// empty in-memory cache of a freshly started server.
	wantStatus(t, doAuth(t, h2, http.MethodPost, "/api/v1/auth/logout", "", withCookie(cookie)), http.StatusNoContent)
	h3 := NewServer(st, mgr, nil)
	wantStatus(t, doAuth(t, h3, http.MethodGet, "/api/v1/auth/me", "", withCookie(cookie)), http.StatusUnauthorized)
}

func TestPersistedSessionExpires(t *testing.T) {
	h1, st, mgr := newTestServer(t, WithSessionTTL(time.Millisecond))
	setPassword(t, st, testPassword)
	cookie := login(t, h1, testAdmin, testPassword)
	time.Sleep(5 * time.Millisecond)

	h2 := NewServer(st, mgr, nil, WithSessionTTL(time.Millisecond))
	wantStatus(t, doAuth(t, h2, http.MethodGet, "/api/v1/auth/me", "", withCookie(cookie)), http.StatusUnauthorized)
}

// The allowlist itself, stated as a table so the boundary is readable in one
// place. Everything not listed is closed to members: that is the point of an
// allowlist, and this table is what would fail if a route quietly joined it.
func TestMemberAllowlist(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   bool
	}{
		// Finding something.
		{http.MethodGet, "/discover", true},
		{http.MethodGet, "/discover/browse", true},
		{http.MethodGet, "/discover/movie/27205", true},
		{http.MethodGet, "/discover/series/1396", true},
		// The filtered scopes and the controls that drive them (PLAN phase 12).
		{http.MethodGet, "/discover/movies", true},
		{http.MethodGet, "/discover/series", true},
		{http.MethodGet, "/discover/people", true},
		{http.MethodGet, "/discover/companies", true},
		{http.MethodGet, "/discover/keywords", true},
		{http.MethodGet, "/discover/genres", true},
		// Asking for it, and taking the ask back.
		{http.MethodPost, "/requests", true},
		{http.MethodGet, "/requests", true},
		{http.MethodDelete, "/requests/12", true},
		// Approving it is somebody else's decision.
		{http.MethodPost, "/requests/12/approve", false},
		// The session.
		{http.MethodGet, "/auth/me", true},
		{http.MethodPost, "/auth/login", true},
		{http.MethodPost, "/auth/logout", true},
		{http.MethodPost, "/settings/password", true},
		// Running the box.
		{http.MethodGet, "/settings", false},
		{http.MethodPut, "/settings", false},
		{http.MethodPost, "/settings/apikey", false},
		{http.MethodGet, "/system/status", false},
		{http.MethodPost, "/system/shutdown", false},
		{http.MethodGet, "/library/movies", false},
		{http.MethodPost, "/library/movies", false},
		{http.MethodGet, "/library/series", false},
		{http.MethodPost, "/library/rescan", false},
		{http.MethodGet, "/wanted", false},
		{http.MethodGet, "/calendar", false},
		{http.MethodGet, "/downloads", false},
		{http.MethodGet, "/import/queue", false},
		{http.MethodGet, "/jobs", false},
		{http.MethodGet, "/events", false},
		{http.MethodGet, "/search", false},
		{http.MethodGet, "/indexers", false},
		{http.MethodGet, "/users", false},
		{http.MethodPost, "/users", false},
		{http.MethodDelete, "/users/2", false},
		{http.MethodPost, "/users/2/password", false},
		// Right shape, wrong method.
		{http.MethodPost, "/discover", false},
		{http.MethodDelete, "/discover/movie/27205", false},
		{http.MethodPut, "/requests/12", false},
		{http.MethodDelete, "/requests", false},
	}
	for _, tc := range tests {
		if got := memberAllowed(tc.method, tc.path); got != tc.want {
			t.Errorf("memberAllowed(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// A member turned away gets 403, not 401. A 401 means "log in", and sending a
// member to the login screen for a door that will never open for them is a lie
// the SPA would loop on.
func TestMemberIsForbiddenNotUnauthorized(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	member := login(t, h, testMember, testPassword)
	admin := login(t, h, testAdmin, testPassword)

	for _, target := range []string{
		"/api/v1/settings",
		"/api/v1/system/status",
		"/api/v1/library/movies",
		"/api/v1/wanted",
		"/api/v1/downloads",
		"/api/v1/events",
		"/api/v1/users",
	} {
		rec := doAuth(t, h, http.MethodGet, target, "", withCookie(member))
		wantStatus(t, rec, http.StatusForbidden)
		wantErrorBody(t, rec)
		// The same route, for the admin, is not forbidden.
		rec = doAuth(t, h, http.MethodGet, target, "", withCookie(admin))
		if rec.Code == http.StatusForbidden {
			t.Fatalf("GET %s = 403 for an admin", target)
		}
	}

	// And what a member may reach, they reach.
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(member)),
		http.StatusOK)
	// Discover needs a metadata provider this server has none of, so it
	// answers 503 — but it is the handler answering, which is the point: the
	// gate let the member through.
	if rec := doAuth(t, h, http.MethodGet, "/api/v1/discover", "", withCookie(member)); rec.Code == http.StatusForbidden {
		t.Fatal("GET /discover = 403 for a member; discover is what a member is for")
	}
}

func TestMeReportsTheCallingIdentity(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	if err := st.SetSetting(ctx, store.SettingAPIKey, "deadbeef"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	tests := []struct {
		name     string
		decorate func(*http.Request)
		// wantUsername and wantRole are the whole identity. Libraries is
		// asserted below rather than in the table: every identity here sees the
		// two seeded shelves, and repeating them per row would only make the
		// table say the same thing three times.
		wantUsername string
		wantRole     string
	}{
		{"admin session", withCookie(login(t, h, testAdmin, testPassword)),
			testAdmin, core.RoleAdmin},
		{"member session", withCookie(login(t, h, testMember, testPassword)),
			testMember, core.RoleMember},
		// The API key is the owner's own credential, so it is an admin — but
		// it names no account, so there is nobody to report.
		{"api key", func(r *http.Request) { r.Header.Set("X-Api-Key", "deadbeef") },
			"", core.RoleAdmin},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doAuth(t, h, http.MethodGet, "/api/v1/auth/me", "", tc.decorate)
			wantStatus(t, rec, http.StatusOK)
			var got meResponse
			decodeBody(t, rec, &got)
			if got.Username != tc.wantUsername || got.Role != tc.wantRole ||
				got.Open || got.Adult || got.SceneFilters != nil {
				t.Fatalf("auth/me = %+v, want %q as %q and nothing adult",
					got, tc.wantUsername, tc.wantRole)
			}
			if len(got.Libraries) != 2 {
				t.Fatalf("auth/me libraries = %+v, want both seeded shelves", got.Libraries)
			}
		})
	}

	// With no credential at all it is 401 like everything else inside the gate.
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/auth/me", ""), http.StatusUnauthorized)
}

// A session whose account was deleted underneath it is not a session. This is
// the one path where the cookie is valid and the identity is not.
func TestSessionForADeletedAccountIsRefused(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testMember, testPassword)

	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(cookie)),
		http.StatusOK)

	if err := st.DeleteUser(context.Background(), member.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	rec := doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusUnauthorized)
	wantErrorBody(t, rec)
}
