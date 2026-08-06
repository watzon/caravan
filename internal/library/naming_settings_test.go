package library

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/store"
)

func TestNamingSettingsKeepDefaultsWithoutRows(t *testing.T) {
	h := newHarness(t)
	if got, want := h.mgr.movieFileName("Big Buck Bunny", 2008, "", ".mkv"), "Big Buck Bunny (2008).mkv"; got != want {
		t.Errorf("movie default = %q, want %q", got, want)
	}
	if got, want := h.mgr.episodeFileName("Planet Earth II", 2016, 1, []int{1}, "Islands", ".mkv"), "Planet Earth II (2016) - S01E01 - Islands.mkv"; got != want {
		t.Errorf("episode default = %q, want %q", got, want)
	}
}

func TestNamingSettingsRenderSanitizedTokens(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for key, value := range map[string]string{
		store.SettingMovieFolderFormat:  "{year} - {title}",
		store.SettingMovieFileFormat:    "{title}{edition}{year}",
		store.SettingSeriesFolderFormat: "TV - {title}",
		store.SettingSeasonFolderFormat: "S{season:02}",
		store.SettingEpisodeFileFormat:  "{series}.{episode}{title}",
	} {
		if err := h.st.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := h.mgr.movieDir(stockMovieLib(), "A/B", 2008), "library/Movies/ (2008) - AB"; got != want {
		t.Errorf("movie dir = %q, want %q", got, want)
	}
	if got, want := h.mgr.episodeFileName("Show/Name", 2016, 1, []int{2}, "Part: one", ".mkv"), "ShowName.S01E02 - Part one.mkv"; got != want {
		t.Errorf("episode = %q, want %q", got, want)
	}
}

func TestValidateNamingSettings(t *testing.T) {
	for name, settings := range map[string]map[string]string{
		"unknown token":    {store.SettingMovieFolderFormat: "{title} {bad}"},
		"missing identity": {store.SettingMovieFileFormat: "{edition}"},
		"empty":            {store.SettingSeriesFolderFormat: ""},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateNamingSettings(settings); err == nil {
				t.Fatal("ValidateNamingSettings accepted invalid format")
			}
		})
	}
}
