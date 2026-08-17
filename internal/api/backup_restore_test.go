package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func doRaw(t *testing.T, h http.Handler, method, target string, body []byte, decorate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if decorate != nil {
		decorate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDatabaseBackupStreamsSQLiteAttachment(t *testing.T) {
	h, st, _ := newTestServer(t)
	if err := st.SetSetting(context.Background(), "backup-test", "committed"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	rec := doRaw(t, h, http.MethodGet, "/api/v1/system/backup", nil, nil)
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("Cache-Control = %q, want no-store, private", got)
	}
	if got := rec.Header().Get("Content-Type"); got != backupContentType {
		t.Fatalf("Content-Type = %q, want %q", got, backupContentType)
	}
	disposition, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("parse Content-Disposition: %v", err)
	}
	if disposition != "attachment" || params["filename"] == "" {
		t.Fatalf("Content-Disposition = %q, want attachment filename", rec.Header().Get("Content-Disposition"))
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatalf("backup body does not begin with the SQLite file header: %q", rec.Body.Bytes())
	}
}

func TestDatabaseRestoreStagesValidSQLiteAndRequiresRestart(t *testing.T) {
	h, st, _ := newTestServer(t)
	if err := st.SetSetting(context.Background(), "restore-test", "current"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	var backup bytes.Buffer
	if err := st.Backup(context.Background(), &backup); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	rec := doRaw(t, h, http.MethodPost, "/api/v1/system/restore", backup.Bytes(), nil)
	wantStatus(t, rec, http.StatusAccepted)
	var response restoreResponse
	decodeBody(t, rec, &response)
	if !response.RestartRequired {
		t.Fatal("restore response did not require restart")
	}
	got, err := st.GetSetting(context.Background(), "restore-test")
	if err != nil {
		t.Fatalf("GetSetting after staged restore: %v", err)
	}
	if got != "current" {
		t.Fatalf("setting after staged restore = %q, want current", got)
	}
}

func TestDatabaseRestoreRejectsInvalidAndOversizeBodies(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := doRaw(t, h, http.MethodPost, "/api/v1/system/restore", []byte("not sqlite"), nil)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/restore", nil)
	request.ContentLength = fallbackRestoreUploadSize + 1
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, request)
	wantStatus(t, rec, http.StatusRequestEntityTooLarge)
	var response errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode oversize error: %v", err)
	}
	if response.Error != restoreUploadLimitMessage(fallbackRestoreUploadSize) {
		t.Fatalf("oversize error = %q, want fallback limit message", response.Error)
	}
}

// Regression: a portable ZIP uploaded with a generic or wrong content type
// must still route to the portable restore path instead of being rejected as
// a corrupt SQLite file.
func TestDatabaseRestoreRoutesZipPayloadsByMagic(t *testing.T) {
	h, _, _ := newTestServer(t)
	zipBody := []byte("PK\x03\x04rest-of-archive")
	for _, contentType := range []string{"", "application/octet-stream", "application/zip"} {
		rec := doRaw(t, h, http.MethodPost, "/api/v1/system/restore", zipBody, func(req *http.Request) {
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
		})
		// No pack service is configured in this harness, so reaching the
		// portable branch is observable as 503 rather than a 400.
		wantStatus(t, rec, http.StatusServiceUnavailable)
	}
}

func TestRestoreUploadLimitFromUsage(t *testing.T) {
	unavailable := errors.New("filesystem unavailable")
	tests := []struct {
		name        string
		free, total int64
		err         error
		want        int64
	}{
		{
			name: "capacity unavailable",
			err:  unavailable,
			want: fallbackRestoreUploadSize,
		},
		{
			name:  "zero capacity",
			total: 0,
			want:  fallbackRestoreUploadSize,
		},
		{
			name:  "reserve consumes free space",
			free:  restoreUploadSafetyReserve,
			total: restoreUploadSafetyReserve,
			want:  minimumRestoreUploadSize,
		},
		{
			name:  "free space after reserve",
			free:  restoreUploadSafetyReserve + 123,
			total: restoreUploadSafetyReserve + 123,
			want:  123,
		},
		{
			name:  "storage permits more than fallback",
			free:  restoreUploadSafetyReserve + fallbackRestoreUploadSize + 1,
			total: restoreUploadSafetyReserve + fallbackRestoreUploadSize + 1,
			want:  fallbackRestoreUploadSize + 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := restoreUploadLimitFromUsage(test.free, test.total, test.err); got != test.want {
				t.Fatalf("restoreUploadLimitFromUsage(%d, %d, %v) = %d, want %d",
					test.free, test.total, test.err, got, test.want)
			}
		})
	}
}

