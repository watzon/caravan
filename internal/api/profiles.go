package api

import (
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// qualityProfileJSON is the API shape of core.QualityProfile.
type qualityProfileJSON struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Cutoff         string   `json:"cutoff"`
	Items          []string `json:"items"`
	UpgradeAllowed bool     `json:"upgrade_allowed"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

func qualityProfileDTO(p core.QualityProfile) qualityProfileJSON {
	return qualityProfileJSON{
		ID:             p.ID,
		Name:           p.Name,
		Cutoff:         p.Cutoff,
		Items:          p.Items,
		UpgradeAllowed: p.UpgradeAllowed,
		CreatedAt:      jsonTime(p.CreatedAt),
		UpdatedAt:      jsonTime(p.UpdatedAt),
	}
}

// profileRequest is the create/update body. Every field is required: a
// profile is small enough that partial updates are not worth a pointer
// ladder.
type profileRequest struct {
	Name           string   `json:"name"`
	Cutoff         string   `json:"cutoff"`
	Items          []string `json:"items"`
	UpgradeAllowed bool     `json:"upgrade_allowed"`
}

// validateProfile enforces the profile invariants the ladder depends on:
// items are known qualities without duplicates, and the cutoff is one of
// them. A cutoff outside the item list would make "upgrade until cutoff"
// unreachable by definition.
func validateProfile(body profileRequest) (string, bool) {
	if strings.TrimSpace(body.Name) == "" {
		return "name is required", false
	}
	if len(body.Items) == 0 {
		return "items must list at least one quality", false
	}
	seen := map[string]bool{}
	for _, q := range body.Items {
		if core.QualityRank(q) >= len(core.QualityLadder) {
			return "items must be known qualities: " + q, false
		}
		if seen[q] {
			return "items must not repeat a quality: " + q, false
		}
		seen[q] = true
	}
	if !seen[body.Cutoff] {
		return "cutoff must be one of the items", false
	}
	return "", true
}

func (s *server) handleListQualityProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.st.ListQualityProfiles(r.Context())
	if err != nil {
		s.writeStoreError(w, "list quality profiles", err)
		return
	}
	out := make([]qualityProfileJSON, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, qualityProfileDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

func (s *server) handleCreateQualityProfile(w http.ResponseWriter, r *http.Request) {
	var body profileRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if msg, ok := validateProfile(body); !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	p := core.QualityProfile{
		Name:           strings.TrimSpace(body.Name),
		Cutoff:         body.Cutoff,
		Items:          body.Items,
		UpgradeAllowed: body.UpgradeAllowed,
	}
	if err := s.st.CreateQualityProfile(r.Context(), &p); err != nil {
		s.writeProfileConflict(w, "create quality profile", err)
		return
	}
	writeJSON(w, http.StatusCreated, qualityProfileDTO(p))
}

func (s *server) handleUpdateQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body profileRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if msg, ok := validateProfile(body); !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	p := core.QualityProfile{
		ID:             id,
		Name:           strings.TrimSpace(body.Name),
		Cutoff:         body.Cutoff,
		Items:          body.Items,
		UpgradeAllowed: body.UpgradeAllowed,
	}
	if err := s.st.UpdateQualityProfile(r.Context(), &p); err != nil {
		s.writeProfileConflict(w, "update quality profile", err)
		return
	}
	stored, err := s.st.GetQualityProfile(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get quality profile", err)
		return
	}
	writeJSON(w, http.StatusOK, qualityProfileDTO(*stored))
}

func (s *server) handleDeleteQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	profiles, err := s.st.ListQualityProfiles(r.Context())
	if err != nil {
		s.writeStoreError(w, "list quality profiles", err)
		return
	}
	// The default profile (the oldest) is the fallback for everything with a
	// dangling profile id, so deleting it would strand the fallback itself.
	if len(profiles) > 0 && profiles[0].ID == id {
		writeError(w, http.StatusConflict, "the default profile cannot be deleted")
		return
	}
	if err := s.st.DeleteQualityProfile(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete quality profile", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeProfileConflict maps a duplicate-name violation to a 409 the UI can
// put next to the name field. Everything else defers to the generic store
// error mapping.
func (s *server) writeProfileConflict(w http.ResponseWriter, msg string, err error) {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		writeError(w, http.StatusConflict, "a profile with that name already exists")
		return
	}
	s.writeStoreError(w, msg, err)
}
