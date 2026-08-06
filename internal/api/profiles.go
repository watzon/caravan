package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

// qualityProfileJSON is the API shape of core.QualityProfile.
type qualityProfileJSON struct {
	ID                     int64                  `json:"id"`
	Name                   string                 `json:"name"`
	Cutoff                 string                 `json:"cutoff"`
	Items                  []string               `json:"items"`
	UpgradeAllowed         bool                   `json:"upgrade_allowed"`
	PreferredSources       []string               `json:"preferred_sources"`
	ProperRepackPreference string                 `json:"proper_repack_preference"`
	MinSeeders             int                    `json:"min_seeders"`
	MinSizeMB              int64                  `json:"min_size_mb"`
	MaxSizeMB              int64                  `json:"max_size_mb"`
	CustomFormats          []core.CustomFormat    `json:"custom_formats"`
	TVProfile              string                 `json:"tv_profile"`
	TVCompatibilityPolicy  string                 `json:"tv_compatibility_policy"`
	IsDefault              bool                   `json:"is_default"`
	Assignments            profileAssignmentsJSON `json:"assignments"`
	CreatedAt              string                 `json:"created_at"`
	UpdatedAt              string                 `json:"updated_at"`
}

type profileAssignmentsJSON struct {
	Libraries int64 `json:"libraries"`
	Movies    int64 `json:"movies"`
	Series    int64 `json:"series"`
}

// profileDecisionJSON explains a quality-profile score without exposing the
// scoring implementation to API consumers.
type profileDecisionJSON struct {
	Accepted      bool                     `json:"accepted"`
	ProfileID     int64                    `json:"profile_id"`
	ProfileName   string                   `json:"profile_name"`
	Score         int                      `json:"score"`
	Reason        string                   `json:"reason"`
	Contributions profileContributionsJSON `json:"contributions"`
}

type profileContributionsJSON struct {
	Quality         int `json:"quality"`
	Source          int `json:"source"`
	Proper          int `json:"proper"`
	Repack          int `json:"repack"`
	Seeders         int `json:"seeders"`
	CustomFormats   int `json:"custom_formats"`
	TVCompatibility int `json:"tv_compatibility"`
}

type profileTestRequest struct {
	Titles []string `json:"titles"`
}

type profileTestResultJSON struct {
	Title    string              `json:"title"`
	Parsed   parsedJSON          `json:"parsed"`
	Decision profileDecisionJSON `json:"decision"`
}

func qualityProfileDTO(p core.QualityProfile, isDefault bool, assignments store.QualityProfileReferenceCounts) qualityProfileJSON {
	return qualityProfileJSON{
		ID:                     p.ID,
		Name:                   p.Name,
		Cutoff:                 p.Cutoff,
		Items:                  p.Items,
		UpgradeAllowed:         p.UpgradeAllowed,
		PreferredSources:       p.PreferredSources,
		ProperRepackPreference: effectiveProperRepackPreference(p.ProperRepackPreference),
		MinSeeders:             p.MinSeeders,
		MinSizeMB:              p.MinSizeMB,
		MaxSizeMB:              p.MaxSizeMB,
		CustomFormats:          p.CustomFormats,
		TVProfile:              effectiveTVProfile(p.TVProfile),
		TVCompatibilityPolicy:  effectiveTVCompatibilityPolicy(p.TVCompatibilityPolicy),
		IsDefault:              isDefault,
		Assignments: profileAssignmentsJSON{
			Libraries: assignments.Libraries,
			Movies:    assignments.Movies,
			Series:    assignments.Series,
		},
		CreatedAt: jsonTime(p.CreatedAt),
		UpdatedAt: jsonTime(p.UpdatedAt),
	}
}

func (s *server) qualityProfileSummaryDTO(ctx context.Context, p core.QualityProfile, isDefault bool) (qualityProfileJSON, error) {
	assignments, err := s.st.GetQualityProfileReferenceCounts(ctx, p.ID)
	if err != nil {
		return qualityProfileJSON{}, err
	}
	return qualityProfileDTO(p, isDefault, assignments), nil
}

