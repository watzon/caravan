package extract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Kind is the format of a detected archive set.
type Kind int

const (
	// KindRAR is a rar archive, single- or multi-volume.
	KindRAR Kind = iota
	// KindZip is a zip archive.
	KindZip
)

func (k Kind) String() string {
	switch k {
	case KindRAR:
		return "rar"
	case KindZip:
		return "zip"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Set is one archive found in a download directory.
type Set struct {
	// Kind is the format.
	Kind Kind
	// Volumes are the files the set is made of, relative to the download
	// directory, in reading order. Volumes[0] is the one handed to the decoder,
	// the decoder follows the rest itself, and the whole list is what gets
	// deleted once the extract is verified.
	Volumes []string
}

var (
	// partRE matches the modern multi-volume naming: name.part01.rar. The
	// width of the number varies between packers, so it is parsed rather than
	// matched exactly.
	partRE = regexp.MustCompile(`(?i)^(.+)\.part(\d+)\.rar$`)

	// oldVolRE matches the legacy continuation volumes that follow a plain
	// .rar: .r00 through .r99, then .s00, and so on up the alphabet.
	oldVolRE = regexp.MustCompile(`(?i)^(.+)\.([r-z])(\d\d)$`)
)

// Detect lists the archive sets in dir, top level only, in a stable order.
//
// It returns an [*Error] wrapping [ErrIncomplete] when a multi-volume set has
// a hole in it. A set that is merely absent is not an error: a directory with
// no archives yields no sets, which is how [Extract] recognises a release that
// was posted as plain files.
func Detect(dir string) ([]Set, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, &Error{Err: fmt.Errorf("extract: read %s: %w", dir, err)}
	}

	var zips []string
	// Keyed by lowercased base name so a set survives inconsistent casing.
	parts := map[string]map[int]string{} // base -> part number -> filename
	bare := map[string]string{}          // base -> "base.rar"
	old := map[string]map[int]string{}   // base -> continuation index -> filename

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)

		switch {
		case strings.EqualFold(ext, ".zip"):
			zips = append(zips, name)

		case partRE.MatchString(name):
			m := partRE.FindStringSubmatch(name)
			n, err := strconv.Atoi(m[2])
			if err != nil || n < 1 {
				continue // absurdly long or zero volume number; not a set we know
			}
			base := strings.ToLower(m[1])
			if parts[base] == nil {
				parts[base] = map[int]string{}
			}
			parts[base][n] = name

		case strings.EqualFold(ext, ".rar"):
			bare[strings.ToLower(strings.TrimSuffix(name, ext))] = name

		default:
			m := oldVolRE.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			n, err := strconv.Atoi(m[3])
			if err != nil {
				continue
			}
			// .r00 is the first continuation, .s00 the hundred-and-first.
			base := strings.ToLower(m[1])
			idx := (int(strings.ToLower(m[2])[0])-'r')*100 + n
			if old[base] == nil {
				old[base] = map[int]string{}
			}
			old[base][idx] = name
		}
	}

	var sets []Set

	for base, byNum := range parts {
		// A .partNN.rar set owns its base name; a stray base.rar next to it
		// is part of the same release, not a second archive to open.
		delete(bare, base)
		vols, err := sequence(byNum, 1, func(n int) string {
			return fmt.Sprintf("%s.part%d.rar (or equivalent)", base, n)
		})
		if err != nil {
			return nil, err
		}
		sets = append(sets, Set{Kind: KindRAR, Volumes: vols})
	}

	for base, first := range bare {
		vols := []string{first}
		if byIdx := old[base]; len(byIdx) > 0 {
			rest, err := sequence(byIdx, 0, func(n int) string {
				return fmt.Sprintf("%s.%c%02d", base, 'r'+n/100, n%100)
			})
			if err != nil {
				return nil, err
			}
			vols = append(vols, rest...)
		}
		delete(old, base)
		sets = append(sets, Set{Kind: KindRAR, Volumes: vols})
	}

	// Whatever is left in old has no .rar to start it. An .r00 without its
	// .rar is a set missing its first volume and worth saying so; an .s00 or
	// .z01 without one is more likely a spanned zip or an unrelated file, and
	// guessing there would turn ordinary directories into errors.
	for base, byIdx := range old {
		for idx, name := range byIdx {
			if idx < 100 {
				return nil, &Error{Archive: name, Err: fmt.Errorf("%w: no %s.rar to start the set", ErrIncomplete, base)}
			}
		}
	}

	for _, name := range zips {
		sets = append(sets, Set{Kind: KindZip, Volumes: []string{name}})
	}

	sort.Slice(sets, func(i, j int) bool { return sets[i].Volumes[0] < sets[j].Volumes[0] })
	return sets, nil
}

// sequence flattens a volume map into reading order, requiring it to run from
// first with no gaps. describe names the volume that is missing.
func sequence(byNum map[int]string, first int, describe func(int) string) ([]string, error) {
	vols := make([]string, 0, len(byNum))
	for n := first; n < first+len(byNum); n++ {
		name, ok := byNum[n]
		if !ok {
			return nil, &Error{Err: fmt.Errorf("%w: missing %s", ErrIncomplete, describe(n))}
		}
		vols = append(vols, name)
	}
	return vols, nil
}
