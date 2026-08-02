// Command gen writes the rar fixtures the extract package's tests read.
//
// Rar is a proprietary format with no pure-Go writer, and the rardecode module
// ships no testdata in its module zip, so there is nothing to copy: the
// fixtures have to be built by hand. That is only tractable because RAR 1.5
// (the "rar4" format) is documented and its *stored* method — method byte
// 0x30, no compression — needs no encoder at all. Every fixture here is a
// hand-assembled rar4 archive holding stored files, which is exactly what
// rardecode's archive15 reader parses.
//
// This lives under testdata/ so the go tool never builds it as part of the
// module. Regenerate with:
//
//	go run ./internal/usenet/extract/testdata/gen
//
// Nothing here is third-party code. See ../NOTICE.
//
// Format reference: Parity of the RAR 1.5 block layout as consumed by
// github.com/nwaples/rardecode/v2/archive15.go, cross-read against the
// long-circulated "RAR 4.x technote" block description.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"path/filepath"
	"sort"
)

// Block types.
const (
	blockArc  = 0x73
	blockFile = 0x74
	blockEnd  = 0x7b
)

// Block header flags.
const (
	blockHasData = 0x8000

	arcVolume    = 0x0001
	arcNewNaming = 0x0010
	arcEncrypted = 0x0080

	fileSplitBefore = 0x0001
	fileSplitAfter  = 0x0002
	fileEncrypted   = 0x0004
	fileWindowMask  = 0x00e0

	endArcNotLast = 0x0001
)

// signature is the RAR 1.5 marker block: "Rar!\x1a\x07" plus a version byte.
var signature = []byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x00}

// dosTime is 2024-01-01 00:00:00 packed into the DOS timestamp rar uses.
const dosTime uint32 = (2024-1980)<<25 | 1<<21 | 1<<16

// hostOSUnix is the raw HOST_OS byte for Unix; rardecode adds one to it.
const hostOSUnix = 3

// entry is one file to store in an archive.
type entry struct {
	name string
	data []byte
	dir  bool
	// crc overrides the real CRC32 of data when non-nil, which is how the
	// deliberately corrupt fixture is built.
	crc *uint32
	// encrypted sets the file header's encrypted flag without encrypting
	// anything: the extractor must refuse before it reads a byte, so the
	// payload is never touched.
	encrypted bool
}

func main() {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	// Run from anywhere: write next to this file's parent (testdata/).
	if filepath.Base(dir) == "gen" {
		dir = filepath.Dir(dir)
	} else {
		dir = filepath.Join(dir, "internal", "usenet", "extract", "testdata")
	}

	manifest := map[string]fixture{}

	// Single-volume archive with a nested directory, the ordinary case.
	single := []entry{
		{name: "movie.mkv", data: content("movie", 2000)},
		{name: "subs", dir: true},
		{name: "subs/movie.srt", data: content("subs", 300)},
	}
	write(dir, "single.rar", buildSingle(single, 0))
	manifest["single"] = fixture{Volumes: []string{"single.rar"}, Files: files(single)}

	// Three-volume set using the modern .partNN.rar naming, with one small
	// file wholly inside volume one and one file split across all three.
	multi := []entry{
		{name: "readme.nfo", data: content("readme", 120)},
		{name: "big.bin", data: content("big", 3000)},
	}
	vols := buildMulti(multi, 1, []int{1000, 1000, 1000}, arcNewNaming)
	names := []string{"multi.part01.rar", "multi.part02.rar", "multi.part03.rar"}
	for i, v := range vols {
		write(dir, names[i], v)
	}
	manifest["multi-new"] = fixture{Volumes: names, Files: files(multi)}

	// Three-volume set using the old .rar/.r00/.r01 naming.
	old := []entry{
		{name: "show.mkv", data: content("show", 1500)},
	}
	vols = buildMulti(old, 0, []int{500, 500, 500}, 0)
	names = []string{"old.rar", "old.r00", "old.r01"}
	for i, v := range vols {
		write(dir, names[i], v)
	}
	manifest["multi-old"] = fixture{Volumes: names, Files: files(old)}

	// A file flagged as encrypted. Extraction must stop at the header.
	write(dir, "fileenc.rar", buildSingle([]entry{
		{name: "secret.mkv", data: content("secret", 400), encrypted: true},
	}, 0))
	manifest["file-encrypted"] = fixture{Volumes: []string{"fileenc.rar"}}

	// A whole archive flagged as header-encrypted. rardecode refuses to open
	// it at all, which is a different code path from the flag above.
	var buf bytes.Buffer
	buf.Write(signature)
	buf.Write(mainHeader(arcEncrypted))
	write(dir, "headerenc.rar", buf.Bytes())
	manifest["header-encrypted"] = fixture{Volumes: []string{"headerenc.rar"}}

	// A file whose stored CRC does not match its bytes.
	badCRC := uint32(0xdeadbeef)
	write(dir, "corrupt.rar", buildSingle([]entry{
		{name: "broken.mkv", data: content("broken", 500), crc: &badCRC},
	}, 0))
	manifest["corrupt"] = fixture{Volumes: []string{"corrupt.rar"}}

	writeManifest(dir, manifest)
}

type fixture struct {
	Volumes []string        `json:"volumes"`
	Files   map[string]file `json:"files,omitempty"`
}

