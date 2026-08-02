package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// The regressions in this file are the attacks from the phase-5 security
// review, written as the attacker would send them.

// A page on evil.com auto-submits a form at the LAN box. enctype="text/plain"
// makes it a CORS simple request: no preflight, and the attacker never needs to
// read the reply. Before the same-origin guard this queued a storage migration
// that physically moved the library off the drive, and the bodiless variant
// against /system/shutdown stopped the server outright — on the passwordless
// default the Docker image ships.
func TestCrossSiteFormPostIsRefused(t *testing.T) {
	h, st, _ := newTestServer(t)
	root := t.TempDir()
	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	// The form field is *named* with the JSON body and has an empty value;
	// text/plain encoding sends it as `{"root":"/data/attacker"}=`, and
	// json.Decoder ignores everything after the first value.
	forged := func(method, target, body string) *http.Request {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, target, nil)
		} else {
			r = httptest.NewRequest(method, target, strings.NewReader(body))
			r.Header.Set("Content-Type", "text/plain;charset=UTF-8")
		}
		r.Header.Set("Origin", "http://evil.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		return r
	}

	for _, tc := range []struct{ name, method, target, body string }{
		{"migrate the library away", http.MethodPost, "/api/v1/system/storage-root/migrate",
			`{"root":"/data/attacker"}=` + "\r\n"},
		{"repoint the library", http.MethodPost, "/api/v1/system/storage-root/repoint",
			`{"root":"/data/attacker"}=` + "\r\n"},
		{"stop the server", http.MethodPost, "/api/v1/system/shutdown", ""},
		{"lock the owner out", http.MethodPost, "/api/v1/settings/password",
			`{"current_password":"","new_password":"attackerpassword"}=` + "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, forged(tc.method, tc.target, tc.body))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body %q)", rec.Code, rec.Body.String())
			}
		})
	}

	// Nothing landed: no password was set and the storage root is untouched.
	got, err := st.GetSetting(context.Background(), store.SettingStorageRoot)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != root {
		t.Fatalf("storage_root = %q, want %q", got, root)
	}
	hash, err := st.GetSetting(context.Background(), store.SettingPasswordHash)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetSetting: %v", err)
	}
	if hash != "" {
		t.Fatal("a cross-site form set the password and locked the owner out")
	}
}

// The SPA's own requests carry same-origin fetch metadata, and a non-browser
// caller (curl, a script with the API key) carries neither header. Both must
// still work — a CSRF defence that breaks them is not shippable.
func TestSameOriginAndNonBrowserRequestsStillWork(t *testing.T) {
	h, _, _ := newTestServer(t)

	spa := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"tmdb_api_key":"k"}`))
	spa.Header.Set("Content-Type", contentTypeJSON)
	spa.Header.Set("Origin", "http://"+spa.Host)
	spa.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, spa)
	wantStatus(t, rec, http.StatusOK)

	// No Origin, no fetch metadata: a script, not a browser.
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"tmdb_api_key":"k2"}`)
	wantStatus(t, rec, http.StatusOK)
}