func TestDatabaseRestoreUsesStorageLimitForDeclaredAndChunkedBodies(t *testing.T) {
	h, _, database := newStorageAwareRestoreServer(t)
	const limit int64 = 8
	restoreDiskUsageForTest(t, database, restoreUploadSafetyReserve+limit, restoreUploadSafetyReserve+limit, nil)

	for _, test := range []struct {
		name          string
		contentLength int64
	}{
		{name: "declared", contentLength: limit + 1},
		{name: "chunked", contentLength: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/system/restore",
				bytes.NewReader(make([]byte, limit+1)))
			request.ContentLength = test.contentLength
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, request)
			wantStatus(t, rec, http.StatusRequestEntityTooLarge)
			var response errorResponse
			decodeBody(t, rec, &response)
			if response.Error != restoreUploadLimitMessage(limit) {
				t.Fatalf("restore error = %q, want limit for %q", response.Error, database)
			}
		})
	}
}

func TestDatabaseRestoreAllowsDeclaredBackupOverFallbackWhenStoragePermits(t *testing.T) {
	h, st, database := newStorageAwareRestoreServer(t)
	var backup bytes.Buffer
	if err := st.Backup(context.Background(), &backup); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	restoreDiskUsageForTest(t, database,
		restoreUploadSafetyReserve+fallbackRestoreUploadSize+int64(backup.Len()),
		restoreUploadSafetyReserve+fallbackRestoreUploadSize+int64(backup.Len()), nil)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/restore", bytes.NewReader(backup.Bytes()))
	request.ContentLength = fallbackRestoreUploadSize + 1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request)
	wantStatus(t, rec, http.StatusAccepted)
	wantRestoreResponse(t, rec)
}

func TestDatabaseBackupAndRestoreRequireAnAdmin(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, "member", testPassword, core.RoleMember)
	admin := login(t, h, testAdmin, testPassword)
	member := login(t, h, "member", testPassword)

	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/system/backup"},
		{http.MethodPost, "/api/v1/system/restore"},
	} {
		t.Run(request.method+request.path, func(t *testing.T) {
			rec := doRaw(t, h, request.method, request.path, nil, nil)
			wantStatus(t, rec, http.StatusUnauthorized)
			wantErrorBody(t, rec)

			rec = doRaw(t, h, request.method, request.path, nil, withCookie(member))
			wantStatus(t, rec, http.StatusForbidden)
			wantErrorBody(t, rec)
		})
	}

	backup := doRaw(t, h, http.MethodGet, "/api/v1/system/backup", nil, withCookie(admin))
	wantStatus(t, backup, http.StatusOK)
	restored := doRaw(t, h, http.MethodPost, "/api/v1/system/restore", backup.Body.Bytes(), withCookie(admin))
	wantStatus(t, restored, http.StatusAccepted)
	wantRestoreResponse(t, restored)
}

func wantRestoreResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var response restoreResponse
	decodeBody(t, rec, &response)
	if !response.RestartRequired {
		t.Fatal("admin restore response did not require restart")
	}
}

func newStorageAwareRestoreServer(t *testing.T) (http.Handler, *store.Store, string) {
	t.Helper()
	database := filepath.Join(t.TempDir(), "caravan.db")
	st, err := store.Open(database)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewServer(st, &stubManager{st: st}, testDist(), WithRuntimeDiagnostics(RuntimeConfig{
		DatabasePath: database,
	})), st, database
}

func restoreDiskUsageForTest(t *testing.T, wantPath string, free, total int64, usageErr error) {
	t.Helper()
	usage := restoreDiskUsage
	called := false
	restoreDiskUsage = func(path string) (int64, int64, error) {
		called = true
		if path != wantPath {
			t.Errorf("disk usage path = %q, want %q", path, wantPath)
		}
		return free, total, usageErr
	}
	t.Cleanup(func() {
		restoreDiskUsage = usage
		if !called {
			t.Error("restore did not check the database filesystem capacity")
		}
	})
}
