package api

import (
	"net/http"

	"github.com/watzon/caravan/internal/core"
)

// tvProfileJSON is one built-in playback-target capability description. The
// wire name stays stable while the product calls these playback targets.
type tvProfileJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	VideoCodecs []string `json:"video_codecs"`
	MaxBitDepth int      `json:"max_bit_depth"`
	AudioCodecs []string `json:"audio_codecs"`
	Containers  []string `json:"containers"`
	MaxQuality  string   `json:"max_quality"`
}

// compatibilityJSON is the verdict of an item's playback target on one
// release or imported file.
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

func playbackTarget(profile *core.QualityProfile) core.TVProfile {
	if profile == nil {
		return core.ResolveTVProfile("")
	}
	return core.ResolveTVProfile(profile.TVProfile)
}

func (s *server) handleListTVProfiles(w http.ResponseWriter, r *http.Request) {
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
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}
