package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// Caravan's authentication is single-user and optional (SPEC §11): with no
// password set the API is wide open, which is the right default for a box on a
// trusted LAN. Once a password exists, every /api/v1 request needs either the
// session cookie a login issues or the API key an external tool carries.
const (
	// sessionCookieName is the opaque session handle. It is HttpOnly so no
	// script can read it and SameSite=Lax so another site cannot make the
	// browser POST with it.
	//
	// Secure is deliberately NOT set: Caravan serves plain HTTP on a LAN
	// (SPEC §2, §11), and a Secure cookie is never sent back over http://, so
	// setting it would lock every user out of their own server. Anyone putting
	// Caravan on the public internet is expected to terminate TLS in front of
	// it, where the proxy can upgrade the cookie.
	sessionCookieName = "caravan_session"

	// defaultSessionTTL is how long a login lasts. Sessions live in memory
	// only: a restart logs you out, which for a single-user media box is a
	// cheaper trade than another table in a database that is meant to be
	// deletable (SPEC §7).
	defaultSessionTTL = 7 * 24 * time.Hour

	// minPasswordLength is a floor, not a policy: Caravan does not demand
	// symbols or digits. maxPasswordLength stops a caller from making the
	// server hash a megabyte.
	minPasswordLength = 8
	maxPasswordLength = 256
)

// POST /auth/login is the one gated-API route an unauthenticated caller may
// reach, and every call to it runs an argon2id derivation that allocates
// argonMemory (19 MiB). net/http imposes no handler concurrency limit, so
// without these three numbers a LAN device — or, before the same-origin guard,
// any web page — could hold hundreds of those blocks live at once and OOM a
// Raspberry Pi (SPEC §2.1).
const (
	// loginConcurrency is how many password verifications may run at once.
	// Two is generous for a single-user server and caps the derivation's
	// footprint at ~38 MiB whatever the request rate.
	loginConcurrency = 2

	// loginFailureLimit is how many rejected passwords are tolerated before
	// the endpoint stops answering for loginLockout. A human retyping a
	// password never reaches it; a dictionary run reaches it immediately and
	// is then rate-limited to loginFailureLimit guesses per loginLockout.
	loginFailureLimit = 5
	loginLockout      = time.Minute
)

// loginGuard bounds POST /auth/login: how many verifications run at once, and
// how fast wrong ones may be tried.
//
// In memory and process-wide, like the session store: Caravan is single-user,
// so there is no per-account state to keep, and a restart clearing the lockout
// costs an attacker more than it costs the owner.
type loginGuard struct {
	// slots is the concurrency cap, held for the duration of one verification.
	slots chan struct{}

	mu          sync.Mutex
	failures    int
	lockedUntil time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{slots: make(chan struct{}, loginConcurrency)}
}

// enter reserves a verification slot, reporting false when the endpoint is
// locked out or already at capacity. A false answer must be a 429 written
// without touching argon2 — the whole point is that the expensive work does not
// start.
func (g *loginGuard) enter() bool {
	g.mu.Lock()
	locked := time.Now().Before(g.lockedUntil)
	g.mu.Unlock()
	if locked {
		return false
	}
	select {
	case g.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *loginGuard) leave() { <-g.slots }

// fail records a rejected password. It reports whether the owner should be told
// — the first failure of a burst, and every lockout — so the activity feed
// carries the attack without being drowned by it.
func (g *loginGuard) fail() (notable bool, locked bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures++
	if g.failures >= loginFailureLimit {
		g.failures = 0
		g.lockedUntil = time.Now().Add(loginLockout)
		return true, true
	}
	return g.failures == 1, false
}

// succeed clears the burst. A correct password is proof the traffic was the
// owner all along.
func (g *loginGuard) succeed() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures = 0
	g.lockedUntil = time.Time{}
}

// argon2id parameters, following the OWASP baseline (19 MiB, 2 passes, 1 lane).
// They are recorded in every hash, so raising them later still verifies the
// passwords already stored.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// WithListenAddr tells the API which address the process is bound to, so
// GET /system/status can report whether Caravan is reachable from other
// machines. The UI nags about "listening on all interfaces without a password"
// from that flag (SPEC §11). An unset address reports "not public": a wrong
// nag is worse than a missing one.
func WithListenAddr(addr string) Option {
	return func(s *server) { s.listenAddr = addr }
}

// WithSessionTTL overrides how long a login lasts.
func WithSessionTTL(d time.Duration) Option {
	return func(s *server) { s.sessionTTL = d }
}

// sessionStore holds the live logins. In-memory by design (see
// defaultSessionTTL) and tiny: Caravan is single-user, so this map holds one
// entry per browser the owner has logged in from.
type sessionStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{tokens: make(map[string]time.Time)}
}

