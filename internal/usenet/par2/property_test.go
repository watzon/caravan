package par2_test

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/watzon/caravan/internal/usenet/par2"
)

// The corpus cases damage the same bytes every run, which is what makes them
// comparable to par2cmdline's frozen verdict but also means they only ever
// exercise a handful of the possible missing-slice patterns. The recovery
// matrix depends on *which* slices are missing, so these tests walk the space:
// random slice sets, random file deletions, and every damage count from one up
// to the set's exact capacity.

// destroySlices overwrites the given global slice indices with random bytes,
// leaving the file lengths alone.
func destroySlices(t *testing.T, dir string, set fixtureSet, rng *rand.Rand, global map[int]bool) {
	t.Helper()
	next := 0
	for _, f := range set.Files {
		slices := int((uint64(f.Size) + set.BlockSize - 1) / set.BlockSize)
		var touched bool
		blob, err := os.ReadFile(filepath.Join(dir, f.Name))
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		for j := 0; j < slices; j++ {
			if !global[next+j] {
				continue
			}
			touched = true
			start := uint64(j) * set.BlockSize
			end := start + set.BlockSize
			if end > uint64(len(blob)) {
				end = uint64(len(blob))
			}
			junk := make([]byte, end-start)
			rng.Read(junk)
			// Random bytes could in principle reproduce the original; make
			// certain they do not.
			junk[0] = blob[start] ^ 0xFF
			copy(blob[start:end], junk)
		}
		if touched {
			if err := os.WriteFile(filepath.Join(dir, f.Name), blob, 0o644); err != nil {
				t.Fatalf("write %s: %v", f.Name, err)
			}
		}
		next += slices
	}
	if next == 0 {
		t.Fatal("set describes no slices")
	}
}

