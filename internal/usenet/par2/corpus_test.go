package par2_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/watzon/caravan/internal/usenet/par2"
)

// The corpus in testdata is produced by par2cmdline 1.2.0 and committed whole:
// pristine sources, the par2 sets over them, the exact damaged bytes for each
// case, the reference implementation's verbatim verdict, and MANIFEST.json
// distilled from it. See testdata/generate.sh.
//
// These tests never invoke the par2 binary. The point of the corpus is that
// Caravan's answers are checked against a foreign implementation's answers, so
// the reference has to be frozen rather than consulted: a test that shells out
// to par2 would only prove that whatever par2 is installed today agrees with
// itself.

type manifest struct {
	GeneratedBy string       `json:"generated_by"`
	Sets        []fixtureSet `json:"sets"`
}

type fixtureSet struct {
	Name                  string        `json:"name"`
	BlockSize             uint64        `json:"block_size"`
	RecoveryBlocksCreated int           `json:"recovery_blocks_created"`
	IndexFile             string        `json:"index_file"`
	Par2Files             []string      `json:"par2_files"`
	Files                 []fixtureFile `json:"files"`
	Cases                 []fixtureCase `json:"cases"`
}

type fixtureFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	MD5    string `json:"md5"`
}

type fixtureCase struct {
	Case         string    `json:"case"`
	Description  string    `json:"description"`
	DamagedFiles []string  `json:"damaged_files"`
	RemovedFiles []string  `json:"removed_files"`
	Reference    reference `json:"reference"`
}

