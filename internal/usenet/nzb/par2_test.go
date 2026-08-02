package nzb_test

import (
	"testing"

	"github.com/watzon/caravan/internal/usenet/nzb"
)

func TestPar2Detection(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		isPar2   bool
		isVolume bool
		offset   int
		blocks   int
		setName  string
	}{
		{name: "index file", file: "release.par2", isPar2: true, setName: "release"},
		{name: "recovery volume", file: "release.vol000+01.par2", isPar2: true, isVolume: true, offset: 0, blocks: 1, setName: "release"},
		{name: "large recovery volume", file: "release.vol127+64.par2", isPar2: true, isVolume: true, offset: 127, blocks: 64, setName: "release"},
		{name: "uppercase extension", file: "RELEASE.VOL003+04.PAR2", isPar2: true, isVolume: true, offset: 3, blocks: 4, setName: "RELEASE"},
		{name: "dotted base name", file: "some.release-grp.vol012+08.par2", isPar2: true, isVolume: true, offset: 12, blocks: 8, setName: "some.release-grp"},
		{name: "media file", file: "release.mkv"},
		{name: "rar part", file: "release.part01.rar"},
		{name: "par2-looking but not", file: "release.par2.mkv"},
		{name: "vol pattern without par2 extension", file: "release.vol000+01.rar"},
		{name: "empty", file: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nzb.IsPar2(tt.file); got != tt.isPar2 {
				t.Errorf("IsPar2(%q) = %v, want %v", tt.file, got, tt.isPar2)
			}
			offset, blocks, ok := nzb.Par2Volume(tt.file)
			if ok != tt.isVolume {
				t.Fatalf("Par2Volume(%q) ok = %v, want %v", tt.file, ok, tt.isVolume)
			}
			if ok {
				if offset != tt.offset || blocks != tt.blocks {
					t.Errorf("Par2Volume(%q) = (%d, %d), want (%d, %d)", tt.file, offset, blocks, tt.offset, tt.blocks)
				}
			}
			if got := nzb.Par2SetName(tt.file); got != tt.setName {
				t.Errorf("Par2SetName(%q) = %q, want %q", tt.file, got, tt.setName)
			}
		})
	}
}

func TestPar2SetNameGroupsASet(t *testing.T) {
	// Grouping by set name is what lets a pipeline fetch only the volumes of
	// the set it actually needs when an NZB carries more than one.
	set := map[string][]string{}
	for _, f := range []string{
		"movie.par2", "movie.vol000+01.par2", "movie.vol001+02.par2",
		"sample.par2", "sample.vol000+01.par2",
		"movie.mkv", "sample.mkv",
	} {
		if name := nzb.Par2SetName(f); name != "" {
			set[name] = append(set[name], f)
		}
	}
	if got, want := len(set), 2; got != want {
		t.Fatalf("sets = %d (%v), want %d", got, set, want)
	}
	if got, want := len(set["movie"]), 3; got != want {
		t.Errorf("movie set = %d files, want %d", got, want)
	}
	if got, want := len(set["sample"]), 2; got != want {
		t.Errorf("sample set = %d files, want %d", got, want)
	}
}
