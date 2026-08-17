package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/indexer/packs"
	"github.com/watzon/caravan/internal/store"
)

func TestPortableBackupAndRestoreUsePackServiceWhenConfigured(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "caravan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewServer(st, &stubManager{st: st}, testDist(), WithDefinitionPacks(&packs.Service{Store: st, DataDir: root, Version: "1.0.0"}))

	backup := doRaw(t, h, http.MethodGet, "/api/v1/system/backup", nil, nil)
	wantStatus(t, backup, http.StatusOK)
	if got := backup.Header().Get("Content-Type"); got != portableBackupContentType {
		t.Fatalf("portable Content-Type = %q, want %q", got, portableBackupContentType)
	}
	if !bytes.HasPrefix(backup.Body.Bytes(), []byte("PK\x03\x04")) {
		t.Fatalf("portable backup does not begin with ZIP local header: %q", backup.Body.Bytes())
	}
	restore := doRaw(t, h, http.MethodPost, "/api/v1/system/restore", backup.Body.Bytes(), func(r *http.Request) {
		r.Header.Set("Content-Type", portableBackupContentType)
	})
	wantStatus(t, restore, http.StatusAccepted)
	wantRestoreResponse(t, restore)
}

func TestPortableRestoreChunkedOverflowIs413AndMalformedIsGeneric400(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "caravan.sqlite")
	st, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	service := &packs.Service{Store: st, DataDir: root, Version: "1.0.0"}
	h := NewServer(st, &stubManager{st: st}, testDist(),
		WithDefinitionPacks(service), WithRuntimeDiagnostics(RuntimeConfig{DatabasePath: database}))
	const limit int64 = 8
	restoreDiskUsageForTest(t, database, restoreUploadSafetyReserve+limit, restoreUploadSafetyReserve+limit, nil)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/restore", bytes.NewReader(make([]byte, limit+1)))
	request.ContentLength = -1
	request.Header.Set("Content-Type", portableBackupContentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request)
	wantStatus(t, rec, http.StatusRequestEntityTooLarge)
	var tooLarge errorResponse
	decodeBody(t, rec, &tooLarge)
	if tooLarge.Error != restoreUploadLimitMessage(limit) {
		t.Fatalf("portable overflow error = %q", tooLarge.Error)
	}

	malformed := doRaw(t, h, http.MethodPost, "/api/v1/system/restore", []byte("bad zip"), func(r *http.Request) {
		r.Header.Set("Content-Type", portableBackupContentType)
		r.ContentLength = -1
	})
	wantStatus(t, malformed, http.StatusBadRequest)
	var invalid errorResponse
	decodeBody(t, malformed, &invalid)
	if invalid.Error != "restore upload is not a compatible Caravan portable backup" {
		t.Fatalf("malformed portable error = %q", invalid.Error)
	}
}

type backupWriteErrorHandler struct {
	messages []string
}

func (h *backupWriteErrorHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *backupWriteErrorHandler) Handle(_ context.Context, record slog.Record) error {
	h.messages = append(h.messages, record.Message)
	return nil
}
func (h *backupWriteErrorHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *backupWriteErrorHandler) WithGroup(string) slog.Handler      { return h }

type failingBackupResponseWriter struct {
	header http.Header
	cancel context.CancelFunc
}

func (w *failingBackupResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (*failingBackupResponseWriter) WriteHeader(int) {}
func (w *failingBackupResponseWriter) Write([]byte) (int, error) {
	if w.cancel != nil {
		w.cancel()
	}
	return 0, errors.New("synthetic response write failure")
}

func TestPortableBackupChecksFinalResponseWriteAndSuppressesClientGone(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "caravan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	service := &packs.Service{Store: st, DataDir: root, Version: "1.0.0"}
	logs := &backupWriteErrorHandler{}
	s := &server{st: st, definitionPacks: service, log: slog.New(logs)}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/backup", nil)
	s.handleBackup(&failingBackupResponseWriter{}, req)
	if len(logs.messages) != 1 || logs.messages[0] != "write portable database backup" {
		t.Fatalf("portable write failure logs = %v", logs.messages)
	}

	logs.messages = nil
	ctx, cancel := context.WithCancel(context.Background())
	req = httptest.NewRequest(http.MethodGet, "/api/v1/system/backup", nil).WithContext(ctx)
	s.handleBackup(&failingBackupResponseWriter{cancel: cancel}, req)
	if len(logs.messages) != 0 {
		t.Fatalf("client-gone portable write failure was logged: %v", logs.messages)
	}
}