// reference is par2cmdline's verdict, lifted from the committed logs.
type reference struct {
	Verdict                 string           `json:"verdict"`
	VerifyExitCode          int              `json:"verify_exit_code"`
	DataBlocksTotal         int              `json:"data_blocks_total"`
	DataBlocksGood          int              `json:"data_blocks_good"`
	SlicesNeeded            int              `json:"slices_needed"`
	RecoveryBlocksAvailable int              `json:"recovery_blocks_available"`
	BlockDeficit            int              `json:"block_deficit"`
	FilesOK                 []string         `json:"files_ok"`
	FileBlocksFound         map[string][]int `json:"file_blocks_found"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	blob, err := os.ReadFile(filepath.Join("testdata", "MANIFEST.json"))
	if err != nil {
		t.Fatalf("read MANIFEST.json: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(blob, &m); err != nil {
		t.Fatalf("parse MANIFEST.json: %v", err)
	}
	if len(m.Sets) == 0 {
		t.Fatal("MANIFEST.json describes no sets")
	}
	return m
}

// stage rebuilds a case in a fresh directory: the pristine sources, the par2
// set, then the case's damaged files copied over the top and its removed files
// deleted.
func stage(t *testing.T, set fixtureSet, c fixtureCase) string {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join("testdata", "sets", set.Name)

	for _, f := range set.Files {
		copyFile(t, filepath.Join(base, "original", f.Name), filepath.Join(dir, f.Name))
	}
	for _, name := range set.Par2Files {
		copyFile(t, filepath.Join(base, "par2", name), filepath.Join(dir, name))
	}
	for _, name := range c.DamagedFiles {
		copyFile(t, filepath.Join(base, "cases", c.Case, name), filepath.Join(dir, name))
	}
	for _, name := range c.RemovedFiles {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatalf("remove %s: %v", name, err)
		}
	}
	return dir
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	blob, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, blob, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func TestCorpusVerify(t *testing.T) {
	m := loadManifest(t)
	for _, set := range m.Sets {
		for _, c := range set.Cases {
			t.Run(set.Name+"/"+c.Case, func(t *testing.T) {
				dir := stage(t, set, c)
				s, err := par2.Open(filepath.Join(dir, set.IndexFile))
				if err != nil {
					t.Fatalf("Open: %v", err)
				}

				if s.SliceSize != set.BlockSize {
					t.Errorf("SliceSize = %d, want %d", s.SliceSize, set.BlockSize)
				}
				if s.TotalSlices != c.Reference.DataBlocksTotal {
					t.Errorf("TotalSlices = %d, want %d (par2cmdline)", s.TotalSlices, c.Reference.DataBlocksTotal)
				}
				if s.RecoverySlices() != c.Reference.RecoveryBlocksAvailable {
					t.Errorf("RecoverySlices = %d, want %d (par2cmdline)", s.RecoverySlices(), c.Reference.RecoveryBlocksAvailable)
				}
				if len(s.Files) != len(set.Files) {
					t.Fatalf("set describes %d files, want %d", len(s.Files), len(set.Files))
				}

				rep, err := s.Verify(context.Background(), dir)
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}

				if rep.GoodSlices != c.Reference.DataBlocksGood {
					t.Errorf("GoodSlices = %d, want %d (par2cmdline)", rep.GoodSlices, c.Reference.DataBlocksGood)
				}
				if rep.MissingSlices != c.Reference.SlicesNeeded {
					t.Errorf("MissingSlices = %d, want %d (par2cmdline)", rep.MissingSlices, c.Reference.SlicesNeeded)
				}
				if got, want := rep.Deficit(), c.Reference.BlockDeficit; got != want {
					t.Errorf("Deficit = %d, want %d (par2cmdline)", got, want)
				}

				switch c.Reference.Verdict {
				case "complete":
					if !rep.Complete() {
						t.Errorf("Complete = false, want true (par2cmdline: repair is not required)")
					}
					if rep.Repairable() {
						t.Error("Repairable = true for an undamaged set")
					}
				case "repairable":
					if rep.Complete() {
						t.Error("Complete = true, want false (par2cmdline: repair is required)")
					}
					if !rep.Repairable() {
						t.Errorf("Repairable = false, want true (par2cmdline: repair is possible)")
					}
				case "unrepairable":
					if rep.Repairable() {
						t.Errorf("Repairable = true, want false (par2cmdline: repair is not possible)")
					}
					if rep.Deficit() <= 0 {
						t.Errorf("Deficit = %d for an unrepairable set, want > 0", rep.Deficit())
					}
				default:
					t.Fatalf("unknown reference verdict %q", c.Reference.Verdict)
				}

				// Which files the reference considered usable as-is.
				var ok []string
				byName := map[string]par2.FileStatus{}
				for _, st := range rep.Files {
					byName[st.Name] = st
					if st.State == par2.FileComplete {
						ok = append(ok, st.Name)
					}
				}
				sort.Strings(ok)
				want := append([]string(nil), c.Reference.FilesOK...)
				sort.Strings(want)
				if !equalStrings(ok, want) {
					t.Errorf("complete files = %v, want %v (par2cmdline)", ok, want)
				}

				// And, where par2cmdline printed a per-file block count, the
				// same count. The corpus damages data in place, so a scanning
				// verifier and a positional one have to agree exactly.
				for name, counts := range c.Reference.FileBlocksFound {
					st, found := byName[name]
					if !found {
						t.Fatalf("par2cmdline reported blocks for %q but the set has no such file", name)
					}
					if st.GoodSlices != counts[0] || st.GoodSlices+st.BadSlices != counts[1] {
						t.Errorf("%s: found %d of %d blocks, want %d of %d (par2cmdline)",
							name, st.GoodSlices, st.GoodSlices+st.BadSlices, counts[0], counts[1])
					}
					if st.State != par2.FileDamaged {
						t.Errorf("%s: state = %v, want damaged", name, st.State)
					}
				}

				// A removed file must be reported missing, not merely damaged:
				// the distinction is what the queue UI shows.
				for _, name := range c.RemovedFiles {
					if st := byName[name]; st.State != par2.FileMissing {
						t.Errorf("%s: state = %v, want missing", name, st.State)
					} else if st.ActualLength != -1 {
						t.Errorf("%s: ActualLength = %d, want -1", name, st.ActualLength)
					}
				}
			})
		}
	}
}

func TestCorpusRepair(t *testing.T) {
	m := loadManifest(t)
	for _, set := range m.Sets {
		for _, c := range set.Cases {
			t.Run(set.Name+"/"+c.Case, func(t *testing.T) {
				dir := stage(t, set, c)
				s, err := par2.Open(filepath.Join(dir, set.IndexFile))
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				rep, err := s.Verify(context.Background(), dir)
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}

				before := snapshot(t, dir)
				err = s.Repair(context.Background(), dir, rep)

				if c.Reference.Verdict == "unrepairable" {
					var insufficient *par2.InsufficientError
					if !errors.As(err, &insufficient) {
						t.Fatalf("Repair error = %v, want *InsufficientError", err)
					}
					if !errors.Is(err, par2.ErrUnrepairable) {
						t.Error("Repair error does not unwrap to ErrUnrepairable")
					}
					if insufficient.Needed != c.Reference.SlicesNeeded {
						t.Errorf("Needed = %d, want %d (par2cmdline)", insufficient.Needed, c.Reference.SlicesNeeded)
					}
					if insufficient.Available != c.Reference.RecoveryBlocksAvailable {
						t.Errorf("Available = %d, want %d (par2cmdline)", insufficient.Available, c.Reference.RecoveryBlocksAvailable)
					}
					if insufficient.Deficit() != c.Reference.BlockDeficit {
						t.Errorf("Deficit = %d, want %d (par2cmdline)", insufficient.Deficit(), c.Reference.BlockDeficit)
					}
					// Nothing may have been written: a repair that cannot
					// finish must not leave half-rebuilt media behind.
					if after := snapshot(t, dir); !equalSnapshots(before, after) {
						t.Error("Repair modified the directory despite failing")
					}
					return
				}

				if err != nil {
					t.Fatalf("Repair: %v", err)
				}

				// Byte-identical to the originals, or it is not a repair.
				for _, f := range set.Files {
					got := sha256File(t, filepath.Join(dir, f.Name))
					if got != f.SHA256 {
						t.Errorf("%s: sha256 = %s, want %s", f.Name, got, f.SHA256)
					}
					info, err := os.Stat(filepath.Join(dir, f.Name))
					if err != nil {
						t.Fatalf("stat %s: %v", f.Name, err)
					}
					if info.Size() != f.Size {
						t.Errorf("%s: size = %d, want %d", f.Name, info.Size(), f.Size)
					}
				}

				// And the set now agrees.
				after, err := s.Verify(context.Background(), dir)
				if err != nil {
					t.Fatalf("Verify after repair: %v", err)
				}
				if !after.Complete() {
					t.Errorf("set is still incomplete after repair: %d of %d slices bad",
						after.MissingSlices, after.TotalSlices)
				}

				// Repairing again must be a no-op, not a second rewrite.
				if err := s.Repair(context.Background(), dir, after); err != nil {
					t.Errorf("Repair of a complete set: %v", err)
				}

				// No temporary files left lying around for the extractor to
				// trip over.
				assertNoTempFiles(t, dir)
			})
		}
	}
}

func TestCorpusVerifyAndRepair(t *testing.T) {
	m := loadManifest(t)
	for _, set := range m.Sets {
		for _, c := range set.Cases {
			t.Run(set.Name+"/"+c.Case, func(t *testing.T) {
				dir := stage(t, set, c)
				s, err := par2.Open(filepath.Join(dir, set.IndexFile))
				if err != nil {
					t.Fatalf("Open: %v", err)
				}

				rep, err := s.VerifyAndRepair(context.Background(), dir)
				if rep == nil {
					t.Fatal("VerifyAndRepair returned no report")
				}
				// The report always describes the damage as found, so the UI
				// can say what was wrong even when the repair succeeded.
				if rep.MissingSlices != c.Reference.SlicesNeeded {
					t.Errorf("report MissingSlices = %d, want %d (pre-repair state)",
						rep.MissingSlices, c.Reference.SlicesNeeded)
				}

				if c.Reference.Verdict == "unrepairable" {
					if !errors.Is(err, par2.ErrUnrepairable) {
						t.Fatalf("VerifyAndRepair error = %v, want ErrUnrepairable", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("VerifyAndRepair: %v", err)
				}
				for _, f := range set.Files {
					if got := sha256File(t, filepath.Join(dir, f.Name)); got != f.SHA256 {
						t.Errorf("%s: sha256 = %s, want %s", f.Name, got, f.SHA256)
					}
				}
			})
		}
	}
}

// TestCorpusOpenFindsSiblingVolumes proves Open pulls in the recovery volumes
// the index file does not name, which is where the whole recovery budget
// lives: an index file on its own has zero recovery blocks.
func TestCorpusOpenFindsSiblingVolumes(t *testing.T) {
	m := loadManifest(t)
	set := m.Sets[0]
	c := set.Cases[0]
	dir := stage(t, set, c)

	full, err := par2.Open(filepath.Join(dir, set.IndexFile))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if full.RecoverySlices() != set.RecoveryBlocksCreated {
		t.Errorf("RecoverySlices = %d, want %d", full.RecoverySlices(), set.RecoveryBlocksCreated)
	}
	if len(full.Sources) != len(set.Par2Files) {
		t.Errorf("Sources = %v, want all %d par2 files", full.Sources, len(set.Par2Files))
	}

	indexOnly, err := par2.OpenFiles(filepath.Join(dir, set.IndexFile))
	if err != nil {
		t.Fatalf("OpenFiles: %v", err)
	}
	if indexOnly.RecoverySlices() != 0 {
		t.Errorf("index file alone has %d recovery slices, want 0", indexOnly.RecoverySlices())
	}
	if indexOnly.TotalSlices != full.TotalSlices {
		t.Errorf("index file alone describes %d slices, want %d", indexOnly.TotalSlices, full.TotalSlices)
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[rel] = sha256File(t, path)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".par2" {
			continue
		}
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("repair left a temporary file behind: %s", e.Name())
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
