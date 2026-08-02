package nzb

import (
	"regexp"
	"strconv"
	"strings"
)

// par2Volume matches a par2 recovery volume: "release.vol007+16.par2" carries
// 16 recovery blocks starting at block 7. The base name before ".volNNN+NNN"
// is what ties a volume to the index file it repairs with.
var par2Volume = regexp.MustCompile(`(?i)^(.*)\.vol(\d+)\+(\d+)\.par2$`)

// IsPar2 reports whether name is any member of a par2 set — the index file
// ("release.par2") or a recovery volume ("release.vol000+01.par2").
//
// This is the split the pipeline is built on: content files are assembled and
// extracted, par2 files are the repair budget and are fetched only when
// verification says they are needed (SPEC §5.1).
func IsPar2(name string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), ".par2")
}

// Par2Volume reports whether name is a recovery volume, and if so the block
// offset and block count its name advertises.
//
// The count is what a "needs N more blocks" failure is measured against
// (PLAN phase 7 task 4): a set whose volumes total fewer blocks than the
// damage cannot be repaired, and saying so before downloading them is the
// difference between a fast honest failure and a slow one.
func Par2Volume(name string) (offset, count int, ok bool) {
	m := par2Volume.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return 0, 0, false
	}
	offset, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, false
	}
	count, err = strconv.Atoi(m[3])
	if err != nil {
		return 0, 0, false
	}
	return offset, count, true
}

// Par2SetName is the base name every member of a par2 set shares:
// "release.par2" and "release.vol007+16.par2" both belong to set "release".
// It returns "" when name is not a par2 file, so it doubles as a grouping key
// for an NZB that carries more than one set.
func Par2SetName(name string) string {
	name = strings.TrimSpace(name)
	if !IsPar2(name) {
		return ""
	}
	if m := par2Volume.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name[:len(name)-len(".par2")]
}
