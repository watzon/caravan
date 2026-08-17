package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/indexer/packs"
)

const (
	definitionPackMultipartReserve = int64(1 << 20)
	definitionPackOwnerRequired    = "definition pack management requires the owner to create and sign in to an administrator account"
	definitionPackUploadInvalid    = "definition pack upload is invalid"
)

type definitionPackRevisionJSON struct {
	Source                string `json:"source"`
	Revision              string `json:"revision"`
	InstallState          string `json:"install_state"`
	Pending               bool   `json:"pending"`
	Active                bool   `json:"active"`
	LastKnownGood         bool   `json:"last_known_good"`
	ValidationCode        string `json:"validation_code,omitempty"`
	DefinitionCount       int    `json:"definition_count"`
	RunnableCount         int    `json:"runnable_count"`
	ArchiveDigest         string `json:"archive_digest"`
	ManifestDigest        string `json:"manifest_digest"`
	LicenseDigest         string `json:"license_digest"`
	NoticeDigest          string `json:"notice_digest,omitempty"`
	SignatureFingerprint  string `json:"signature_fingerprint"`
	LicenseExpression     string `json:"license_expression"`
	Provenance            string `json:"provenance"`
	MinimumCaravanVersion string `json:"minimum_caravan_version"`
	InstalledAt           string `json:"installed_at,omitempty"`
	AcceptedAt            string `json:"accepted_at"`
	AcceptedByUserID      int64  `json:"accepted_by_user_id"`
}

func definitionPackStatusDTO(status packs.Status) definitionPackRevisionJSON {
	return definitionPackRevisionJSON{
		Source: status.Source, Revision: status.Revision, InstallState: status.State,
		Pending: status.Pending, Active: status.Active, LastKnownGood: status.LastKnownGood,
		ValidationCode: status.ValidationCode, DefinitionCount: status.DefinitionCount, RunnableCount: status.RunnableCount,
		ArchiveDigest: status.ArchiveDigest, ManifestDigest: status.ManifestDigest, LicenseDigest: status.LicenseDigest,
		NoticeDigest: status.NoticeDigest, SignatureFingerprint: status.SignerKeyFingerprint,
		LicenseExpression: status.LicenseExpression, Provenance: status.Provenance, MinimumCaravanVersion: status.MinimumCaravanVersion,
		InstalledAt: status.InstalledAt.UTC().Format(time.RFC3339Nano), AcceptedAt: status.AcceptedAt.UTC().Format(time.RFC3339Nano),
		AcceptedByUserID: status.AcceptedByUserID,
	}
}

func (s *server) requireDefinitionPacks(w http.ResponseWriter) (*packs.Service, bool) {
	if s.definitionPacks == nil {
		writeError(w, http.StatusServiceUnavailable, "definition pack service is not configured")
		return nil, false
	}
	return s.definitionPacks, true
}

func (s *server) requireDefinitionPackOwner(w http.ResponseWriter, r *http.Request) (int64, bool) {
	user := currentUser(r)
	if user.ID <= 0 {
		writeError(w, http.StatusForbidden, definitionPackOwnerRequired)
		return 0, false
	}
	return user.ID, true
}

func (s *server) handleListDefinitionPacks(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireDefinitionPacks(w)
	if !ok {
		return
	}
	statuses, err := service.List(r.Context())
	if err != nil {
		s.log.Error("list definition packs", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list definition packs")
		return
	}
	out := make([]definitionPackRevisionJSON, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, definitionPackStatusDTO(status))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": out})
}

type definitionPackMultipart struct {
	archive, signerKeyID, publicKey, source, token []byte
}

func (s *server) readDefinitionPackMultipart(w http.ResponseWriter, r *http.Request, install bool) (definitionPackMultipart, bool) {
	var result definitionPackMultipart
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		writeError(w, http.StatusBadRequest, definitionPackUploadInvalid)
		return result, false
	}
	if r.ContentLength > cardigann.MaxPackArchiveBytes+definitionPackMultipartReserve {
		writeError(w, http.StatusRequestEntityTooLarge, definitionPackUploadInvalid)
		return result, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, cardigann.MaxPackArchiveBytes+definitionPackMultipartReserve)
	reader := multipart.NewReader(r.Body, params["boundary"])
	seen := map[string]bool{}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			definitionPackUploadError(w, nextErr)
			return result, false
		}
		name := part.FormName()
		if seen[name] || (name != "archive" && name != "signer_key_id" && name != "public_key" && (!install || (name != "source" && name != "token"))) {
			_ = part.Close()
			writeError(w, http.StatusBadRequest, definitionPackUploadInvalid)
			return result, false
		}
		seen[name] = true
		limit := definitionPackMultipartReserve
		if name == "archive" {
			limit = cardigann.MaxPackArchiveBytes
		}
		data, readErr := io.ReadAll(io.LimitReader(part, limit+1))
		_ = part.Close()
		if readErr != nil || int64(len(data)) > limit {
			if readErr != nil {
				definitionPackUploadError(w, readErr)
			} else {
				writeError(w, http.StatusRequestEntityTooLarge, definitionPackUploadInvalid)
			}
			return result, false
		}
		switch name {
		case "archive":
			result.archive = data
		case "signer_key_id":
			result.signerKeyID = data
		case "public_key":
			result.publicKey = data
		case "source":
			result.source = data
		case "token":
			result.token = data
		}
	}
	if len(result.archive) == 0 || len(result.signerKeyID) == 0 || len(result.publicKey) == 0 || (install && (len(result.source) == 0 || len(result.token) == 0)) {
		writeError(w, http.StatusBadRequest, definitionPackUploadInvalid)
		return result, false
	}
	return result, true
}

