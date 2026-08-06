package api

import (
	"net/http"
	"testing"
)

func TestRemotePathMappingCRUD(t *testing.T) {
	h, st, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/remote-path-mappings",
		`{"remote_path":"/downloads","local_path":"/mnt/media/downloads"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created remotePathMappingJSON
	decodeBody(t, rec, &created)
	if created.ID == 0 || created.RemotePath != "/downloads" || created.LocalPath != "/mnt/media/downloads" {
		t.Fatalf("created mapping = %+v", created)
	}
	if created.MatchCount != 0 || created.LastMatchedAt != "" {
		t.Fatalf("new mapping diagnostics = count %d, last %q", created.MatchCount, created.LastMatchedAt)
	}
	if err := st.RecordRemotePathMappingMatch(t.Context(), created.ID); err != nil {
		t.Fatalf("RecordRemotePathMappingMatch: %v", err)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/remote-path-mappings", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Mappings []remotePathMappingJSON `json:"remote_path_mappings"`
	}
	decodeBody(t, rec, &list)
	if len(list.Mappings) != 1 || list.Mappings[0].ID != created.ID {
		t.Fatalf("mapping list = %+v, want id %d", list.Mappings, created.ID)
	}
	if list.Mappings[0].MatchCount != 1 || list.Mappings[0].LastMatchedAt == "" {
		t.Fatalf("listed mapping diagnostics = %+v", list.Mappings[0])
	}

	rec = do(t, h, http.MethodPut, "/api/v1/remote-path-mappings/"+itoa(created.ID),
		`{"remote_path":"D:\\\\Complete","local_path":"/srv/complete/"}`)
	wantStatus(t, rec, http.StatusOK)
	var updated remotePathMappingJSON
	decodeBody(t, rec, &updated)
	if updated.RemotePath != `D:\\Complete` || updated.LocalPath != "/srv/complete" {
		t.Fatalf("updated mapping = %+v", updated)
	}
	if updated.MatchCount != 1 || updated.LastMatchedAt == "" {
		t.Fatalf("updated mapping lost diagnostics: %+v", updated)
	}

	rec = do(t, h, http.MethodDelete, "/api/v1/remote-path-mappings/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNoContent)
	rec = do(t, h, http.MethodDelete, "/api/v1/remote-path-mappings/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNotFound)
}

func TestRemotePathMappingsValidateAndRejectDuplicateRoots(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, body := range []string{
		`{"remote_path":"downloads","local_path":"/mnt/downloads"}`,
		`{"remote_path":"/downloads","local_path":"mnt/downloads"}`,
	} {
		rec := do(t, h, http.MethodPost, "/api/v1/remote-path-mappings", body)
		wantStatus(t, rec, http.StatusBadRequest)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/remote-path-mappings",
		`{"remote_path":"/Downloads/","local_path":"/mnt/one"}`)
	wantStatus(t, rec, http.StatusCreated)
	rec = do(t, h, http.MethodPost, "/api/v1/remote-path-mappings",
		`{"remote_path":"/downloads","local_path":"/mnt/two"}`)
	wantStatus(t, rec, http.StatusConflict)
}
