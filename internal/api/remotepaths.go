package api

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

type remotePathMappingJSON struct {
	ID            int64  `json:"id"`
	RemotePath    string `json:"remote_path"`
	LocalPath     string `json:"local_path"`
	MatchCount    int64  `json:"match_count"`
	LastMatchedAt string `json:"last_matched_at"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type remotePathMappingRequest struct {
	RemotePath string `json:"remote_path"`
	LocalPath  string `json:"local_path"`
}

func remotePathMappingDTO(m core.RemotePathMapping) remotePathMappingJSON {
	return remotePathMappingJSON{
		ID:            m.ID,
		RemotePath:    m.RemotePath,
		LocalPath:     m.LocalPath,
		MatchCount:    m.MatchCount,
		LastMatchedAt: jsonTime(m.LastMatchedAt),
		CreatedAt:     jsonTime(m.CreatedAt),
		UpdatedAt:     jsonTime(m.UpdatedAt),
	}
}

func (body remotePathMappingRequest) mapping() (core.RemotePathMapping, string) {
	remotePath := strings.TrimSpace(body.RemotePath)
	localPath := strings.TrimSpace(body.LocalPath)
	if !absoluteClientPath(remotePath) {
		return core.RemotePathMapping{}, "remote_path must be an absolute Unix or Windows path"
	}
	if !filepath.IsAbs(localPath) {
		return core.RemotePathMapping{}, "local_path must be an absolute path on the Caravan host"
	}
	return core.RemotePathMapping{
		RemotePath: cleanClientRoot(remotePath),
		LocalPath:  filepath.Clean(localPath),
	}, ""
}

func absoluteClientPath(value string) bool {
	if strings.HasPrefix(value, "/") {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func cleanClientRoot(value string) string {
	value = strings.TrimSpace(value)
	if value == "/" || (len(value) == 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\')) {
		return value
	}
	return strings.TrimRight(value, `/\\`)
}

func (s *server) handleListRemotePathMappings(w http.ResponseWriter, r *http.Request) {
	mappings, err := s.st.ListRemotePathMappings(r.Context())
	if err != nil {
		s.writeStoreError(w, "list remote path mappings", err)
		return
	}
	out := make([]remotePathMappingJSON, 0, len(mappings))
	for _, mapping := range mappings {
		out = append(out, remotePathMappingDTO(mapping))
	}
	writeJSON(w, http.StatusOK, map[string]any{"remote_path_mappings": out})
}

func (s *server) handleCreateRemotePathMapping(w http.ResponseWriter, r *http.Request) {
	var body remotePathMappingRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	mapping, message := body.mapping()
	if message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}
	if !s.remotePathFree(r, w, mapping.RemotePath, 0) {
		return
	}
	if err := s.st.CreateRemotePathMapping(r.Context(), &mapping); err != nil {
		s.writeStoreError(w, "create remote path mapping", err)
		return
	}
	writeJSON(w, http.StatusCreated, remotePathMappingDTO(mapping))
}

func (s *server) handleUpdateRemotePathMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.st.GetRemotePathMapping(r.Context(), id); err != nil {
		s.writeStoreError(w, "get remote path mapping", err)
		return
	}
	var body remotePathMappingRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	mapping, message := body.mapping()
	if message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}
	if !s.remotePathFree(r, w, mapping.RemotePath, id) {
		return
	}
	mapping.ID = id
	if err := s.st.UpdateRemotePathMapping(r.Context(), &mapping); err != nil {
		s.writeStoreError(w, "update remote path mapping", err)
		return
	}
	updated, err := s.st.GetRemotePathMapping(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get updated remote path mapping", err)
		return
	}
	writeJSON(w, http.StatusOK, remotePathMappingDTO(*updated))
}

func (s *server) handleDeleteRemotePathMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.st.GetRemotePathMapping(r.Context(), id); err != nil {
		s.writeStoreError(w, "get remote path mapping", err)
		return
	}
	if err := s.st.DeleteRemotePathMapping(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete remote path mapping", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) remotePathFree(r *http.Request, w http.ResponseWriter, remotePath string, exceptID int64) bool {
	mappings, err := s.st.ListRemotePathMappings(r.Context())
	if err != nil {
		s.writeStoreError(w, "list remote path mappings", err)
		return false
	}
	for _, mapping := range mappings {
		if mapping.ID != exceptID && strings.EqualFold(mapping.RemotePath, remotePath) {
			writeError(w, http.StatusConflict, "a remote path mapping already uses this client path")
			return false
		}
	}
	return true
}