func profileDecisionDTO(r core.Release, p *core.QualityProfile) profileDecisionJSON {
	score, reject, contributions := wanted.ScoreReleaseWithContributions(r, p)
	reason := reject
	if reason == "" {
		reason = fmt.Sprintf("accepted by profile %q", p.Name)
	}
	return profileDecisionJSON{
		Accepted:    reject == "",
		ProfileID:   p.ID,
		ProfileName: p.Name,
		Score:       score,
		Reason:      reason,
		Contributions: profileContributionsJSON{
			Quality:         contributions.Quality,
			Source:          contributions.Source,
			Proper:          contributions.Proper,
			Repack:          contributions.Repack,
			Seeders:         contributions.Seeders,
			CustomFormats:   contributions.CustomFormats,
			TVCompatibility: contributions.TVCompatibility,
		},
	}
}

// profileRequest is the create/update body. Fields introduced after the
// original quality ladder may be omitted; their legacy-safe defaults apply.
type profileRequest struct {
	Name                   string              `json:"name"`
	Cutoff                 string              `json:"cutoff"`
	Items                  []string            `json:"items"`
	UpgradeAllowed         bool                `json:"upgrade_allowed"`
	PreferredSources       []string            `json:"preferred_sources"`
	ProperRepackPreference string              `json:"proper_repack_preference"`
	MinSeeders             int                 `json:"min_seeders"`
	MinSizeMB              int64               `json:"min_size_mb"`
	MaxSizeMB              int64               `json:"max_size_mb"`
	CustomFormats          []core.CustomFormat `json:"custom_formats"`
	TVProfile              string              `json:"tv_profile"`
	TVCompatibilityPolicy  string              `json:"tv_compatibility_policy"`
}

type qualityProfileExportJSON struct {
	Version        int              `json:"version"`
	DefaultProfile string           `json:"default_profile"`
	Profiles       []profileRequest `json:"profiles"`
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
	if preference := body.ProperRepackPreference; preference != "" &&
		preference != core.ProperRepackPreferencePrefer &&
		preference != core.ProperRepackPreferenceNeutral {
		return "proper_repack_preference must be prefer or neutral", false
	}
	if body.MinSeeders < 0 {
		return "min_seeders must not be negative", false
	}
	if body.MinSizeMB < 0 {
		return "min_size_mb must not be negative", false
	}
	if body.MaxSizeMB < 0 {
		return "max_size_mb must not be negative", false
	}
	if body.MinSizeMB > 0 && body.MaxSizeMB > 0 && body.MaxSizeMB < body.MinSizeMB {
		return "max_size_mb must be at least min_size_mb", false
	}
	sources := make(map[string]bool, len(body.PreferredSources))
	for _, source := range body.PreferredSources {
		if core.SourceRank(source) >= len(core.SourceLadder) {
			return "preferred_sources must contain known sources: " + source, false
		}
		if sources[source] {
			return "preferred_sources must not repeat a source: " + source, false
		}
		sources[source] = true
	}
	formatNames := make(map[string]bool, len(body.CustomFormats))
	for _, format := range body.CustomFormats {
		name := strings.TrimSpace(format.Name)
		if name == "" {
			return "custom_formats must have a name", false
		}
		nameKey := strings.ToLower(name)
		if formatNames[nameKey] {
			return "custom_formats must not repeat a name: " + name, false
		}
		formatNames[nameKey] = true
		if format.Score == 0 {
			return "custom_formats score must not be zero: " + name, false
		}
		if format.Score < -wanted.MaxCustomFormatScore || format.Score > wanted.MaxCustomFormatScore {
			return fmt.Sprintf("custom_formats score must be between -%d and %d: %s",
				wanted.MaxCustomFormatScore, wanted.MaxCustomFormatScore, name), false
		}
		if !hasNonEmptyTerm(format.IncludeTerms) {
			return "custom_formats must include at least one term: " + name, false
		}
		terms := make(map[string]bool, len(format.IncludeTerms)+len(format.ExcludeTerms))
		for _, term := range append(format.IncludeTerms, format.ExcludeTerms...) {
			term = strings.TrimSpace(term)
			if term == "" {
				return "custom_formats terms must not be blank: " + name, false
			}
			termKey := strings.ToLower(term)
			if terms[termKey] {
				return "custom_formats must not repeat a term: " + term, false
			}
			terms[termKey] = true
		}
	}
	if !isKnownTVProfile(body.TVProfile) {
		return "tv_profile must be safe or capable", false
	}
	if policy := body.TVCompatibilityPolicy; policy != "" &&
		policy != core.TVCompatibilityPolicyIgnore &&
		policy != core.TVCompatibilityPolicyPrefer &&
		policy != core.TVCompatibilityPolicyRequire {
		return "tv_compatibility_policy must be ignore, prefer, or require", false
	}
	return "", true
}

