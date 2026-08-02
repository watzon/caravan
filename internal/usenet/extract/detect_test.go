package extract

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		dirs    []string
		want    []Set
		wantErr error
	}{
		{
			name:  "nothing to extract",
			files: []string{"movie.mkv", "movie.nfo", "movie.par2"},
			want:  nil,
		},
		{
			name:  "single rar",
			files: []string{"release.rar", "release.par2"},
			want:  []Set{{Kind: KindRAR, Volumes: []string{"release.rar"}}},
		},
		{
			name:  "modern part naming",
			files: []string{"rel.part03.rar", "rel.part01.rar", "rel.part02.rar"},
			want: []Set{{Kind: KindRAR, Volumes: []string{
				"rel.part01.rar", "rel.part02.rar", "rel.part03.rar",
			}}},
		},
		{
			name:  "part numbers wider than two digits",
			files: []string{"rel.part001.rar", "rel.part002.rar"},
			want:  []Set{{Kind: KindRAR, Volumes: []string{"rel.part001.rar", "rel.part002.rar"}}},
		},
		{
			name:  "legacy r00 naming",
			files: []string{"rel.r01", "rel.rar", "rel.r00"},
			want:  []Set{{Kind: KindRAR, Volumes: []string{"rel.rar", "rel.r00", "rel.r01"}}},
		},
		{
			name:  "legacy naming past r99",
			files: []string{"rel.rar", "rel.s00"},
			// r00..r99 come first, so an s00 with no r-volumes is a hole.
			wantErr: ErrIncomplete,
		},
		{
			name:  "zip",
			files: []string{"rel.zip", "rel.par2"},
			want:  []Set{{Kind: KindZip, Volumes: []string{"rel.zip"}}},
		},
		{
			name:  "case insensitive extensions",
			files: []string{"REL.RAR", "REL.R00"},
			want:  []Set{{Kind: KindRAR, Volumes: []string{"REL.RAR", "REL.R00"}}},
		},
		{
			name:  "two independent sets, stable order",
			files: []string{"b.zip", "a.part01.rar", "a.part02.rar"},
			want: []Set{
				{Kind: KindRAR, Volumes: []string{"a.part01.rar", "a.part02.rar"}},
				{Kind: KindZip, Volumes: []string{"b.zip"}},
			},
		},
		{
			name:  "a bare rar next to a part set belongs to it",
			files: []string{"rel.rar", "rel.part01.rar", "rel.part02.rar"},
			want:  []Set{{Kind: KindRAR, Volumes: []string{"rel.part01.rar", "rel.part02.rar"}}},
		},
		{
			name:    "gap in the part numbering",
			files:   []string{"rel.part01.rar", "rel.part03.rar"},
			wantErr: ErrIncomplete,
		},
		{
			name:    "part set that does not start at one",
			files:   []string{"rel.part02.rar", "rel.part03.rar"},
			wantErr: ErrIncomplete,
		},
		{
			name:    "gap in the legacy numbering",
			files:   []string{"rel.rar", "rel.r00", "rel.r02"},
			wantErr: ErrIncomplete,
		},
		{
			name:    "legacy continuation with no first volume",
			files:   []string{"rel.r00", "rel.r01"},
			wantErr: ErrIncomplete,
		},
		{
			name: "spanned zip parts are not mistaken for rar volumes",
			// A .z01 alongside a .zip is a spanned zip, not an old rar volume,
			// and there is no .rar for it to continue.
			files: []string{"rel.zip", "rel.z01", "rel.z02"},
			want:  []Set{{Kind: KindZip, Volumes: []string{"rel.zip"}}},
		},
		{
			name:  "archives in subdirectories are left alone",
			files: []string{"movie.mkv"},
			dirs:  []string{"Sample"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tt.dirs {
				if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
				// Something inside the subdirectory that would be detected if
				// the scan recursed.
				writeFileT(t, filepath.Join(dir, d, "sample.rar"), "not scanned")
			}
			for _, f := range tt.files {
				writeFileT(t, filepath.Join(dir, f), f)
			}

			got, err := Detect(dir)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Detect err = %v, want %v", err, tt.wantErr)
				}
				var e *Error
				if !errors.As(err, &e) {
					t.Errorf("Detect err = %T, want *Error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Detect = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDetectFixtures(t *testing.T) {
	m := loadManifest(t)
	for name, fix := range m {
		t.Run(name, func(t *testing.T) {
			dir, _ := stageFixture(t, name)
			sets, err := Detect(dir)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if len(sets) != 1 {
				t.Fatalf("Detect = %+v, want exactly one set", sets)
			}
			if sets[0].Kind != KindRAR {
				t.Errorf("Kind = %v, want rar", sets[0].Kind)
			}
			if !reflect.DeepEqual(sets[0].Volumes, fix.Volumes) {
				t.Errorf("Volumes = %v, want %v", sets[0].Volumes, fix.Volumes)
			}
		})
	}
}

func TestDetectMissingDirectory(t *testing.T) {
	_, err := Detect(filepath.Join(t.TempDir(), "gone"))
	if err == nil {
		t.Fatal("Detect on a missing directory succeeded")
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Errorf("Detect err = %T, want *Error", err)
	}
}

func TestKindString(t *testing.T) {
	for _, tt := range []struct {
		k    Kind
		want string
	}{
		{KindRAR, "rar"},
		{KindZip, "zip"},
		{Kind(9), "Kind(9)"},
	} {
		if got := tt.k.String(); got != tt.want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(tt.k), got, tt.want)
		}
	}
}
