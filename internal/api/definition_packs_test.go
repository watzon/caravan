package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/indexer/packs"
	"github.com/watzon/caravan/internal/store"
)

func TestDefinitionPackRoutesAreRegisteredAndUnavailableWithoutService(t *testing.T) {
	h, _, _ := newTestServer(t, WithDefinitionPacks(nil))
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/definition-packs"},
		{http.MethodPost, "/api/v1/definition-packs/preview"},
		{http.MethodPost, "/api/v1/definition-packs/install"},
		{http.MethodPost, "/api/v1/definition-packs/activate"},
		{http.MethodPost, "/api/v1/definition-packs/rollback"},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			rec := do(t, h, test.method, test.path, "")
			wantStatus(t, rec, http.StatusServiceUnavailable)
			wantErrorBody(t, rec)
		})
	}
}

func TestDefinitionPackMutationsRequirePersistedOwnerOnOpenServer(t *testing.T) {
	h, _, _ := newTestServer(t, WithDefinitionPacks(&packs.Service{}))
	for _, path := range []string{
		"/api/v1/definition-packs/preview", "/api/v1/definition-packs/install",
		"/api/v1/definition-packs/activate", "/api/v1/definition-packs/rollback",
	} {
		rec := do(t, h, http.MethodPost, path, "")
		wantStatus(t, rec, http.StatusForbidden)
		wantErrorBody(t, rec)
	}
}

type apiSignedPack struct {
	archive                 []byte
	publicKey               ed25519.PublicKey
	privateKey              ed25519.PrivateKey
	source, revision, keyID string
	license, notice         string
	definition              []byte
}

type apiPackField struct {
	name string
	data []byte
}

func makeAPISignedPack(t *testing.T) apiSignedPack {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture := apiSignedPack{
		publicKey: publicKey, privateKey: privateKey,
		source: "synthetic.test", revision: "r1", keyID: "synthetic-ed25519-key",
		license: "Invented synthetic test license.\n", notice: "Invented synthetic test notice.\n",
		definition: []byte("id: fixture\nname: Synthetic Fixture\ntype: private\nlinks: [https://tracker.example]\nsettings:\n  - {name: token, type: text}\ncaps: {modes: {search: [q]}}\nsearch:\n  paths: [{path: /search}]\n  rows: {selector: article}\n  fields:\n    title: {selector: h2}\n    download: {selector: a, attribute: href}\n# exact-definition-marker\n"),
	}
	fixture.archive = buildAPISignedPack(t, fixture)
	return fixture
}