// issue mints a 256-bit opaque token valid for ttl.
func (s *sessionStore) issue(ttl time.Duration) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("api: generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = time.Now().Add(ttl)
	return token, nil
}

// valid reports whether token names a live session, dropping it when it has
// expired.
//
// The lookup is a constant-time scan rather than a map hit: the map is at most
// a handful of entries, and comparing a presented credential in constant time
// is the rule here, not an optimisation to be traded away.
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	found := false
	for stored, expiry := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(token)) != 1 {
			continue
		}
		if now.After(expiry) {
			delete(s.tokens, stored)
			return false
		}
		found = true
	}
	return found
}

// revoke ends one session; revoking an unknown token is not an error.
func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// revokeAll ends every session. A password that changed must not leave the
// sessions it protected alive.
func (s *sessionStore) revokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.tokens)
}

// hashPassword returns an argon2id PHC string. The salt is per-password, so
// two identical passwords never share a hash.
func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("api: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword checks password against an encoded hash. Cost parameters are
// read back out of the hash, so hashes written by an older parameter set keep
// verifying. A malformed hash verifies nothing rather than everything.
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	if memory == 0 || timeCost == 0 || threads == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// passwordHash reads the stored hash. An absent key and an empty value mean
// the same thing: no password.
func (s *server) passwordHash(ctx context.Context) (string, error) {
	hash, err := s.st.GetSetting(ctx, store.SettingPasswordHash)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	return hash, nil
}

// authExempt reports whether a path inside /api/v1 is reachable without
// credentials. The paths are as seen after the /api/v1 prefix is stripped.
//
// Three holes, each deliberate:
//
//   - /auth/login and /auth/logout, because a login that needed a session
//     could never happen.
//   - /calendar.ics, which authenticates itself with the API key: it is
//     subscribed to by calendar apps that cannot hold a cookie.
//   - /images/, because the DLNA server hands televisions this URL for album
//     art (internal/dlna) and a TV cannot log in. The hole is narrow: the
//     handler serves only image files (jpg/jpeg/png/webp) that already exist
//     under the storage root, so what leaks is library artwork — never a media
//     file, never a path outside the root.
func authExempt(path string) bool {
	switch path {
	case "/auth/login", "/auth/logout", "/calendar.ics":
		return true
	}
	return strings.HasPrefix(path, "/images/")
}

// requireAuth gates the JSON API. It wraps only the /api/v1 subtree, which is
// what exempts the SPA and its assets (the login screen has to load) and the
// /dlna protocol surface (televisions cannot log in).
func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		hash, err := s.passwordHash(r.Context())
		if err != nil {
			s.writeStoreError(w, "read password", err)
			return
		}
		// No password set: Caravan behaves exactly as it did before phase 5.
		if hash == "" {
			next.ServeHTTP(w, r)
			return
		}

		if cookie, err := r.Cookie(sessionCookieName); err == nil && s.sessions.valid(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}
		ok, err := s.apiKeyAuthenticated(r)
		if err != nil {
			s.writeStoreError(w, "read api key", err)
			return
		}
		if ok {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized")
	})
}

// apiKeyAuthenticated reports whether the request carries the configured API
// key in the X-Api-Key header. That header is the only form the gated API
// accepts.
//
// The ?apikey= query form is deliberately NOT accepted here. It exists for one
// caller — the iCal feed, whose subscribers cannot set a header — and a URL
// handed to Google Calendar or a housemate's phone is stored in third-party
// databases, browser history and Referer headers. Honouring it on every route
// would make that shared URL a credential for POST /system/shutdown and
// POST /system/storage-root/migrate. See calendarKeyAuthenticated.
func (s *server) apiKeyAuthenticated(r *http.Request) (bool, error) {
	return s.apiKeyMatches(r, r.Header.Get("X-Api-Key"))
}

// calendarKeyAuthenticated is the iCal feed's own check: the header, or the
// query parameter a calendar subscription is forced to use. Nothing else in the
// API reaches it, so the feed URL unlocks the feed and nothing more.
func (s *server) calendarKeyAuthenticated(r *http.Request) (bool, error) {
	switch ok, err := s.apiKeyAuthenticated(r); {
	case err != nil:
		return false, err
	case ok:
		return true, nil
	}
	return s.apiKeyMatches(r, r.URL.Query().Get("apikey"))
}

// apiKeyMatches compares a presented credential with the stored API key in
// constant time. An unset key matches nothing.
func (s *server) apiKeyMatches(r *http.Request, presented string) (bool, error) {
	if presented == "" {
		return false, nil
	}
	key, err := s.st.GetSetting(r.Context(), store.SettingAPIKey)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if key == "" {
		return false, nil
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(key)) == 1, nil
}

