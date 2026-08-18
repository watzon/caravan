package convert

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/store"
)

const (
	DefaultVideoPreset      = "veryfast"
	DefaultVideoCRF         = 20
	DefaultAudioBitrateKbps = 192
)

var videoPresets = map[string]bool{
	"ultrafast": true,
	"superfast": true,
	"veryfast":  true,
	"faster":    true,
	"fast":      true,
	"medium":    true,
	"slow":      true,
	"slower":    true,
	"veryslow":  true,
}

// EncodingSettings controls ffmpeg's cost/quality tradeoffs when a plan must
// re-encode a stream. The playback target still owns codecs, container and
// maximum resolution; remuxes ignore these settings and copy every selected
// stream.
type EncodingSettings struct {
	VideoPreset      string
	VideoCRF         int
	AudioBitrateKbps int
}

// DefaultEncodingSettings preserves Caravan's original ffmpeg behaviour.
func DefaultEncodingSettings() EncodingSettings {
	return EncodingSettings{
		VideoPreset:      DefaultVideoPreset,
		VideoCRF:         DefaultVideoCRF,
		AudioBitrateKbps: DefaultAudioBitrateKbps,
	}
}

// ResolveEncodingSettings applies code defaults to absent or invalid values.
// It also returns the first invalid-value error so the settings API can reject
// bad input while a worker reading an old or hand-edited database stays usable.
func ResolveEncodingSettings(values map[string]string) (EncodingSettings, error) {
	settings := DefaultEncodingSettings()
	var firstErr error

	if raw, ok := values[store.SettingConvertVideoPreset]; ok {
		value := strings.TrimSpace(raw)
		if videoPresets[value] {
			settings.VideoPreset = value
		} else {
			firstErr = fmt.Errorf("invalid %s", store.SettingConvertVideoPreset)
		}
	}
	if raw, ok := values[store.SettingConvertVideoCRF]; ok {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && value >= 0 && value <= 51 {
			settings.VideoCRF = value
		} else if firstErr == nil {
			firstErr = fmt.Errorf("invalid %s: expected an integer from 0 to 51", store.SettingConvertVideoCRF)
		}
	}
	if raw, ok := values[store.SettingConvertAudioBitrateKbps]; ok {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && value >= 64 && value <= 512 {
			settings.AudioBitrateKbps = value
		} else if firstErr == nil {
			firstErr = fmt.Errorf("invalid %s: expected an integer from 64 to 512", store.SettingConvertAudioBitrateKbps)
		}
	}
	return settings, firstErr
}