func buildAPISignedPack(t *testing.T, fixture apiSignedPack) []byte {
	t.Helper()
	digest := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	manifest := map[string]any{
		"format_version": 1, "cardigann_schema_version": 1,
		"source": fixture.source, "revision": fixture.revision,
		"spdx_license_expression": "MIT", "provenance": "invented synthetic API fixture",
		"signer_key_id": fixture.keyID, "minimum_caravan_version": "0.1.0",
		"total_files":              3,
		"total_uncompressed_bytes": len(fixture.license) + len(fixture.notice) + len(fixture.definition),
		"license":                  map[string]any{"path": "LICENSE", "sha256": digest([]byte(fixture.license))},
		"notice":                   map[string]any{"path": "NOTICE", "sha256": digest([]byte(fixture.notice))},
		"definitions": []map[string]any{{
			"id": "fixture", "metadata_id": "1337x", "path": "definitions/fixture.yml",
			"sha256": digest(fixture.definition), "approved_origins": []string{"https://tracker.example"},
		}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(fixture.privateKey, manifestBytes)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, member := range []struct {
		name string
		data []byte
	}{
		{"manifest.json", manifestBytes}, {"manifest.sig", signature}, {"LICENSE", []byte(fixture.license)},
		{"NOTICE", []byte(fixture.notice)}, {"definitions/fixture.yml", fixture.definition},
	} {
		h := &zip.FileHeader{Name: member.name, Method: zip.Store}
		h.SetMode(0o600)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func tamperAPIPackDefinition(t *testing.T, archive []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, file := range reader.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "definitions/fixture.yml" {
			data = append(data, []byte("tampered: true\n")...)
		}
		h := &zip.FileHeader{Name: file.Name, Method: zip.Store}
		h.SetMode(0o600)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func newDefinitionPackAPIServer(t *testing.T, opts ...Option) (http.Handler, *store.Store, *packs.Service, *http.Cookie, *http.Cookie) {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "caravan.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	service := &packs.Service{Store: st, DataDir: dataDir, Version: "1.0.0", PreviewTTL: time.Minute}
	opts = append(opts, WithDefinitionPacks(service))
	h := NewServer(st, &stubManager{st: st}, testDist(), opts...)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	return h, st, service, login(t, h, testAdmin, testPassword), login(t, h, testMember, testPassword)
}

func packMultipart(t *testing.T, fields []apiPackField) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, field := range fields {
		var part io.Writer
		var err error
		if field.name == "archive" {
			part, err = mw.CreateFormFile(field.name, "fixture.zip")
		} else {
			part, err = mw.CreateFormField(field.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(field.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), mw.FormDataContentType()
}

func doPackMultipart(t *testing.T, h http.Handler, path string, fields []apiPackField, cookie *http.Cookie, contentLength int64) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := packMultipart(t, fields)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if contentLength != 0 {
		req.ContentLength = contentLength
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func previewPackFields(f apiSignedPack) []apiPackField {
	return []apiPackField{
		{name: "archive", data: f.archive},
		{name: "signer_key_id", data: []byte(f.keyID)},
		{name: "public_key", data: []byte(base64.StdEncoding.EncodeToString(f.publicKey))},
	}
}

func installPackFields(f apiSignedPack, source, token string) []apiPackField {
	fields := previewPackFields(f)
	return append(fields, apiPackField{name: "source", data: []byte(source)}, apiPackField{name: "token", data: []byte(token)})
}

func TestDefinitionPackMultipartPreviewInstallPersistsInactiveAndSanitizesResponses(t *testing.T) {
	h, st, _, admin, _ := newDefinitionPackAPIServer(t)
	fixture := makeAPISignedPack(t)
	previewRec := doPackMultipart(t, h, "/api/v1/definition-packs/preview", previewPackFields(fixture), admin, 0)
	wantStatus(t, previewRec, http.StatusOK)
	var preview struct {
		Source, Revision, License, Notice, Token string
	}
	decodeBody(t, previewRec, &preview)
	if preview.Source != fixture.source || preview.Revision != fixture.revision || preview.License != fixture.license || preview.Notice != fixture.notice || preview.Token == "" {
		t.Fatalf("preview = %+v", preview)
	}

	installRec := doPackMultipart(t, h, "/api/v1/definition-packs/install", installPackFields(fixture, preview.Source, preview.Token), admin, 0)
	wantStatus(t, installRec, http.StatusCreated)
	var installed definitionPackRevisionJSON
	decodeBody(t, installRec, &installed)
	if installed.Source != fixture.source || installed.Revision != fixture.revision || installed.InstallState != core.DefinitionPackInstalled || installed.Active || installed.Pending || installed.RunnableCount != 1 {
		t.Fatalf("installed status = %+v", installed)
	}
	revision, err := st.GetDefinitionPackRevision(context.Background(), fixture.source, fixture.revision)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(revision.SignerPublicKey, fixture.publicKey) || revision.ArchiveRelPath == "" {
		t.Fatalf("persisted receipt omitted signer/path: %+v", revision)
	}

	listRec := doAuth(t, h, http.MethodGet, "/api/v1/definition-packs", "", withCookie(admin))
	wantStatus(t, listRec, http.StatusOK)
	for name, rec := range map[string]*httptest.ResponseRecorder{"install": installRec, "list": listRec} {
		raw := rec.Body.String()
		for _, leaked := range []string{
			`"public_key"`, `"signer_public_key"`, `"archive_rel_path"`, `"settings"`, `"token"`,
			base64.StdEncoding.EncodeToString(fixture.publicKey), revision.ArchiveRelPath,
			"exact-definition-marker", "secret-setting-value", preview.Token,
		} {
			if leaked != "" && strings.Contains(raw, leaked) {
				t.Fatalf("%s response leaked %q: %s", name, leaked, raw)
			}
		}
	}
}

func TestDefinitionPackMultipartMemberIsForbiddenBeforeUploadProcessing(t *testing.T) {
	h, _, _, _, member := newDefinitionPackAPIServer(t)
	fixture := makeAPISignedPack(t)
	for _, path := range []string{"/api/v1/definition-packs/preview", "/api/v1/definition-packs/install"} {
		rec := doPackMultipart(t, h, path, previewPackFields(fixture), member, 0)
		wantStatus(t, rec, http.StatusForbidden)
		wantErrorBody(t, rec)
	}
}

func TestDefinitionPackMultipartRejectsInvalidShapesKeysSignaturesAndTamper(t *testing.T) {
	h, _, _, admin, _ := newDefinitionPackAPIServer(t)
	fixture := makeAPISignedPack(t)
	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	valid := previewPackFields(fixture)
	tests := []struct {
		name   string
		fields []apiPackField
	}{
		{name: "duplicate field", fields: append(append([]apiPackField(nil), valid...), apiPackField{name: "public_key", data: []byte("duplicate")})},
		{name: "unknown field", fields: append(append([]apiPackField(nil), valid...), apiPackField{name: "extra", data: []byte("no")})},
		{name: "missing archive", fields: valid[1:]},
		{name: "missing signer key id", fields: []apiPackField{valid[0], valid[2]}},
		{name: "missing public key", fields: valid[:2]},
		{name: "invalid base64", fields: []apiPackField{valid[0], valid[1], {name: "public_key", data: []byte("not-base64")}}},
		{name: "short public key", fields: []apiPackField{valid[0], valid[1], {name: "public_key", data: []byte(base64.StdEncoding.EncodeToString([]byte("short")))}}},
		{name: "unknown key id", fields: []apiPackField{valid[0], {name: "signer_key_id", data: []byte("wrong-key")}, valid[2]}},
		{name: "invalid signature", fields: []apiPackField{valid[0], valid[1], {name: "public_key", data: []byte(base64.StdEncoding.EncodeToString(otherPublic))}}},
		{name: "tampered definition", fields: []apiPackField{{name: "archive", data: tamperAPIPackDefinition(t, fixture.archive)}, valid[1], valid[2]}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := doPackMultipart(t, h, "/api/v1/definition-packs/preview", test.fields, admin, 0)
			wantStatus(t, rec, http.StatusBadRequest)
			var body errorResponse
			decodeBody(t, rec, &body)
			if body.Error != definitionPackUploadInvalid {
				t.Fatalf("error = %q, want stable upload error", body.Error)
			}
		})
	}
}

func TestDefinitionPackMultipartLimitsMapToStable413(t *testing.T) {
	h, _, _, admin, _ := newDefinitionPackAPIServer(t)
	fixture := makeAPISignedPack(t)
	oversizeScalar := bytes.Repeat([]byte{'k'}, int(definitionPackMultipartReserve)+1)
	oversizeArchive := bytes.Repeat([]byte{'z'}, int(cardigann.MaxPackArchiveBytes)+1)
	globalFields := []apiPackField{
		{name: "archive", data: oversizeArchive[:len(oversizeArchive)-1]},
		{name: "signer_key_id", data: oversizeScalar[:len(oversizeScalar)-1]},
		{name: "public_key", data: previewPackFields(fixture)[2].data},
	}
	for _, test := range []struct {
		name          string
		fields        []apiPackField
		contentLength int64
	}{
		{name: "per scalar field", fields: []apiPackField{{name: "archive", data: fixture.archive}, {name: "signer_key_id", data: oversizeScalar}, previewPackFields(fixture)[2]}},
		{name: "per archive field", fields: []apiPackField{{name: "archive", data: oversizeArchive}, previewPackFields(fixture)[1], previewPackFields(fixture)[2]}},
		{name: "chunked global", fields: globalFields, contentLength: -1},
		{name: "declared global", fields: previewPackFields(fixture), contentLength: cardigann.MaxPackArchiveBytes + definitionPackMultipartReserve + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := doPackMultipart(t, h, "/api/v1/definition-packs/preview", test.fields, admin, test.contentLength)
			wantStatus(t, rec, http.StatusRequestEntityTooLarge)
			var body errorResponse
			decodeBody(t, rec, &body)
			if body.Error != definitionPackUploadInvalid {
				t.Fatalf("error = %q, want stable upload error", body.Error)
			}
		})
	}
}

func installPackThroughAPI(t *testing.T, h http.Handler, admin *http.Cookie, fixture apiSignedPack) definitionPackRevisionJSON {
	t.Helper()
	previewRec := doPackMultipart(t, h, "/api/v1/definition-packs/preview", previewPackFields(fixture), admin, 0)
	wantStatus(t, previewRec, http.StatusOK)
	var preview struct {
		Source, Token string
	}
	decodeBody(t, previewRec, &preview)
	installRec := doPackMultipart(t, h, "/api/v1/definition-packs/install", installPackFields(fixture, preview.Source, preview.Token), admin, 0)
	wantStatus(t, installRec, http.StatusCreated)
	var installed definitionPackRevisionJSON
	decodeBody(t, installRec, &installed)
	return installed
}

func TestDefinitionPackActivationIsRestartGatedAndRollbackCancelsPending(t *testing.T) {
	h, _, service, admin, _ := newDefinitionPackAPIServer(t)
	fixture := makeAPISignedPack(t)
	installed := installPackThroughAPI(t, h, admin, fixture)
	if installed.RunnableCount != 1 || installed.Active || installed.Pending {
		t.Fatalf("fresh install = %+v", installed)
	}

	activate := doAuth(t, h, http.MethodPost, "/api/v1/definition-packs/activate",
		`{"source":`+quote(fixture.source)+`,"revision":`+quote(fixture.revision)+`}`, withCookie(admin))
	wantStatus(t, activate, http.StatusAccepted)
	wantRestoreResponse(t, activate)
	pending, err := service.Status(context.Background(), fixture.source, fixture.revision)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.Pending || pending.Active || pending.LastKnownGood {
		t.Fatalf("activation hot-promoted instead of pending restart: %+v", pending)
	}

	rollback := doAuth(t, h, http.MethodPost, "/api/v1/definition-packs/rollback",
		`{"source":`+quote(fixture.source)+`,"revision":`+quote(fixture.revision)+`}`, withCookie(admin))
	wantStatus(t, rollback, http.StatusOK)
	var rolledBack definitionPackRevisionJSON
	decodeBody(t, rollback, &rolledBack)
	if rolledBack.Pending || rolledBack.Active || rolledBack.LastKnownGood {
		t.Fatalf("rollback did not cancel pending only: %+v", rolledBack)
	}
}

func TestDefinitionPackActivationRejectsZeroRunnableAndLifecycleErrorsExposeNoPaths(t *testing.T) {
	h, _, _, admin, _ := newDefinitionPackAPIServer(t)
	fixture := makeAPISignedPack(t)
	fixture.definition = []byte("id: fixture\nname: Synthetic Invalid Fixture\nlinks: [https://tracker.example]\ncaps: {modes: {search: [q]}}\nsearch:\n  paths: [{path: /search}]\n  rows: {selector: article}\n  fields:\n    title: {selector: h2, filters: [{name: dateparse}]}\n    download: {selector: a, attribute: href}\n")
	fixture.archive = buildAPISignedPack(t, fixture)
	installed := installPackThroughAPI(t, h, admin, fixture)
	if installed.RunnableCount != 0 {
		t.Fatalf("compiler-invalid install runnable count = %d, want 0", installed.RunnableCount)
	}
	activate := doAuth(t, h, http.MethodPost, "/api/v1/definition-packs/activate",
		`{"source":`+quote(fixture.source)+`,"revision":`+quote(fixture.revision)+`}`, withCookie(admin))
	wantStatus(t, activate, http.StatusConflict)
	var rejected errorResponse
	decodeBody(t, activate, &rejected)
	if rejected.Error != "definition pack revision cannot be activated" {
		t.Fatalf("zero-runnable activation error = %q", rejected.Error)
	}

	for path, want := range map[string]string{
		"activate": "definition pack revision cannot be activated",
		"rollback": "definition pack rollback was not accepted",
	} {
		rec := doAuth(t, h, http.MethodPost, "/api/v1/definition-packs/"+path,
			`{"source":"missing.test","revision":"not-installed"}`, withCookie(admin))
		wantStatus(t, rec, http.StatusConflict)
		var body errorResponse
		decodeBody(t, rec, &body)
		if body.Error != want || strings.Contains(body.Error, "archives/") || strings.Contains(body.Error, "caravan.db") {
			t.Fatalf("%s error = %q, want stable no-path error %q", path, body.Error, want)
		}
	}
}