// A form content type is never a legitimate API body, and it is the encoding
// that lets a cross-site form smuggle JSON past a decoder that ignores trailing
// bytes. Refusing it is the second lock on the same door.
func TestFormEncodedBodiesAreRefused(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"tmdb_api_key":"k"}=`))
		r.Header.Set("Content-Type", ct)
		r.Header.Set("Origin", "http://"+r.Host)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("Content-Type %q: status = %d, want 415 (body %q)", ct, rec.Code, rec.Body.String())
		}
	}
}

// storage_root has rules attached — absolute, exists, is a folder, and not
// while a migration owns both roots. The generic settings PUT enforced none of
// them, so a stale tab or a script could flip the root mid-move; the library
// then read as entirely missing and whichever way the job ended it clobbered
// the value the user had written.
func TestPutSettingsCannotWriteTheStorageRoot(t *testing.T) {
	h, st, _ := newTestServer(t)
	root := t.TempDir()
	ctx := context.Background()
	if err := st.SetSetting(ctx, store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"storage_root":"/tmp/typo"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"storage_root":"relative/path"}`)
	wantStatus(t, rec, http.StatusBadRequest)

	got, err := st.GetSetting(ctx, store.SettingStorageRoot)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != root {
		t.Fatalf("storage_root = %q, want %q — PUT /settings wrote it anyway", got, root)
	}

	// The repoint endpoint is the way in, and it validates.
	rec = do(t, h, http.MethodPost, "/api/v1/system/storage-root/repoint", `{"root":"relative/path"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	rec = do(t, h, http.MethodPost, "/api/v1/system/storage-root/repoint", `{"root":`+quote(t.TempDir())+`}`)
	wantStatus(t, rec, http.StatusOK)
}

// Every /auth/login runs a 19 MiB argon2id derivation, and net/http caps
// handler concurrency at nothing. A burst of concurrent logins used to hold one
// of those blocks live per request, which OOM-kills a Pi-class box — the exact
// hardware SPEC §2.1 targets, and an unclean shutdown that leaves the marker
// dirty and downloads refusing to resume.
func TestLoginIsBoundedAndAudited(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)

	const attempts = 40
	var wg sync.WaitGroup
	codes := make([]int, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := do(t, h, http.MethodPost, "/api/v1/auth/login", `{"password":"rockyou"}`)
			codes[i] = rec.Code
		}()
	}
	wg.Wait()

	var refused int
	for _, code := range codes {
		switch code {
		case http.StatusUnauthorized:
		case http.StatusTooManyRequests:
			refused++
		default:
			t.Fatalf("login answered %d, want 401 or 429", code)
		}
	}
	if refused == 0 {
		t.Fatalf("all %d concurrent guesses ran an argon2 derivation; nothing bounded the burst", attempts)
	}

	// And the owner can see it happened: a failure nobody can see cannot be
	// responded to (SPEC §13).
	events, err := st.ListEvents(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Category == EventCategorySystem && strings.Contains(e.Message, "Failed login") {
			found = true
			if e.Level != core.EventLevelWarn {
				t.Fatalf("failed-login event level = %q, want %q", e.Level, core.EventLevelWarn)
			}
		}
	}
	if !found {
		t.Fatal("a dictionary run against the password left no trace in the activity feed")
	}
}

// A dirty start and a half-finished migration turn up together: the crash that
// set the flag is the crash that stopped the move. The recovery banner's one
// button rescans, and a rescan deletes the media_file row of every path that is
// no longer under the root — which, mid-move, is every file the mover has
// already taken, artwork references included.
func TestVerifyAndRescanRefuseWhileAMigrationIsOpen(t *testing.T) {
	h, st, mgr := newTestServer(t, WithDirtyStart(true))
	ctx := context.Background()
	if err := st.SetSetting(ctx, store.SettingStorageRoot, t.TempDir()); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	m := &core.StorageMigration{
		SourceRoot: t.TempDir(),
		TargetRoot: t.TempDir(),
		Status:     core.StorageMigrationRunning,
	}
	if err := st.CreateStorageMigration(ctx, m); err != nil {
		t.Fatalf("CreateStorageMigration: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/system/verify", "")
	wantStatus(t, rec, http.StatusConflict)
	rec = do(t, h, http.MethodPost, "/api/v1/library/rescan", "")
	wantStatus(t, rec, http.StatusConflict)

	if n := mgr.scanCount.Load(); n != 0 {
		t.Fatalf("%d scans ran while the library's files were mid-move", n)
	}

	// Once the move resolves, recovery works exactly as before.
	m.Status = core.StorageMigrationDone
	if err := st.UpdateStorageMigration(ctx, m); err != nil {
		t.Fatalf("UpdateStorageMigration: %v", err)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/system/verify", "")
	wantStatus(t, rec, http.StatusOK)
}

// SPEC §10.1 step 2 — "point Caravan at existing media, with a library scan
// queued immediately" — is unreachable in Docker and on a portable drive,
// because both bring a storage root with them and the SPA routes straight past
// the first-run screen. A user who ran `docker compose up` on a host that
// already had media under the mount landed on an empty library, with nothing
// scanned until they found Settings -> Storage -> Rescan for themselves.
func TestStartupScanRunsOnlyWhenTheRootWasJustSeeded(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/caravan.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	mgr := &stubManager{st: st}
	NewServer(st, mgr, testDist(), WithStartupScan(true))
	for range 200 {
		if mgr.scanCount.Load() > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if n := mgr.scanCount.Load(); n != 1 {
		t.Fatalf("scans after a seeded first start = %d, want 1", n)
	}

	// Every later start already has the root in the settings table, so nothing
	// is queued and a restart is not a rescan.
	quiet := &stubManager{st: st}
	NewServer(st, quiet, testDist())
	time.Sleep(20 * time.Millisecond)
	if n := quiet.scanCount.Load(); n != 0 {
		t.Fatalf("scans on an ordinary start = %d, want 0", n)
	}
}