func definitionPackUploadError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, definitionPackUploadInvalid)
		return
	}
	writeError(w, http.StatusBadRequest, definitionPackUploadInvalid)
}

func definitionPackPublicKey(data []byte) ([]byte, bool) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, false
	}
	return key, true
}

func (s *server) handlePreviewDefinitionPack(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireDefinitionPacks(w)
	if !ok {
		return
	}
	actor, ok := s.requireDefinitionPackOwner(w, r)
	if !ok {
		return
	}
	input, ok := s.readDefinitionPackMultipart(w, r, false)
	if !ok {
		return
	}
	key, ok := definitionPackPublicKey(input.publicKey)
	if !ok {
		writeError(w, http.StatusBadRequest, definitionPackUploadInvalid)
		return
	}
	preview, err := service.Preview(r.Context(), actor, string(input.signerKeyID), key, input.archive)
	if err != nil {
		s.log.Warn("definition pack preview rejected", "error", err)
		writeError(w, http.StatusBadRequest, definitionPackUploadInvalid)
		return
	}
	if !utf8.Valid(preview.License) || !utf8.Valid(preview.Notice) {
		writeError(w, http.StatusBadRequest, definitionPackUploadInvalid)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": preview.Source, "revision": preview.Revision, "archive_digest": preview.ArchiveDigest,
		"manifest_digest": preview.ManifestDigest, "license_digest": preview.LicenseDigest,
		"signature_fingerprint": preview.SignerKeyFingerprint, "license": string(preview.License), "notice": string(preview.Notice),
		"token": preview.Token, "expires_at": preview.ExpiresAt,
	})
}

func (s *server) handleInstallDefinitionPack(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireDefinitionPacks(w)
	if !ok {
		return
	}
	actor, ok := s.requireDefinitionPackOwner(w, r)
	if !ok {
		return
	}
	input, ok := s.readDefinitionPackMultipart(w, r, true)
	if !ok {
		return
	}
	key, ok := definitionPackPublicKey(input.publicKey)
	if !ok {
		writeError(w, http.StatusBadRequest, definitionPackUploadInvalid)
		return
	}
	status, err := service.AcceptAndInstall(r.Context(), actor, string(input.source), string(input.token), string(input.signerKeyID), key, input.archive)
	if err != nil {
		s.log.Warn("definition pack install rejected", "error", err)
		writeError(w, http.StatusConflict, "definition pack install was not accepted")
		return
	}
	writeJSON(w, http.StatusCreated, definitionPackStatusDTO(status))
}

type definitionPackLifecycleRequest struct {
	Source   string `json:"source"`
	Revision string `json:"revision"`
}

func (s *server) handleActivateDefinitionPack(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireDefinitionPacks(w)
	if !ok {
		return
	}
	if _, ok := s.requireDefinitionPackOwner(w, r); !ok {
		return
	}
	var body definitionPackLifecycleRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	status, err := service.Status(r.Context(), strings.TrimSpace(body.Source), strings.TrimSpace(body.Revision))
	if err != nil || status.RunnableCount == 0 {
		writeError(w, http.StatusConflict, "definition pack revision cannot be activated")
		return
	}
	if err := service.RequestActivation(r.Context(), status.Source, status.Revision); err != nil {
		s.log.Warn("definition pack activation rejected", "error", err)
		writeError(w, http.StatusConflict, "definition pack revision cannot be activated")
		return
	}
	writeJSON(w, http.StatusAccepted, restoreResponse{RestartRequired: true})
}

func (s *server) handleRollbackDefinitionPack(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireDefinitionPacks(w)
	if !ok {
		return
	}
	if _, ok := s.requireDefinitionPackOwner(w, r); !ok {
		return
	}
	var body definitionPackLifecycleRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := service.Rollback(r.Context(), strings.TrimSpace(body.Source), strings.TrimSpace(body.Revision)); err != nil {
		s.log.Warn("definition pack rollback rejected", "error", err)
		writeError(w, http.StatusConflict, "definition pack rollback was not accepted")
		return
	}
	status, err := service.Status(r.Context(), strings.TrimSpace(body.Source), strings.TrimSpace(body.Revision))
	if err != nil {
		s.log.Error("read definition pack rollback status", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read definition pack status")
		return
	}
	writeJSON(w, http.StatusOK, definitionPackStatusDTO(status))
}
