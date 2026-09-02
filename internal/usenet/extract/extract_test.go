package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// manifest mirrors testdata/MANIFEST.json, which the fixture generator writes
// alongside the rar files. Expectations live there rather than in this file so
// regenerating the fixtures cannot leave the tests asserting stale sizes.
type manifest map[string]struct {
	Volumes []string `json:"volumes"`
	Files   map[string]struct {
		Size   int    `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "MANIFEST.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

// stageFixture copies a manifest fixture's volumes into a fresh directory and
// returns the directory and the fixture entry.
func stageFixture(t *testing.T, name string) (string, manifest) {
	t.Helper()
	m := loadManifest(t)
	fix, ok := m[name]
	if !ok {
		t.Fatalf("no fixture %q in MANIFEST.json", name)
	}
	dir := t.TempDir()
	for _, v := range fix.Volumes {
		b, err := os.ReadFile(filepath.Join("testdata", v))
		if err != nil {
			t.Fatalf("read fixture %s: %v", v, err)
		}
		if err := os.WriteFile(filepath.Join(dir, v), b, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", v, err)
		}
	}
	return dir, m
}

type zipEntry struct {
	name  string
	data  string
	mode  fs.FileMode
	flags uint16
}

func makeZip(t *testing.T, path string, entries ...zipEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		h.Flags = e.flags
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", e.name, err)
		}
		if _, err := io.WriteString(w, e.data); err != nil {
			t.Fatalf("write zip entry %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}

// snapshot records every path under root and the hash of its contents, so a
// test can assert a failed extract changed precisely nothing.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			out[filepath.ToSlash(rel)] = "dir"
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writeFileT(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestExtractZip(t *testing.T) {
	dir := t.TempDir()
	makeZip(t, filepath.Join(dir, "release.zip"),
		zipEntry{name: "movie.mkv", data: strings.Repeat("video", 400)},
		zipEntry{name: "subs/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "subs/movie.srt", data: "1\n00:00:01 --> 00:00:02\nhi\n"},
	)
	writeFileT(t, filepath.Join(dir, "release.par2"), "par2 header")
	writeFileT(t, filepath.Join(dir, "release.vol000+01.PAR2"), "par2 volume")
	writeFileT(t, filepath.Join(dir, "release.nfo"), "nfo")

	res, err := Extract(context.Background(), dir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if got, want := res.Archives, []string{"release.zip"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Archives = %v, want %v", got, want)
	}
	sort.Strings(res.Files)
	if got, want := res.Files, []string{"movie.mkv", "subs/movie.srt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
	if want := int64(400*len("video") + len("1\n00:00:01 --> 00:00:02\nhi\n")); res.Bytes != want {
		t.Errorf("Bytes = %d, want %d", res.Bytes, want)
	}

	if got := string(mustRead(t, filepath.Join(dir, "movie.mkv"))); got != strings.Repeat("video", 400) {
		t.Errorf("movie.mkv contents wrong (%d bytes)", len(got))
	}
	if got := string(mustRead(t, filepath.Join(dir, "subs", "movie.srt"))); got != "1\n00:00:01 --> 00:00:02\nhi\n" {
		t.Errorf("subs/movie.srt = %q", got)
	}

	// The archive and both par2 files go; anything else stays.
	sort.Strings(res.Removed)
	want := []string{"release.par2", "release.vol000+01.PAR2", "release.zip"}
	if !reflect.DeepEqual(res.Removed, want) {
		t.Errorf("Removed = %v, want %v", res.Removed, want)
	}
	for _, gone := range want {
		if _, err := os.Stat(filepath.Join(dir, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s still present after extract", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "release.nfo")); err != nil {
		t.Errorf("release.nfo should have been left alone: %v", err)
	}

	// No staging debris.
	for _, e := range mustReadDir(t, dir) {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			t.Errorf("staging directory %s left behind", e.Name())
		}
	}
}

func TestExtractZipSlipRejected(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{"parent traversal", "../evil.txt"},
		{"nested traversal", "subs/../../evil.txt"},
		{"absolute path", "/tmp/evil.txt"},
		{"windows separator traversal", `..\evil.txt`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "download")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			makeZip(t, filepath.Join(dir, "evil.zip"),
				zipEntry{name: "movie.mkv", data: "harmless"},
				zipEntry{name: tt.entry, data: "pwned"},
			)
			before := snapshot(t, parent)

			_, err := Extract(context.Background(), dir)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("Extract err = %v, want ErrUnsafePath", err)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("Extract err = %T, want *Error", err)
			}
			if e.Archive != "evil.zip" || e.Entry != tt.entry {
				t.Errorf("Error{Archive: %q, Entry: %q}, want {evil.zip, %q}", e.Archive, e.Entry, tt.entry)
			}
			if after := snapshot(t, parent); !reflect.DeepEqual(before, after) {
				t.Errorf("tree changed after rejected extract:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func TestExtractZipEncryptedRejected(t *testing.T) {
	dir := t.TempDir()
	makeZip(t, filepath.Join(dir, "locked.zip"),
		zipEntry{name: "movie.mkv", data: "ciphertext", flags: zipEncrypted},
	)
	before := snapshot(t, dir)

	_, err := Extract(context.Background(), dir)
	if !errors.Is(err, ErrEncrypted) {
		t.Fatalf("Extract err = %v, want ErrEncrypted", err)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("directory changed after rejected extract")
	}
}

func TestExtractZipSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	makeZip(t, filepath.Join(dir, "sneaky.zip"),
		zipEntry{name: "link", data: "/etc/passwd", mode: fs.ModeSymlink | 0o777},
	)
	before := snapshot(t, dir)

	_, err := Extract(context.Background(), dir)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Extract err = %v, want ErrUnsafePath", err)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("directory changed after rejected extract")
	}
}

func TestExtractRARSingleVolume(t *testing.T) {
	dir, m := stageFixture(t, "single")
	writeFileT(t, filepath.Join(dir, "single.par2"), "par2")

	res, err := Extract(context.Background(), dir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFixtureFiles(t, dir, m["single"].Files)

	if got, want := res.Archives, []string{"single.rar"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Archives = %v, want %v", got, want)
	}
	sort.Strings(res.Removed)
	if got, want := res.Removed, []string{"single.par2", "single.rar"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Removed = %v, want %v", got, want)
	}
}

func TestExtractRARMultiVolume(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
	}{
		{"modern part naming", "multi-new"},
		{"legacy r00 naming", "multi-old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, m := stageFixture(t, tt.fixture)
			fix := m[tt.fixture]

			res, err := Extract(context.Background(), dir)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			// A file split across volumes has to come out as one stream, so
			// the size and hash checks here are the real assertion.
			assertFixtureFiles(t, dir, fix.Files)

			if !reflect.DeepEqual(res.Archives, fix.Volumes) {
				t.Errorf("Archives = %v, want %v", res.Archives, fix.Volumes)
			}
			for _, v := range fix.Volumes {
				if _, err := os.Stat(filepath.Join(dir, v)); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("volume %s survived a verified extract", v)
				}
			}
		})
	}
}

// TestExtractRARMixedWidthVolumeNames covers a real posting habit: volumes
// named part01…part09, then part010, the poster appended an unpadded counter to
// "part0". The successor name rardecode infers ("part02.rar") does not exist
// under that spelling, and the set must still extract, because the part number
// is the volume's identity, not its width.
func TestExtractRARMixedWidthVolumeNames(t *testing.T) {
	dir, m := stageFixture(t, "multi-new")
	fix := m["multi-new"]

	renames := map[string]string{
		"multi.part02.rar": "multi.part002.rar",
		"multi.part03.rar": "multi.part0003.rar",
	}
	for from, to := range renames {
		if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)); err != nil {
			t.Fatalf("rename %s: %v", from, err)
		}
	}

	res, err := Extract(context.Background(), dir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFixtureFiles(t, dir, fix.Files)

	want := []string{"multi.part01.rar", "multi.part002.rar", "multi.part0003.rar"}
	if !reflect.DeepEqual(res.Archives, want) {
		t.Errorf("Archives = %v, want %v", res.Archives, want)
	}
	for _, v := range want {
		if _, err := os.Stat(filepath.Join(dir, v)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("volume %s survived a verified extract", v)
		}
	}
}

// TestExtractRARScrambledVolumeNames is the failure a real release hit: the
// name a volume was posted under is not the volume it holds. The multi-mixed
// fixture packs both defects into four volumes: part1 is a digit narrower than
// the rest, and part03/part04 hold each other's volumes.
//
// Reading them in name order does not fail cleanly. Each file exists and is a
// valid rar volume, so the decoder gets several megabytes into the payload
// before the volume number in a header contradicts the one it asked for, and
// what it has written by then is the wrong bytes. The hash check below is the
// assertion that matters; without volume-number resolution this test fails with
// rardecode's "bad volume number".
func TestExtractRARScrambledVolumeNames(t *testing.T) {
	dir, m := stageFixture(t, "multi-mixed")
	fix := m["multi-mixed"]

	res, err := Extract(context.Background(), dir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFixtureFiles(t, dir, fix.Files)

	if !reflect.DeepEqual(res.Archives, fix.Volumes) {
		t.Errorf("Archives = %v, want %v", res.Archives, fix.Volumes)
	}
	for _, v := range fix.Volumes {
		if _, err := os.Stat(filepath.Join(dir, v)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("volume %s survived a verified extract", v)
		}
	}
}

// The mapping under that, asserted directly: the archive's own record of which
// volume it is beats the number in its name, and a set that records nothing
// falls back to the names unchanged.
func TestResolveVolumesPrefersTheNumberTheArchiveRecords(t *testing.T) {
	t.Run("rar5 records its volume number", func(t *testing.T) {
		dir, m := stageFixture(t, "multi-mixed")
		vs := resolveVolumes(dir, m["multi-mixed"].Volumes)

		want := map[int]string{
			1: "mixed.part1.rar",
			2: "mixed.part02.rar",
			3: "mixed.part04.rar", // holds volume 3 despite its name
			4: "mixed.part03.rar", // holds volume 4 despite its name
		}
		if !reflect.DeepEqual(vs.byNum, want) {
			t.Fatalf("byNum = %v, want %v", vs.byNum, want)
		}
		if got, wantFirst := vs.first(), filepath.Join(dir, "mixed.part1.rar"); got != wantFirst {
			t.Errorf("first() = %s, want %s", got, wantFirst)
		}
	})

	t.Run("rar4 has no such field and keeps its names", func(t *testing.T) {
		dir, m := stageFixture(t, "multi-new")
		vs := resolveVolumes(dir, m["multi-new"].Volumes)

		want := map[int]string{1: "multi.part01.rar", 2: "multi.part02.rar", 3: "multi.part03.rar"}
		if !reflect.DeepEqual(vs.byNum, want) {
			t.Fatalf("byNum = %v, want %v", vs.byNum, want)
		}
	})

	t.Run("legacy naming is left entirely to the decoder", func(t *testing.T) {
		dir, m := stageFixture(t, "multi-old")
		vs := resolveVolumes(dir, m["multi-old"].Volumes)

		// No part numbers to key on, so nothing is mapped and Open passes the
		// decoder's own names straight through.
		if len(vs.byNum) != 0 {
			t.Fatalf("byNum = %v, want nothing mapped", vs.byNum)
		}
		if got, want := vs.first(), filepath.Join(dir, "old.rar"); got != want {
			t.Errorf("first() = %s, want %s", got, want)
		}
	})
}

// A volume number this set does not have reads as "not there" rather than as
// whichever file happens to bear the name. It is how the decoder learns a set
// has ended, and handing it a wrong volume instead is what turned a misnamed
// release into a corrupt file.
func TestVolumeSetOpenRefusesANumberTheSetDoesNotHave(t *testing.T) {
	dir, m := stageFixture(t, "multi-mixed")
	vs := resolveVolumes(dir, m["multi-mixed"].Volumes)

	if _, err := vs.Open(filepath.Join(dir, "mixed.part5.rar")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open(part5) = %v, want a not-exist error", err)
	}
	// And a number it does have opens the file that holds it, whatever that
	// file is called.
	f, err := vs.Open(filepath.Join(dir, "mixed.part3.rar"))
	if err != nil {
		t.Fatalf("Open(part3): %v", err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "mixed.part04.rar"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("Open(part3) did not return the file holding volume 3")
	}
}

// The header parser on its own: it answers for a rar5 volume that records a
// number, and declines, rather than guessing, for everything else.
func TestRarVolumeNumber(t *testing.T) {
	dir, _ := stageFixture(t, "multi-mixed")
	for name, want := range map[string]int{
		"mixed.part02.rar": 2,
		"mixed.part03.rar": 4,
		"mixed.part04.rar": 3,
	} {
		got, err := rarVolumeNumber(filepath.Join(dir, name))
		if err != nil || got != want {
			t.Errorf("rarVolumeNumber(%s) = %d, %v, want %d", name, got, err, want)
		}
	}

	// The first volume of a rar5 set omits the field, as rar writes it.
	if _, err := rarVolumeNumber(filepath.Join(dir, "mixed.part1.rar")); !errors.Is(err, errNoVolumeNumber) {
		t.Errorf("rarVolumeNumber(volume one) = %v, want errNoVolumeNumber", err)
	}

	rar4, _ := stageFixture(t, "multi-new")
	if _, err := rarVolumeNumber(filepath.Join(rar4, "multi.part02.rar")); !errors.Is(err, errNoVolumeNumber) {
		t.Errorf("rarVolumeNumber(rar4) = %v, want errNoVolumeNumber", err)
	}

	// Not an archive at all, and a file that is not there: neither is a reason
	// to guess a number.
	junk := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(junk, []byte("this is not a rar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rarVolumeNumber(junk); !errors.Is(err, errNoVolumeNumber) {
		t.Errorf("rarVolumeNumber(text file) = %v, want errNoVolumeNumber", err)
	}
	if _, err := rarVolumeNumber(filepath.Join(t.TempDir(), "gone.rar")); err == nil {
		t.Error("rarVolumeNumber of a missing file returned no error")
	}
}

func TestExtractRARFailuresLeaveDirectoryUntouched(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    error
	}{
		{"file flagged encrypted", "file-encrypted", ErrEncrypted},
		{"archive header encrypted", "header-encrypted", ErrEncrypted},
		{"stored crc does not match", "corrupt", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, _ := stageFixture(t, tt.fixture)
			writeFileT(t, filepath.Join(dir, "release.par2"), "par2")
			before := snapshot(t, dir)

			res, err := Extract(context.Background(), dir)
			if err == nil {
				t.Fatalf("Extract succeeded, want failure (result %+v)", res)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("Extract err = %v, want %v", err, tt.want)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Errorf("Extract err = %T, want *Error", err)
			}
			// Nothing extracted, nothing cleaned: the archives and the par2
			// files a retry would need are all still there.
			if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
				t.Errorf("directory changed after failed extract:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func TestExtractNoArchivesIsANoOp(t *testing.T) {
	dir := t.TempDir()
	writeFileT(t, filepath.Join(dir, "movie.mkv"), "already plain")
	writeFileT(t, filepath.Join(dir, "movie.par2"), "par2")
	writeFileT(t, filepath.Join(dir, "movie.nfo"), "nfo")
	before := snapshot(t, dir)

	res, err := Extract(context.Background(), dir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Archives) != 0 || len(res.Files) != 0 || len(res.Removed) != 0 || res.Bytes != 0 {
		t.Errorf("Result = %+v, want everything empty", res)
	}
	// The par2 sweep is the tail of a successful extract, not something done
	// to a release that was never packed.
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("directory changed by a no-op extract:\nbefore %v\nafter  %v", before, after)
	}
}

func TestExtractRefusesToOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	makeZip(t, filepath.Join(dir, "release.zip"), zipEntry{name: "movie.mkv", data: "from archive"})
	writeFileT(t, filepath.Join(dir, "movie.mkv"), "already here")
	before := snapshot(t, dir)

	_, err := Extract(context.Background(), dir)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Extract err = %v, want ErrExists", err)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("directory changed after refused extract")
	}
	if got := string(mustRead(t, filepath.Join(dir, "movie.mkv"))); got != "already here" {
		t.Errorf("movie.mkv = %q, want the pre-existing file", got)
	}
}

func TestExtractMissingVolumeFailsBeforeAnyWork(t *testing.T) {
	dir, _ := stageFixture(t, "multi-new")
	if err := os.Remove(filepath.Join(dir, "multi.part02.rar")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)

	_, err := Extract(context.Background(), dir)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Extract err = %v, want ErrIncomplete", err)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("directory changed after an incomplete set was rejected")
	}
}

func TestExtractHonoursCancelledContext(t *testing.T) {
	dir := t.TempDir()
	makeZip(t, filepath.Join(dir, "release.zip"), zipEntry{name: "movie.mkv", data: strings.Repeat("x", 1<<16)})
	before := snapshot(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Extract(ctx, dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract err = %v, want context.Canceled", err)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("directory changed after a cancelled extract")
	}
}

func TestExtractMultipleSets(t *testing.T) {
	dir := t.TempDir()
	makeZip(t, filepath.Join(dir, "a.zip"), zipEntry{name: "a.mkv", data: "aaa"})
	makeZip(t, filepath.Join(dir, "b.zip"), zipEntry{name: "b.mkv", data: "bbb"})

	res, err := Extract(context.Background(), dir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got, want := res.Archives, []string{"a.zip", "b.zip"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Archives = %v, want %v", got, want)
	}
	sort.Strings(res.Files)
	if got, want := res.Files, []string{"a.mkv", "b.mkv"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
}

func TestSafeEntryName(t *testing.T) {
	ok := []struct{ in, want string }{
		{"movie.mkv", "movie.mkv"},
		{"subs/movie.srt", "subs/movie.srt"},
		{`subs\movie.srt`, "subs/movie.srt"},
		{"a/./b.mkv", "a/b.mkv"},
		{"weird -name- [1080p].mkv", "weird -name- [1080p].mkv"},
		{"subs/", "subs"},
	}
	for _, tt := range ok {
		got, err := safeEntryName(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("safeEntryName(%q) = %q, %v; want %q, nil", tt.in, got, err, tt.want)
		}
	}

	bad := []string{
		"",
		"..",
		"../evil",
		"a/../../evil",
		`..\evil`,
		"/etc/passwd",
		`\\server\share\evil`,
		"movie\x00.mkv",
		"/",
		".",
	}
	for _, in := range bad {
		if got, err := safeEntryName(in); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("safeEntryName(%q) = %q, %v; want ErrUnsafePath", in, got, err)
		}
	}
}

func assertFixtureFiles(t *testing.T, dir string, files map[string]struct {
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}) {
	t.Helper()
	for name, want := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		fi, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s: %v", name, err)
			continue
		}
		if fi.Size() != int64(want.Size) {
			t.Errorf("%s size = %d, want %d", name, fi.Size(), want.Size)
		}
		if got := hashFile(t, path); got != want.SHA256 {
			t.Errorf("%s sha256 = %s, want %s", name, got, want.SHA256)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	return entries
}