func TestPropertyRandomDamageWithinCapacity(t *testing.T) {
	m := loadManifest(t)
	rng := rand.New(rand.NewSource(20260801))

	for _, set := range m.Sets {
		pristine := pristineCase(t, set)
		total := set.Cases[0].Reference.DataBlocksTotal
		capacity := set.RecoveryBlocksCreated

		// Every damage count the set is supposed to survive, several random
		// slice choices each.
		for count := 1; count <= capacity && count <= total; count++ {
			for trial := 0; trial < 6; trial++ {
				name := setTrialName(set.Name, count, trial)
				t.Run(name, func(t *testing.T) {
					dir := stage(t, set, pristine)
					chosen := pickSlices(rng, total, count)
					destroySlices(t, dir, set, rng, chosen)

					s, err := par2.Open(filepath.Join(dir, set.IndexFile))
					if err != nil {
						t.Fatalf("Open: %v", err)
					}
					rep, err := s.Verify(context.Background(), dir)
					if err != nil {
						t.Fatalf("Verify: %v", err)
					}
					if rep.MissingSlices != count {
						t.Fatalf("MissingSlices = %d, want %d", rep.MissingSlices, count)
					}
					if !rep.Repairable() {
						t.Fatalf("Repairable = false with %d damaged slices and %d recovery blocks",
							count, s.RecoverySlices())
					}
					if err := s.Repair(context.Background(), dir, rep); err != nil {
						t.Fatalf("Repair: %v", err)
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
}

func TestPropertyRandomDamageBeyondCapacity(t *testing.T) {
	m := loadManifest(t)
	rng := rand.New(rand.NewSource(20260802))

	for _, set := range m.Sets {
		pristine := pristineCase(t, set)
		total := set.Cases[0].Reference.DataBlocksTotal
		capacity := set.RecoveryBlocksCreated

		for count := capacity + 1; count <= total; count++ {
			t.Run(setTrialName(set.Name, count, 0), func(t *testing.T) {
				dir := stage(t, set, pristine)
				chosen := pickSlices(rng, total, count)
				destroySlices(t, dir, set, rng, chosen)
				before := snapshot(t, dir)

				s, err := par2.Open(filepath.Join(dir, set.IndexFile))
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				rep, err := s.Verify(context.Background(), dir)
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				if rep.Repairable() {
					t.Fatalf("Repairable = true with %d damaged slices and %d recovery blocks",
						count, s.RecoverySlices())
				}

				err = s.Repair(context.Background(), dir, rep)
				var insufficient *par2.InsufficientError
				if !errors.As(err, &insufficient) {
					t.Fatalf("Repair error = %v, want *InsufficientError", err)
				}
				if insufficient.Needed != count || insufficient.Available != capacity {
					t.Errorf("error = %v, want needed %d available %d", insufficient, count, capacity)
				}
				if insufficient.Deficit() != count-capacity {
					t.Errorf("Deficit = %d, want %d", insufficient.Deficit(), count-capacity)
				}
				if after := snapshot(t, dir); !equalSnapshots(before, after) {
					t.Error("Repair modified the directory despite failing")
				}
			})
		}
	}
}

// TestPropertyRandomMissingFiles deletes whole files rather than corrupting
// them, which is the shape a download that never got a file at all produces.
func TestPropertyRandomMissingFiles(t *testing.T) {
	m := loadManifest(t)
	rng := rand.New(rand.NewSource(20260803))

	for _, set := range m.Sets {
		pristine := pristineCase(t, set)
		for trial := 0; trial < 8; trial++ {
			t.Run(setTrialName(set.Name, 0, trial), func(t *testing.T) {
				dir := stage(t, set, pristine)

				var deleted []string
				removedSlices := 0
				for _, f := range set.Files {
					if rng.Intn(2) == 0 {
						continue
					}
					slices := int((uint64(f.Size) + set.BlockSize - 1) / set.BlockSize)
					if removedSlices+slices > set.RecoveryBlocksCreated {
						continue
					}
					if err := os.Remove(filepath.Join(dir, f.Name)); err != nil {
						t.Fatalf("remove %s: %v", f.Name, err)
					}
					deleted = append(deleted, f.Name)
					removedSlices += slices
				}
				if len(deleted) == 0 {
					t.Skip("no file fit inside the recovery budget this trial")
				}

				s, err := par2.Open(filepath.Join(dir, set.IndexFile))
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				rep, err := s.Verify(context.Background(), dir)
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				if rep.MissingSlices != removedSlices {
					t.Fatalf("MissingSlices = %d, want %d", rep.MissingSlices, removedSlices)
				}
				for _, st := range rep.Files {
					want := par2.FileComplete
					for _, name := range deleted {
						if st.Name == name {
							want = par2.FileMissing
						}
					}
					if st.State != want {
						t.Errorf("%s: state = %v, want %v", st.Name, st.State, want)
					}
				}
				if err := s.Repair(context.Background(), dir, rep); err != nil {
					t.Fatalf("Repair: %v", err)
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

// TestPropertyTrailingGarbage covers the one kind of damage that needs no
// recovery blocks at all: a file that is byte-correct but longer than it
// should be, which is what a resumed download can leave behind.
func TestPropertyTrailingGarbage(t *testing.T) {
	m := loadManifest(t)
	set := m.Sets[0]
	dir := stage(t, set, pristineCase(t, set))

	target := filepath.Join(dir, set.Files[0].Name)
	fh, err := os.OpenFile(target, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", target, err)
	}
	if _, err := fh.Write(make([]byte, 4096)); err != nil {
		t.Fatalf("append: %v", err)
	}
	fh.Close()

	s, err := par2.Open(filepath.Join(dir, set.IndexFile))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rep, err := s.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Complete() {
		t.Fatal("Complete = true for a file with 4 KiB of trailing garbage")
	}
	if rep.MissingSlices != 0 {
		t.Errorf("MissingSlices = %d, want 0: no slice is damaged, the file is just too long", rep.MissingSlices)
	}
	if !rep.Repairable() {
		t.Fatal("Repairable = false for damage that needs no recovery blocks")
	}
	if err := s.Repair(context.Background(), dir, rep); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	for _, f := range set.Files {
		if got := sha256File(t, filepath.Join(dir, f.Name)); got != f.SHA256 {
			t.Errorf("%s: sha256 = %s, want %s", f.Name, got, f.SHA256)
		}
	}
}

func pickSlices(rng *rand.Rand, total, count int) map[int]bool {
	perm := rng.Perm(total)
	out := make(map[int]bool, count)
	for _, i := range perm[:count] {
		out[i] = true
	}
	return out
}

// pristineCase is the undamaged case of a set, used as the starting point for
// randomly generated damage.
func pristineCase(t *testing.T, set fixtureSet) fixtureCase {
	t.Helper()
	for _, c := range set.Cases {
		if c.Reference.Verdict == "complete" {
			return c
		}
	}
	t.Fatalf("set %s has no undamaged case", set.Name)
	return fixtureCase{}
}

func setTrialName(set string, count, trial int) string {
	return set + "/" + strconv.Itoa(count) + "-slices/" + strconv.Itoa(trial)
}
