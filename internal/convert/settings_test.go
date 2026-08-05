package convert

import (
	"testing"

	"github.com/watzon/caravan/internal/store"
)

func TestResolveEncodingSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got, err := ResolveEncodingSettings(nil)
		if err != nil {
			t.Fatalf("ResolveEncodingSettings: %v", err)
		}
		if want := DefaultEncodingSettings(); got != want {
			t.Fatalf("settings = %+v, want defaults %+v", got, want)
		}
	})

	t.Run("stored values", func(t *testing.T) {
		got, err := ResolveEncodingSettings(map[string]string{
			store.SettingConvertVideoPreset:      " slow ",
			store.SettingConvertVideoCRF:         "18",
			store.SettingConvertAudioBitrateKbps: "256",
		})
		if err != nil {
			t.Fatalf("ResolveEncodingSettings: %v", err)
		}
		want := EncodingSettings{VideoPreset: "slow", VideoCRF: 18, AudioBitrateKbps: 256}
		if got != want {
			t.Fatalf("settings = %+v, want %+v", got, want)
		}
	})

	t.Run("invalid fields use their defaults", func(t *testing.T) {
		got, err := ResolveEncodingSettings(map[string]string{
			store.SettingConvertVideoPreset:      "turbo",
			store.SettingConvertVideoCRF:         "19",
			store.SettingConvertAudioBitrateKbps: "513",
		})
		if err == nil {
			t.Fatal("ResolveEncodingSettings accepted invalid values")
		}
		want := DefaultEncodingSettings()
		want.VideoCRF = 19
		if got != want {
			t.Fatalf("settings = %+v, want valid fields plus defaults %+v", got, want)
		}
	})
}
