package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// tvProfileJSON is one built-in target-set capability description (SPEC §8).
// The profiles are code-owned, so this endpoint is read-only: the only thing a
// user chooses is which one is active, and that rides in PUT /settings under
// store.SettingTVProfile.
type tvProfileJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	VideoCodecs []string `json:"video_codecs"`
	MaxBitDepth int      `json:"max_bit_depth"`
	AudioCodecs []string `json:"audio_codecs"`
	Containers  []string `json:"containers"`
	MaxQuality  string   `json:"max_quality"`
	// Active marks the profile the compatibility fields on releases and media
	// files were computed against, so the UI never has to resolve the fallback
	// itself.
	Active bool `json:"active"`
}

// compatibilityJSON is the verdict of the active TV profile on one release or
// one imported file. It is advisory in exactly the way the picker's flags are:
// nothing is hidden, refused or reordered because of it.
type compatibilityJSON struct {
	// Verdict is "unknown", "compatible", "needs-remux" or "incompatible".
	Verdict string `json:"verdict"`
	// Reasons are human-readable, worst first; empty unless something is off.
	Reasons []string `json:"reasons"`
}

func compatibilityDTO(c core.TVCompatibility) compatibilityJSON {
	reasons := c.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	return compatibilityJSON{Verdict: c.Verdict, Reasons: reasons}
}

// activeTVProfile resolves the configured profile. A missing or unreadable
// setting falls back to the safe default rather than failing the request: the
// compatibility fields are advisory, and a picker that 500s because a
// preference row is absent would be worse than one that assumes the cautious
// answer.
func (s *server) activeTVProfile(ctx context.Context) core.TVProfile {
	id, err := s.st.GetSetting(ctx, store.SettingTVProfile)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("read tv profile", "error", err)
	}
	return core.ResolveTVProfile(id)
}

func (s *server) handleListTVProfiles(w http.ResponseWriter, r *http.Request) {
	active := s.activeTVProfile(r.Context())

	profiles := core.TVProfiles()
	out := make([]tvProfileJSON, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, tvProfileJSON{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			VideoCodecs: p.VideoCodecs,
			MaxBitDepth: p.MaxBitDepth,
			AudioCodecs: p.AudioCodecs,
			Containers:  p.Containers,
			MaxQuality:  p.MaxQuality,
			Active:      p.ID == active.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}