type file struct {
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

func files(entries []entry) map[string]file {
	out := map[string]file{}
	for _, e := range entries {
		if e.dir {
			continue
		}
		sum := sha256.Sum256(e.data)
		out[e.name] = file{Size: len(e.data), SHA256: hex.EncodeToString(sum[:])}
	}
	return out
}

// content builds deterministic, human-readable filler.
func content(seed string, n int) []byte {
	var buf []byte
	for i := 0; len(buf) < n; i++ {
		buf = append(buf, fmt.Sprintf("%s line %04d caravan extract fixture\n", seed, i)...)
	}
	return buf[:n]
}

// buildSingle assembles a one-volume archive.
func buildSingle(entries []entry, arcFlags uint16) []byte {
	var buf bytes.Buffer
	buf.Write(signature)
	buf.Write(mainHeader(arcFlags))
	for _, e := range entries {
		if e.dir {
			buf.Write(dirHeader(e.name))
			continue
		}
		buf.Write(fileHeader(e, 0, len(e.data), len(e.data)))
		buf.Write(e.data)
	}
	buf.Write(endHeader(false))
	return buf.Bytes()
}

// buildMulti assembles a multi-volume archive. Entries before splitIdx are
// stored whole in volume zero; entries[splitIdx] is split across the volumes
// using chunks, which must sum to its length. Entries after it are not
// supported — the fixtures do not need them.
func buildMulti(entries []entry, splitIdx int, chunks []int, extraArcFlags uint16) [][]byte {
	split := entries[splitIdx]
	total := 0
	for _, c := range chunks {
		total += c
	}
	if total != len(split.data) {
		log.Fatalf("chunks sum to %d, %s is %d bytes", total, split.name, len(split.data))
	}

	vols := make([][]byte, len(chunks))
	off := 0
	for i, c := range chunks {
		var buf bytes.Buffer
		buf.Write(signature)
		buf.Write(mainHeader(arcVolume | extraArcFlags))
		if i == 0 {
			for _, e := range entries[:splitIdx] {
				if e.dir {
					buf.Write(dirHeader(e.name))
					continue
				}
				buf.Write(fileHeader(e, 0, len(e.data), len(e.data)))
				buf.Write(e.data)
			}
		}
		var flags uint16
		if i > 0 {
			flags |= fileSplitBefore
		}
		if i < len(chunks)-1 {
			flags |= fileSplitAfter
		}
		// Every block of a split file repeats the full unpacked size and the
		// CRC of the whole file; only the packed size is per-block.
		buf.Write(fileHeader(split, flags, c, len(split.data)))
		buf.Write(split.data[off : off+c])
		buf.Write(endHeader(i < len(chunks)-1))
		vols[i] = buf.Bytes()
		off += c
	}
	return vols
}

// mainHeader is the 13-byte archive block every volume opens with.
func mainHeader(flags uint16) []byte {
	h := make([]byte, 13)
	h[2] = blockArc
	binary.LittleEndian.PutUint16(h[3:], flags)
	binary.LittleEndian.PutUint16(h[5:], uint16(len(h)))
	return seal(h)
}

// endHeader closes a volume. notLast tells the reader another volume follows.
func endHeader(notLast bool) []byte {
	h := make([]byte, 7)
	h[2] = blockEnd
	var flags uint16
	if notLast {
		flags = endArcNotLast
	}
	binary.LittleEndian.PutUint16(h[3:], flags)
	binary.LittleEndian.PutUint16(h[5:], uint16(len(h)))
	return seal(h)
}

// dirHeader is a directory entry: the full window-size mask is how rar4 says
// "this is a directory", and it carries no data.
func dirHeader(name string) []byte {
	return block(name, fileWindowMask, 0, 0, 0, 0o755)
}

func fileHeader(e entry, flags uint16, packed, unpacked int) []byte {
	sum := crc32.ChecksumIEEE(e.data)
	if e.crc != nil {
		sum = *e.crc
	}
	if e.encrypted {
		flags |= fileEncrypted
	}
	return block(e.name, flags, packed, unpacked, sum, 0o644)
}

// block builds a file block header. Layout after the shared 7-byte prefix:
// PACK_SIZE(4) UNP_SIZE(4) HOST_OS(1) FILE_CRC(4) FTIME(4) UNP_VER(1)
// METHOD(1) NAME_SIZE(2) ATTR(4) NAME(NAME_SIZE).
func block(name string, flags uint16, packed, unpacked int, sum uint32, attr uint32) []byte {
	h := make([]byte, 32+len(name))
	h[2] = blockFile
	binary.LittleEndian.PutUint16(h[3:], flags|blockHasData)
	binary.LittleEndian.PutUint16(h[5:], uint16(len(h)))
	binary.LittleEndian.PutUint32(h[7:], uint32(packed))
	binary.LittleEndian.PutUint32(h[11:], uint32(unpacked))
	h[15] = hostOSUnix
	binary.LittleEndian.PutUint32(h[16:], sum)
	binary.LittleEndian.PutUint32(h[20:], dosTime)
	h[24] = 20   // decoder version, ignored for stored files
	h[25] = 0x30 // method: stored
	binary.LittleEndian.PutUint16(h[26:], uint16(len(name)))
	binary.LittleEndian.PutUint32(h[28:], attr)
	copy(h[32:], name)
	return seal(h)
}

// seal fills in the header CRC, which covers every byte after it.
func seal(h []byte) []byte {
	binary.LittleEndian.PutUint16(h, uint16(crc32.ChecksumIEEE(h[2:])))
	return h
}

func write(dir, name string, data []byte) {
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		log.Fatal(err)
	}
}

func writeManifest(dir string, m map[string]fixture) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, k := range keys {
		v, err := json.Marshal(m[k])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(&buf, "  %q: %s", k, v)
		if i < len(keys)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("}\n")
	write(dir, "MANIFEST.json", buf.Bytes())
}
