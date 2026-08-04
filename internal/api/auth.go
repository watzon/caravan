package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// Caravan's authentication is optional and role-aware (SPEC §11). With no
// accounts at all the API is wide open, which is the right default for a box
// on a trusted LAN; every caller then acts as an implicit admin. Once one
// account exists, every /api/v1 request needs either the session cookie a
// login issues or the API key an external tool carries, and what a session may
// reach depends on whether it belongs to an admin or a member (memberAllowed).
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
// In memory and process-wide, deliberately not per-account: a household has a
// handful of people, so a per-account counter would only give a guesser a
// fresh budget per username they invent. A restart clearing the lockout costs
// an attacker more than it costs the owner.
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

// session is one live login: whose it is, and when it stops being one.
type session struct {
	userID int64
	expiry time.Time
}

// sessionStore holds the live logins. In-memory by design (see
// defaultSessionTTL) and tiny: a household has a handful of people, so this map
// holds one entry per browser any of them has logged in from.
type sessionStore struct {
	mu     sync.Mutex
	tokens map[string]session
}

func newSessionStore() *sessionStore {
	return &sessionStore{tokens: make(map[string]session)}
}

// issue mints a 256-bit opaque token for userID, valid for ttl. The token
// carries no identity itself — the map is the only place the pairing exists —
// so a stolen cookie is worth nothing once the entry is gone.
func (s *sessionStore) issue(userID int64, ttl time.Duration) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("api: generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = session{userID: userID, expiry: time.Now().Add(ttl)}
	return token, nil
}

// valid reports whose live session token names, dropping it when it has
// expired.
//
// The lookup is a constant-time scan rather than a map hit: the map is at most
// a handful of entries, and comparing a presented credential in constant time
// is the rule here, not an optimisation to be traded away.
func (s *sessionStore) valid(token string) (int64, bool) {
	if token == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var (
		userID int64
		found  bool
	)
	for stored, sess := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(token)) != 1 {
			continue
		}
		if now.After(sess.expiry) {
			delete(s.tokens, stored)
			return 0, false
		}
		userID, found = sess.userID, true
	}
	return userID, found
}

// revoke ends one session; revoking an unknown token is not an error.
func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// revokeUser ends every session belonging to one account, and nothing else. A
// password that changed must not leave the sessions it protected alive, and a
// deleted account must not leave a live browser behind — but neither is a
// reason to sign a housemate out of the film they were browsing.
func (s *sessionStore) revokeUser(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, sess := range s.tokens {
		if sess.userID == userID {
			delete(s.tokens, token)
		}
	}
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

// requestUser is who a gated request is acting as. Handlers read it with
// currentUser; requireAuth is the only thing that writes it, so a handler can
// never be reached with an identity nobody checked.
type requestUser struct {
	// ID is the account behind the session, or 0 when there is no account:
	// an open server, or a caller holding the API key.
	ID int64
	// Role is core.RoleAdmin or core.RoleMember.
	Role string
	// Open says the server has no accounts at all and is therefore running in
	// its trusted-LAN default. It is what distinguishes "everyone is an admin
	// because nobody has signed up" from "this person is an admin".
	Open bool
	// AdultAccess is the account's adult_access grant, carried here because
	// requireAuth has already read the row and the adult gate would otherwise
	// re-read it on every request. It is meaningless without Role and without
	// the server-wide switch — read it through core.AdultVisible, never alone.
	AdultAccess bool
}

// userContextKey is the private key requestUser is stored under. It is an
// unexported empty struct type so no other package can read or forge the
// identity out of a context.
type userContextKey struct{}

// withRequestUser returns r carrying u as its identity.
func withRequestUser(r *http.Request, u requestUser) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userContextKey{}, u))
}

// currentUser is who the request is acting as. A request that never went
// through requireAuth — an exempt path, or a unit test calling a handler
// directly — reports the open implicit admin, which is what those paths behave
// as anyway.
func currentUser(r *http.Request) requestUser {
	if u, ok := r.Context().Value(userContextKey{}).(requestUser); ok {
		return u
	}
	return requestUser{Role: core.RoleAdmin, Open: true}
}