// authResponse is what the auth endpoints answer with. It carries no
// credential: the session is in the cookie and the password never comes back.
type authResponse struct {
	PasswordSet bool `json:"password_set"`
}

type loginRequest struct {
	Password string `json:"password"`
}

// handleLogin exchanges the password for a session cookie.
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	// Before the hash is even read: the guard exists so that a burst of
	// requests cannot make this process allocate one argon2 block per request.
	if !s.logins.enter() {
		writeError(w, http.StatusTooManyRequests, "too many login attempts; wait a moment and try again")
		return
	}
	defer s.logins.leave()

	hash, err := s.passwordHash(r.Context())
	if err != nil {
		s.writeStoreError(w, "read password", err)
		return
	}
	if hash == "" {
		writeError(w, http.StatusBadRequest, "no password is set")
		return
	}
	if !verifyPassword(hash, body.Password) {
		// A failure nobody can see cannot be responded to (SPEC §13), so it
		// reaches the process log with the device that sent it and — for the
		// first of a burst, and every lockout — the activity feed the History
		// screen renders.
		s.log.Warn("rejected login", "remote_addr", r.RemoteAddr)
		if notable, locked := s.logins.fail(); notable {
			detail := "One or more passwords were rejected. If this was not you, someone on your network is guessing."
			if locked {
				detail = fmt.Sprintf(
					"%d passwords were rejected in a row, so logins are refused for %s. If this was not you, someone on your network is guessing.",
					loginFailureLimit, loginLockout)
			}
			s.logEvent(r.Context(), &core.Event{
				Level:    core.EventLevelWarn,
				Category: EventCategorySystem,
				Message:  "Failed login attempt",
				Detail:   detail,
			})
		}
		// Deliberately says nothing about which half was wrong; there is only
		// one account, so there is nothing to enumerate either.
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	s.logins.succeed()

	if !s.startSession(w) {
		return
	}
	writeJSON(w, http.StatusOK, authResponse{PasswordSet: true})
}

// handleLogout invalidates the presented session. It is exempt from the
// middleware so an expired cookie can still be cleared; with no valid token
// there is nothing to revoke and the reply is the same 204.
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.revoke(cookie.Value)
	}
	http.SetCookie(w, clearedSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleSetPassword sets, changes or clears the password. An empty new_password
// clears it, which puts the server back in its open default.
//
// Changing the password revokes every session, including this request's, and
// then issues a fresh one: the browser that made the change stays logged in and
// every other one is turned out.
func (s *server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var body passwordRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	current, err := s.passwordHash(r.Context())
	if err != nil {
		s.writeStoreError(w, "read password", err)
		return
	}
	if current != "" && !verifyPassword(current, body.CurrentPassword) {
		// 400 rather than 401: the session that got here is valid, it is the
		// body that is wrong, and a 401 would send the SPA to the login screen
		// over a typo.
		writeError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}

	if body.NewPassword == "" {
		if err := s.st.DeleteSetting(r.Context(), store.SettingPasswordHash); err != nil {
			s.writeStoreError(w, "clear password", err)
			return
		}
		s.sessions.revokeAll()
		http.SetCookie(w, clearedSessionCookie())
		writeJSON(w, http.StatusOK, authResponse{PasswordSet: false})
		return
	}

	if n := len(body.NewPassword); n < minPasswordLength || n > maxPasswordLength {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("password must be between %d and %d characters", minPasswordLength, maxPasswordLength))
		return
	}
	hash, err := hashPassword(body.NewPassword)
	if err != nil {
		// The error carries no password material, but it is still only a log
		// line; the reply says nothing.
		s.log.Error("hash password", "error", err)
		writeError(w, http.StatusInternalServerError, "hash password")
		return
	}
	if err := s.st.SetSetting(r.Context(), store.SettingPasswordHash, hash); err != nil {
		s.writeStoreError(w, "write password", err)
		return
	}

	s.sessions.revokeAll()
	if !s.startSession(w) {
		return
	}
	writeJSON(w, http.StatusOK, authResponse{PasswordSet: true})
}

// startSession issues a session and sets its cookie, reporting whether the
// caller may continue.
func (s *server) startSession(w http.ResponseWriter) bool {
	token, err := s.sessions.issue(s.sessionTTL)
	if err != nil {
		s.log.Error("issue session", "error", err)
		writeError(w, http.StatusInternalServerError, "issue session")
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.sessionTTL.Seconds()),
	})
	return true
}

func clearedSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// listeningPublicly reports whether addr is reachable from other machines,
// which is what makes a missing password worth nagging about.
//
// Unparseable or hostname-based addresses report false: the nag is a warning
// about a known-bad combination, and guessing would train the user to dismiss
// it.
func listeningPublicly(addr string) bool {
	if addr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	// ":8677" — every interface.
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsUnspecified() {
		return true
	}
	return !ip.IsLoopback()
}