func hasNonEmptyTerm(terms []string) bool {
	for _, term := range terms {
		if strings.TrimSpace(term) != "" {
			return true
		}
	}
	return false
}

func isKnownTVProfile(id string) bool {
	if id == "" {
		return true
	}
	for _, profile := range core.TVProfiles() {
		if id == profile.ID {
			return true
		}
	}
	return false
}

func profileFromRequest(id int64, body profileRequest) core.QualityProfile {
	return core.QualityProfile{
		ID:                     id,
		Name:                   strings.TrimSpace(body.Name),
		Cutoff:                 body.Cutoff,
		Items:                  body.Items,
		UpgradeAllowed:         body.UpgradeAllowed,
		PreferredSources:       body.PreferredSources,
		ProperRepackPreference: effectiveProperRepackPreference(body.ProperRepackPreference),
		MinSeeders:             body.MinSeeders,
		MinSizeMB:              body.MinSizeMB,
		MaxSizeMB:              body.MaxSizeMB,
		CustomFormats:          body.CustomFormats,
		TVProfile:              effectiveTVProfile(body.TVProfile),
		TVCompatibilityPolicy:  effectiveTVCompatibilityPolicy(body.TVCompatibilityPolicy),
	}
}

func effectiveProperRepackPreference(preference string) string {
	if preference == "" {
		return core.ProperRepackPreferencePrefer
	}
	return preference
}

func profileRequestFromProfile(p core.QualityProfile) profileRequest {
	return profileRequest{
		Name:                   p.Name,
		Cutoff:                 p.Cutoff,
		Items:                  p.Items,
		UpgradeAllowed:         p.UpgradeAllowed,
		PreferredSources:       p.PreferredSources,
		ProperRepackPreference: effectiveProperRepackPreference(p.ProperRepackPreference),
		MinSeeders:             p.MinSeeders,
		MinSizeMB:              p.MinSizeMB,
		MaxSizeMB:              p.MaxSizeMB,
		CustomFormats:          p.CustomFormats,
		TVProfile:              effectiveTVProfile(p.TVProfile),
		TVCompatibilityPolicy:  effectiveTVCompatibilityPolicy(p.TVCompatibilityPolicy),
	}
}
func effectiveTVProfile(id string) string {
	if id == "" {
		return core.TVProfileSafe
	}
	return id
}

func effectiveTVCompatibilityPolicy(policy string) string {
	if policy == "" {
		return core.TVCompatibilityPolicyIgnore
	}
	return policy
}

func (s *server) handleListQualityProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.st.ListQualityProfiles(r.Context())
	if err != nil {
		s.writeStoreError(w, "list quality profiles", err)
		return
	}
	defaultProfile, err := s.st.GetDefaultQualityProfile(r.Context())
	if err != nil {
		s.writeStoreError(w, "get default quality profile", err)
		return
	}
	out := make([]qualityProfileJSON, 0, len(profiles))
	for _, p := range profiles {
		dto, err := s.qualityProfileSummaryDTO(r.Context(), p, p.ID == defaultProfile.ID)
		if err != nil {
			s.writeStoreError(w, "count quality profile assignments", err)
			return
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

// handleExportQualityProfiles returns portable policy definitions. Assignments
// are not exported because they are local library state.
func (s *server) handleExportQualityProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.st.ListQualityProfiles(r.Context())
	if err != nil {
		s.writeStoreError(w, "list quality profiles for export", err)
		return
	}
	defaultProfile, err := s.st.GetDefaultQualityProfile(r.Context())
	if err != nil {
		s.writeStoreError(w, "get default quality profile for export", err)
		return
	}
	out := make([]profileRequest, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, profileRequestFromProfile(profile))
	}
	w.Header().Set("Content-Disposition", `attachment; filename="quality-profiles.json"`)
	writeJSON(w, http.StatusOK, qualityProfileExportJSON{
		Version:        1,
		DefaultProfile: defaultProfile.Name,
		Profiles:       out,
	})
}