// memberAllowed reports whether a member may make this request. The path is as
// seen after the /api/v1 prefix is stripped.
//
// The philosophy is a server-side allowlist, not a denylist: everything not
// named here is 403 for a member, so a route added tomorrow is closed to
// members until somebody decides otherwise. A member's whole job is to find
// something and ask for it, which is:
//
//   - the discover screens (GET /discover, its browse pages, and one title's
//     detail), because that is where you find something;
//   - making a request, listing requests, and cancelling one — the handlers
//     narrow those last two to the member's own rows, because "who owns this"
//     is a question about the row, not about the URL;
//   - the session endpoints: who am I, log in, log out, and change my own
//     password.
//
// Deliberately absent: approving a request (that is the admin's decision, and
// approving your own request would make the whole role pointless), and every
// library, queue, wanted, calendar, settings and system route. GET /images is
// not here because it never reaches this function — it is exempt from
// authentication entirely, for televisions (see authExempt).
func memberAllowed(method, path string) bool {
	switch method + " " + path {
	case http.MethodGet + " /discover",
		http.MethodGet + " /discover/browse",
		http.MethodGet + " /requests",
		http.MethodPost + " /requests",
		http.MethodGet + " /auth/me",
		http.MethodPost + " /auth/login",
		http.MethodPost + " /auth/logout",
		// Changing your own password. It lives under /settings for historical
		// reasons — it is the one settings route a member may reach — and
		// handleSetPassword only ever touches the caller's own account.
		http.MethodPost + " /settings/password",
		// The adult module's read surface (PLAN phase 9 task 7). Naming a route
		// here does NOT grant it: requireAuth runs first and only decides that
		// a member may reach the path at all, and requireAdult then answers 404
		// unless the server-wide switch is on AND this account was granted. So
		// these three are "a member with the grant may see the Adult screens",
		// and every other /adult route stays admin-only.
		//
		// The listing is also required in the other direction: requireAuth runs
		// BEFORE requireAdult, so a granted member hitting an /adult path that
		// is not named here is turned away with the generic 403 and never
		// reaches the gate at all.
		// Deliberately absent from this list, and therefore admin-only:
		// POST /adult/sites and GET /adult/search (adding to the library is a
		// decision, and searching the provider for a site has no other use),
		// and the member-access card under /adult/users (handing out grants is
		// the admin's job, and the roster is not a member's to read).
		http.MethodGet + " /adult/sites",
		http.MethodGet + " /adult/discover":
		return true
	}

	seg := pathSegments(path)
	switch {
	// GET /discover/{type}/{id}: one title's detail screen.
	case method == http.MethodGet && len(seg) == 3 && seg[0] == "discover":
		return true
	// GET /adult/sites/{id}: one site's page.
	case method == http.MethodGet && len(seg) == 3 && seg[0] == "adult" && seg[1] == "sites":
		return true
	// DELETE /requests/{id}: cancel my request. The handler is what insists on
	// "mine" and "still pending"; the router only knows the shape.
	case method == http.MethodDelete && len(seg) == 2 && seg[0] == "requests":
		return true
	}
	return false
}

// pathSegments splits a routed path into its segments: "/requests/12" becomes
// ["requests", "12"], and "/" becomes the empty list.
func pathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
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

// requireAuth gates the JSON API and resolves who is calling. It wraps only the
// /api/v1 subtree, which is what exempts the SPA and its assets (the login
// screen has to load) and the /dlna protocol surface (televisions cannot log
// in).
//
// It is also the single place a role is enforced. A member reaching a route
// outside memberAllowed gets 403 rather than 401: 401 means "log in", and
// sending a member to the login screen for a door that will never open for
// them is a lie the SPA would loop on.
func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		user, ok, err := s.resolveUser(r)
		if err != nil {
			s.writeStoreError(w, "resolve caller", err)
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if user.Role != core.RoleAdmin && !memberAllowed(r.Method, r.URL.Path) {
			writeError(w, http.StatusForbidden, "admins only")
			return
		}
		next.ServeHTTP(w, withRequestUser(r, user))
	})
}

