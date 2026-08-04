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

	// A RAR5 set whose filenames lie about which volume they hold, which is the
	// only format that can: RAR5 records a volume number in every volume's main
	// header, and RAR4 has no field for one. Both real-world defects are packed
	// into four volumes:
	//
	//   - Mixed number width. part1 is one digit and the rest are two, so the
	//     successor rardecode infers from "part1.rar" is "part2.rar", which is
	//     not what the file is called.
	//   - Swapped names. The file called part03 holds volume 4 and the file
	//     called part04 holds volume 3, so reading them in name order silently
	//     produces the wrong bytes.
	//
	// A poster's set really does arrive like this (a 30-volume release with the
	// same swap twice and the width change at ten), and the extractor has to go
	// by what each volume says it is.
	mixed := []entry{
		{name: "notes.nfo", data: content("notes", 90)},
		{name: "feature.mkv", data: content("feature", 4000)},
	}
	vols5 := buildMulti5(mixed, 1, []int{1000, 1000, 1000, 1000})
	// names[i] is the file the volume at index i is written to: volumes 3 and 4
	// (indexes 2 and 3) swap names.
	names = []string{"mixed.part1.rar", "mixed.part02.rar", "mixed.part04.rar", "mixed.part03.rar"}
	for i, v := range vols5 {
		write(dir, names[i], v)
	}
	// Detect reports a set in part-number order, which for this fixture is not
	// the order the volumes were written in — that is the whole point of it.
	manifest["multi-mixed"] = fixture{
		Volumes: []string{"mixed.part1.rar", "mixed.part02.rar", "mixed.part03.rar", "mixed.part04.rar"},
		Files:   files(mixed),
	}

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

// ---------------------------------------------------------------------------
// RAR 5 ("rar5")
//
// A second, unrelated block layout, needed for one property RAR4 cannot carry:
// a volume's own number. Every block is
//
//	crc32(sizeVarint || body) uint32le, sizeVarint, body, [data]
//
// where size is the length of body and every number inside it is a
// little-endian base-128 varint. Stored files need no encoder here either — the
// compression method lives in three bits of one varint and zero means stored.
// ---------------------------------------------------------------------------

// signature5 is the RAR 5 marker block.
var signature5 = []byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x01, 0x00}

// RAR5 block types and flags, mirroring archive50.go in rardecode.
const (
	block5Main = 1
	block5File = 2
	block5End  = 5

	block5HasData      = 0x0002
	block5DataNotFirst = 0x0008
	block5DataNotLast  = 0x0010

	arc5MultiVol = 0x0001
	arc5VolNum   = 0x0002

	endArc5NotLast = 0x0001

	file5HasCRC32 = 0x0004

	file5HostOSUnix = 1
)

// buildMulti5 assembles a RAR5 multi-volume archive, the same shape buildMulti
// produces for RAR4: entries before splitIdx live whole in volume one, and
// entries[splitIdx] is split across every volume using chunks.
//
// Every volume but the first records its own number, which is what real rar
// does and is the whole point of this fixture.
func buildMulti5(entries []entry, splitIdx int, chunks []int) [][]byte {
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
		buf.Write(signature5)
		buf.Write(mainHeader5(i))
		if i == 0 {
			for _, e := range entries[:splitIdx] {
				buf.Write(fileHeader5(e, 0, len(e.data), len(e.data)))
				buf.Write(e.data)
			}
		}
		var flags uint64
		if i > 0 {
			flags |= block5DataNotFirst
		}
		if i < len(chunks)-1 {
			flags |= block5DataNotLast
		}
		buf.Write(fileHeader5(split, flags, c, len(split.data)))
		buf.Write(split.data[off : off+c])
		buf.Write(endHeader5(i < len(chunks)-1))
		vols[i] = buf.Bytes()
		off += c
	}
	return vols
}

// mainHeader5 is the archive block. volnum is 0-based, as the format records
// it; volume one omits the field entirely, exactly as rar writes it.
func mainHeader5(volnum int) []byte {
	body := []byte{}
	body = appendVarint(body, block5Main)
	body = appendVarint(body, 0) // block flags: no extra area, no data
	if volnum == 0 {
		body = appendVarint(body, arc5MultiVol)
		return seal5(body)
	}
	body = appendVarint(body, arc5MultiVol|arc5VolNum)
	body = appendVarint(body, uint64(volnum))
	return seal5(body)
}

// fileHeader5 is one stored file's block. unpacked is the whole file's length
// and the CRC is the whole file's, repeated in every volume that carries part
// of it; packed is this block's share.
func fileHeader5(e entry, flags uint64, packed, unpacked int) []byte {
	sum := crc32.ChecksumIEEE(e.data)
	if e.crc != nil {
		sum = *e.crc
	}
	body := []byte{}
	body = appendVarint(body, block5File)
	body = appendVarint(body, flags|block5HasData)
	body = appendVarint(body, uint64(packed))
	body = appendVarint(body, file5HasCRC32) // file flags
	body = appendVarint(body, uint64(unpacked))
	body = appendVarint(body, 0) // attributes
	body = binary.LittleEndian.AppendUint32(body, sum)
	body = appendVarint(body, 0) // compression flags: algorithm 0, method 0 (stored)
	body = appendVarint(body, file5HostOSUnix)
	body = appendVarint(body, uint64(len(e.name)))
	body = append(body, e.name...)
	return seal5(body)
}

// endHeader5 closes a volume. notLast tells the reader another volume follows.
func endHeader5(notLast bool) []byte {
	body := []byte{}
	body = appendVarint(body, block5End)
	body = appendVarint(body, 0) // block flags
	var flags uint64
	if notLast {
		flags = endArc5NotLast
	}
	body = appendVarint(body, flags)
	return seal5(body)
}

// seal5 frames a block body: its length, and the CRC32 over the length and the
// body together.
func seal5(body []byte) []byte {
	sized := appendVarint(nil, uint64(len(body)))
	sized = append(sized, body...)
	return append(binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(sized)), sized...)
}

func appendVarint(b []byte, v uint64) []byte {
	return binary.AppendUvarint(b, v)
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