// handleImportQualityProfiles validates the entire document before it starts
// the transactional store update, so a bad profile cannot partially import.
func (s *server) handleImportQualityProfiles(w http.ResponseWriter, r *http.Request) {
	var body qualityProfileExportJSON
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Version != 1 {
		writeError(w, http.StatusBadRequest, "quality profile import version must be 1")
		return
	}
	defaultName := strings.TrimSpace(body.DefaultProfile)
	if defaultName == "" {
		writeError(w, http.StatusBadRequest, "default_profile is required")
		return
	}
	profiles := make([]core.QualityProfile, 0, len(body.Profiles))
	names := make(map[string]bool, len(body.Profiles))
	for _, profile := range body.Profiles {
		if msg, ok := validateProfile(profile); !ok {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		p := profileFromRequest(0, profile)
		if names[p.Name] {
			writeError(w, http.StatusBadRequest, "profiles must not repeat a name: "+p.Name)
			return
		}
		names[p.Name] = true
		profiles = append(profiles, p)
	}
	if !names[defaultName] {
		writeError(w, http.StatusBadRequest, "default_profile must name an imported profile")
		return
	}
	if err := s.st.ImportQualityProfiles(r.Context(), profiles, defaultName); err != nil {
		s.writeStoreError(w, "import quality profiles", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": len(profiles)})
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
	p := profileFromRequest(0, body)
	if err := s.st.CreateQualityProfile(r.Context(), &p); err != nil {
		s.writeProfileConflict(w, "create quality profile", err)
		return
	}
	dto, err := s.qualityProfileSummaryDTO(r.Context(), p, false)
	if err != nil {
		s.writeStoreError(w, "count quality profile assignments", err)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
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
	p := profileFromRequest(id, body)
	if err := s.st.UpdateQualityProfile(r.Context(), &p); err != nil {
		s.writeProfileConflict(w, "update quality profile", err)
		return
	}
	stored, err := s.st.GetQualityProfile(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get quality profile", err)
		return
	}
	defaultProfile, err := s.st.GetDefaultQualityProfile(r.Context())
	if err != nil {
		s.writeStoreError(w, "get default quality profile", err)
		return
	}
	dto, err := s.qualityProfileSummaryDTO(r.Context(), *stored, stored.ID == defaultProfile.ID)
	if err != nil {
		s.writeStoreError(w, "count quality profile assignments", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *server) handleSetDefaultQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.st.SetDefaultQualityProfile(r.Context(), id); err != nil {
		s.writeStoreError(w, "set default quality profile", err)
		return
	}
	p, err := s.st.GetQualityProfile(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get quality profile", err)
		return
	}
	dto, err := s.qualityProfileSummaryDTO(r.Context(), *p, true)
	if err != nil {
		s.writeStoreError(w, "count quality profile assignments", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *server) handleTestQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body profileTestRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	profile, err := s.st.GetQualityProfile(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get quality profile", err)
		return
	}

	results := make([]profileTestResultJSON, 0, len(body.Titles))
	for _, title := range body.Titles {
		release := core.Release{Title: title, Parsed: parse.Parse(title)}
		results = append(results, profileTestResultJSON{
			Title:    title,
			Parsed:   parsedDTO(release.Parsed),
			Decision: profileDecisionDTO(release, profile),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *server) handleDeleteQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteQualityProfile(r.Context(), id); err != nil {
		var conflict *store.QualityProfileDeleteConflict
		if errors.As(err, &conflict) {
			if conflict.Default {
				writeError(w, http.StatusConflict, "the system default profile cannot be deleted; choose another default first")
				return
			}
			writeError(w, http.StatusConflict, profileReferenceMessage(conflict.References))
			return
		}
		s.writeStoreError(w, "delete quality profile", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func profileReferenceMessage(refs store.QualityProfileReferenceCounts) string {
	return fmt.Sprintf("quality profile is still referenced by %s, %s, and %s",
		profileReferenceCount(refs.Libraries, "library", "libraries"),
		profileReferenceCount(refs.Movies, "movie", "movies"),
		profileReferenceCount(refs.Series, "series", "series"))
}

func profileReferenceCount(n int64, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
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