// resolveUser works out who is calling from the credentials on the request. It
// enforces nothing: requireAuth is what turns the answer into a 401 or a 403.
//
// It is split out because requireAuth is not the only caller that needs an
// identity. GET /images is auth-EXEMPT, for televisions, so a request to it
// never goes through the middleware and currentUser would report the implicit
// admin — which is fine for library artwork and very much not fine for the
// adult library's (see adultArtworkVisible). A surface that has to know
// whether a real credential was presented asks here rather than trusting the
// fallback identity.
//
// The three answers:
//
//   - (identity, true, nil) — a credential was presented and it is good, or
//     this Caravan has no accounts at all and everyone who reaches it is the
//     implicit admin, exactly as a passwordless server always behaved.
//   - (zero, false, nil) — no usable credential. The zero requestUser has no
//     role deliberately: it is nobody, not an admin.
//   - (_, _, err) — the store could not be asked.
func (s *server) resolveUser(r *http.Request) (requestUser, bool, error) {
	users, err := s.st.CountUsers(r.Context())
	if err != nil {
		return requestUser{}, false, err
	}
	if users == 0 {
		return requestUser{Role: core.RoleAdmin, Open: true}, true, nil
	}

	if cookie, cerr := r.Cookie(sessionCookieName); cerr == nil {
		if userID, ok := s.sessions.valid(cookie.Value); ok {
			user, err := s.st.GetUser(r.Context(), userID)
			switch {
			case errors.Is(err, store.ErrNotFound):
				// The account was deleted while this browser held a live
				// session. Tidy the rest of them away and make it log in.
				s.sessions.revokeUser(userID)
				return requestUser{}, false, nil
			case err != nil:
				return requestUser{}, false, err
			}
			return requestUser{
				ID: user.ID, Role: user.Role, AdultAccess: user.AdultAccess,
			}, true, nil
		}
	}
	// The API key is the owner's own credential, configured in the settings
	// screen and pasted into tools they chose, so it carries admin. It names no
	// account, which is why its ID is zero.
	ok, err := s.apiKeyAuthenticated(r)
	if err != nil {
		return requestUser{}, false, err
	}
	if ok {
		return requestUser{Role: core.RoleAdmin}, true, nil
	}
	return requestUser{}, false, nil
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
//
// PasswordSet means "this server has accounts and is therefore gated", which
// is the same question the SPA asked of it before roles existed.
type authResponse struct {
	PasswordSet bool `json:"password_set"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// invalidCredentials is the one thing a failed login ever says. Naming the half
// that was wrong would turn the endpoint into a list of who lives here.
const invalidCredentials = "invalid username or password"

// decoyHash is verified against when the username names nobody, so that a login
// for an account that does not exist costs the same as one for an account that
// does. Without it the reply time answers the question the message refuses to.
//
// Computed once, on the first such attempt rather than at startup: a server
// nobody guesses at should not pay 19 MiB of key derivation to boot.
var decoyHash = sync.OnceValue(func() string {
	// A hash of a value nothing can present. An error here yields the empty
	// string, which verifyPassword rejects — the wrong cost, never a wrong
	// answer.
	hash, _ := hashPassword("caravan decoy: no account has this password")
	return hash
})

// handleLogin exchanges a username and password for a session cookie.
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	// Before anything is read: the guard exists so that a burst of requests
	// cannot make this process allocate one argon2 block per request.
	if !s.logins.enter() {
		writeError(w, http.StatusTooManyRequests, "too many login attempts; wait a moment and try again")
		return
	}
	defer s.logins.leave()

	users, err := s.st.CountUsers(r.Context())
	if err != nil {
		s.writeStoreError(w, "count users", err)
		return
	}
	if users == 0 {
		writeError(w, http.StatusBadRequest, "no accounts exist on this server")
		return
	}

	user, err := s.st.GetUserByUsername(r.Context(), strings.TrimSpace(body.Username))
	switch {
	case errors.Is(err, store.ErrNotFound):
		user = &core.User{PasswordHash: decoyHash()}
	case err != nil:
		s.writeStoreError(w, "read user", err)
		return
	}

	if !verifyPassword(user.PasswordHash, body.Password) {
		// A failure nobody can see cannot be responded to (SPEC §13), so it
		// reaches the process log with the device that sent it and — for the
		// first of a burst, and every lockout — the activity feed the History
		// screen renders. The username is deliberately not logged: it is
		// attacker-supplied text, and often a real person's password typed
		// into the wrong box.
		s.log.Warn("rejected login", "remote_addr", r.RemoteAddr)
		if notable, locked := s.logins.fail(); notable {
			detail := "One or more logins were rejected. If this was not you, someone on your network is guessing."
			if locked {
				detail = fmt.Sprintf(
					"%d logins were rejected in a row, so logins are refused for %s. If this was not you, someone on your network is guessing.",
					loginFailureLimit, loginLockout)
			}
			s.logEvent(r.Context(), &core.Event{
				Level:    core.EventLevelWarn,
				Category: EventCategorySystem,
				Message:  "Failed login attempt",
				Detail:   detail,
			})
		}
		writeError(w, http.StatusUnauthorized, invalidCredentials)
		return
	}
	// A decoy verifying would be a bug in hashPassword, not a login.
	if user.ID == 0 {
		writeError(w, http.StatusUnauthorized, invalidCredentials)
		return
	}
	s.logins.succeed()

	if !s.startSession(w, user.ID) {
		return
	}
	writeJSON(w, http.StatusOK, authResponse{PasswordSet: true})
}

// meResponse is who the caller is, as GET /auth/me reports it.
type meResponse struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	// Open says the server has no accounts, so nobody logged in and everybody
	// is an admin. The SPA renders the full navigation for it, exactly as it
	// did before roles existed.
	Open bool `json:"open"`
	// Adult says the adult module is visible to this caller: the server-wide
	// switch is on AND this account reaches it (core.AdultVisible). It is what
	// the SPA renders the Adult nav item from, and it is false — not absent —
	// for everyone else, so a client cannot tell "the module is off" from "I
	// was not granted it", which is the same thing the 404 on /adult says.
	//
	// This is the only route outside /adult that reports anything about the
	// module, and it has to be: the SPA must decide what to draw before it
	// makes a request that would 404.
	Adult bool `json:"adult"`
}

// handleMe reports the calling identity. It is inside the gate and reachable by
// members: it is how the SPA learns which half of itself to render, so a
// member has to be able to ask.
//
// An open server answers with no username and the admin role, which is the
// truth: there is nobody to name, and whoever asked may do anything. So does
// the API key, for the same reason.
func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	adult, err := s.adultVisible(r)
	if err != nil {
		s.writeStoreError(w, "read adult settings", err)
		return
	}
	user := currentUser(r)
	if user.ID == 0 {
		writeJSON(w, http.StatusOK, meResponse{Role: user.Role, Open: user.Open, Adult: adult})
		return
	}
	row, err := s.st.GetUser(r.Context(), user.ID)
	if err != nil {
		s.writeStoreError(w, "read user", err)
		return
	}
	writeJSON(w, http.StatusOK, meResponse{Username: row.Username, Role: row.Role, Adult: adult})
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

// handleSetPassword changes the calling account's own password. It is the one
// settings route a member may reach, and it can only ever touch the caller:
// resetting somebody else's is POST /users/{id}/password, which is an admin's
// to make.
//
// The change revokes the account's sessions, including this request's, and then
// issues a fresh one: the browser that made the change stays logged in and
// every other browser signed in as that person is turned out. Other people's
// sessions are untouched.
func (s *server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var body passwordRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	user := currentUser(r)
	if user.ID == 0 {
		// An open server, or the API key: admin either way, but with no
		// account behind it, so there is no password of "mine" to change.
		// 400 rather than 403 — the caller has every right, there is just
		// nothing to act on.
		writeError(w, http.StatusBadRequest,
			"this request is not signed in as an account; create one with POST /users")
		return
	}
	row, err := s.st.GetUser(r.Context(), user.ID)
	if err != nil {
		s.writeStoreError(w, "read user", err)
		return
	}
	if !verifyPassword(row.PasswordHash, body.CurrentPassword) {
		// 400 rather than 401: the session that got here is valid, it is the
		// body that is wrong, and a 401 would send the SPA to the login screen
		// over a typo.
		writeError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}

	hash, ok := hashNewPassword(w, s.log, body.NewPassword)
	if !ok {
		return
	}
	if err := s.st.SetUserPassword(r.Context(), row.ID, hash); err != nil {
		s.writeStoreError(w, "write password", err)
		return
	}

	s.sessions.revokeUser(row.ID)
	if !s.startSession(w, row.ID) {
		return
	}
	writeJSON(w, http.StatusOK, authResponse{PasswordSet: true})
}

// hashNewPassword validates and hashes a password on its way into the database,
// writing the failure itself when it will not do. It is shared by every route
// that sets one — first account, own change, admin reset — so the length rule
// is stated once.
func hashNewPassword(w http.ResponseWriter, log *slog.Logger, password string) (string, bool) {
	if n := len(password); n < minPasswordLength || n > maxPasswordLength {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("password must be between %d and %d characters", minPasswordLength, maxPasswordLength))
		return "", false
	}
	hash, err := hashPassword(password)
	if err != nil {
		// The error carries no password material, but it is still only a log
		// line; the reply says nothing.
		log.Error("hash password", "error", err)
		writeError(w, http.StatusInternalServerError, "hash password")
		return "", false
	}
	return hash, true
}

// startSession issues a session for userID and sets its cookie, reporting
// whether the caller may continue.
func (s *server) startSession(w http.ResponseWriter, userID int64) bool {
	token, err := s.sessions.issue(userID, s.sessionTTL)
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
