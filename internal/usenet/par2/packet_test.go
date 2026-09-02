package par2

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests are in-package because they build par2 files byte by byte and
// reach for the unexported name validation. The corpus tests next door cover
// the parser against real par2cmdline output; this file covers what happens
// when the bytes are wrong, which the corpus deliberately never is.

const (
	testIndex  = "testdata/sets/basic/par2/basic.par2"
	testVolume = "testdata/sets/basic/par2/basic.vol03+4.par2"
)

// buildPacket assembles a well-formed packet with a correct MD5.
func buildPacket(setID [16]byte, ptype string, body []byte) []byte {
	if len(ptype) != 16 {
		panic("packet type must be 16 bytes")
	}
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	var typeBytes [16]byte
	copy(typeBytes[:], ptype)

	sum := md5.New()
	sum.Write(setID[:])
	sum.Write(typeBytes[:])
	sum.Write(body)

	out := make([]byte, 0, packetHeaderSize+len(body))
	out = append(out, packetMagic[:]...)
	out = binary.LittleEndian.AppendUint64(out, uint64(packetHeaderSize+len(body)))
	out = append(out, sum.Sum(nil)...)
	out = append(out, setID[:]...)
	out = append(out, typeBytes[:]...)
	out = append(out, body...)
	return out
}

func writeTemp(t *testing.T, dir, name string, blob []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return blob
}

// corruptAfter flips a byte a fixed distance past the first occurrence of a
// packet type marker, which lands inside that packet's body and invalidates
// only that packet's MD5.
func corruptAfter(t *testing.T, blob []byte, ptype string, offset int) []byte {
	t.Helper()
	i := bytes.Index(blob, []byte(ptype))
	if i < 0 {
		t.Fatalf("fixture contains no %q packet", ptype)
	}
	out := append([]byte(nil), blob...)
	pos := i + 16 + offset
	if pos >= len(out) {
		t.Fatalf("offset %d past end of fixture", offset)
	}
	out[pos] ^= 0xFF
	return out
}

func TestScanPacketsRejectsBadPacketMD5(t *testing.T) {
	dir := t.TempDir()

	// A volume with one recovery slice payload byte flipped: that packet's
	// MD5 no longer covers its contents, so the slice must be dropped rather
	// than fed into the solve.
	good := readFixture(t, testVolume)
	bad := corruptAfter(t, good, typeRecovery, 64)

	countRecovery := func(path string) int {
		n := 0
		if err := scanPackets(path, func(p *rawPacket) error {
			if p.Type == typeRecovery {
				n++
			}
			return nil
		}); err != nil {
			t.Fatalf("scanPackets: %v", err)
		}
		return n
	}

	before := countRecovery(writeTemp(t, dir, "good.par2", good))
	after := countRecovery(writeTemp(t, dir, "bad.par2", bad))
	if before == 0 {
		t.Fatal("fixture volume carries no recovery slices")
	}
	if after != before-1 {
		t.Fatalf("corrupting one recovery packet left %d slices, want %d", after, before-1)
	}
}

func TestOpenFilesRejectsSetWithoutMainPacket(t *testing.T) {
	dir := t.TempDir()
	// Corrupt the main packet only. Without it the loader cannot know which
	// recovery set the remaining packets belong to.
	blob := corruptAfter(t, readFixture(t, testIndex), typeMain, 0)
	path := writeTemp(t, dir, "basic.par2", blob)

	_, err := OpenFiles(path)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("OpenFiles error = %v, want ErrMalformed", err)
	}
	if !errors.Is(err, errNoMainPacket) {
		t.Fatalf("OpenFiles error = %v, want errNoMainPacket", err)
	}
}

func TestOpenFilesRejectsMissingFileDescription(t *testing.T) {
	dir := t.TempDir()
	// Corrupt one file description packet in an index file read on its own.
	// The main packet still lists the file, so the set is unusable and must
	// say so rather than quietly verify two files out of three.
	blob := corruptAfter(t, readFixture(t, testIndex), typeFileDesc, 20)
	path := writeTemp(t, dir, "basic.par2", blob)

	_, err := OpenFiles(path)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("OpenFiles error = %v, want ErrMalformed", err)
	}
}

func TestScanPacketsSkipsUnknownTypesAndJunk(t *testing.T) {
	dir := t.TempDir()
	setID := [16]byte{9: 0xAB}

	var blob []byte
	blob = append(blob, []byte("not a packet, just some leading junk")...)
	blob = append(blob, buildPacket(setID, "PAR 2.0\x00Nonsense", []byte("ignore me"))...)
	blob = append(blob, buildPacket(setID, typeCreator, []byte("caravan test"))...)
	blob = append(blob, []byte("trailing junk")...)
	path := writeTemp(t, dir, "junk.par2", blob)

	var types []string
	if err := scanPackets(path, func(p *rawPacket) error {
		types = append(types, p.Type)
		return nil
	}); err != nil {
		t.Fatalf("scanPackets: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("found %d packets (%q), want 2", len(types), types)
	}
	if types[1] != typeCreator {
		t.Errorf("second packet type = %q, want the creator packet", types[1])
	}
}

func TestScanPacketsSurvivesTruncation(t *testing.T) {
	dir := t.TempDir()
	blob := readFixture(t, testIndex)

	full := 0
	if err := scanPackets(writeTemp(t, dir, "full.par2", blob), func(*rawPacket) error {
		full++
		return nil
	}); err != nil {
		t.Fatalf("scanPackets: %v", err)
	}
	if full < 3 {
		t.Fatalf("index fixture has %d packets, want several", full)
	}

	// Cut the file in the middle. The packets before the cut must still load;
	// the truncated one at the end is simply not there.
	cut := len(blob) / 2
	partial := 0
	if err := scanPackets(writeTemp(t, dir, "cut.par2", blob[:cut]), func(*rawPacket) error {
		partial++
		return nil
	}); err != nil {
		t.Fatalf("scanPackets on a truncated file: %v", err)
	}
	if partial == 0 {
		t.Fatal("a truncated index file yielded no packets at all")
	}
	if partial >= full {
		t.Fatalf("a file cut in half yielded %d packets, want fewer than %d", partial, full)
	}
}

func TestParseMainRejectsBadSliceSize(t *testing.T) {
	for _, size := range []uint64{0, 1, 6, 1023} {
		body := binary.LittleEndian.AppendUint64(nil, size)
		body = binary.LittleEndian.AppendUint32(body, 1)
		body = append(body, make([]byte, 16)...)
		if _, err := parseMain(body); !errors.Is(err, ErrMalformed) {
			t.Errorf("parseMain(slice size %d) error = %v, want ErrMalformed", size, err)
		}
	}
}

func TestParseFileDescRejectsWrongFileID(t *testing.T) {
	body := make([]byte, 56)
	copy(body[0:16], bytes.Repeat([]byte{0xEE}, 16)) // a file ID that hashes to nothing
	binary.LittleEndian.PutUint64(body[48:56], 100)
	body = append(body, []byte("name.bin")...)

	if _, err := parseFileDesc(body); !errors.Is(err, ErrMalformed) {
		t.Fatalf("parseFileDesc with a forged file id: error = %v, want ErrMalformed", err)
	}
}

func TestParseIFSCRejectsRaggedList(t *testing.T) {
	body := append(make([]byte, 16), make([]byte, 25)...) // 25 is not a multiple of 20
	if _, _, err := parseIFSC(body); !errors.Is(err, ErrMalformed) {
		t.Fatalf("parseIFSC error = %v, want ErrMalformed", err)
	}
}

func TestSafeName(t *testing.T) {
	ok := []struct{ in, want string }{
		{"movie.mkv", "movie.mkv"},
		{"Sample/movie.mkv", "Sample/movie.mkv"},
		{"a..b.mkv", "a..b.mkv"},
		{`sub\file.mkv`, "sub/file.mkv"},
	}
	for _, c := range ok {
		got, err := safeName(c.in)
		if err != nil {
			t.Errorf("safeName(%q) = error %v, want %q", c.in, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("safeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// A par2 set comes off a public news server, so a name that escapes the
	// download directory has to be a hard error rather than something the
	// repair writes.
	bad := []string{
		"",
		"../escape.mkv",
		"a/../../escape.mkv",
		"/etc/passwd",
		`..\escape.mkv`,
		"nul\x00byte.mkv",
	}
	for _, in := range bad {
		if got, err := safeName(in); !errors.Is(err, ErrUnsafeName) {
			t.Errorf("safeName(%q) = %q, %v; want ErrUnsafeName", in, got, err)
		}
	}
}

// TestVerifyRespectsContext proves a long verify can be cancelled, which is
// what lets the queue UI stop a repair the user gave up on.
func TestVerifyRespectsContext(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.bin", "beta.bin", "gamma.bin"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/basic/original", name)))
	}
	for _, name := range []string{"basic.par2", "basic.vol03+4.par2"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/basic/par2", name)))
	}

	s, err := Open(filepath.Join(dir, "basic.par2"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Verify(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify with a cancelled context: error = %v, want context.Canceled", err)
	}
}

// TestRepairRejectsMismatchedReport guards the one way a caller can silently
// corrupt a release with this API: repairing against a report for a different
// set.
func TestRepairRejectsMismatchedReport(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.bin", "beta.bin", "gamma.bin"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/basic/original", name)))
	}
	for _, name := range []string{"basic.par2", "basic.vol03+4.par2"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/basic/par2", name)))
	}
	s, err := Open(filepath.Join(dir, "basic.par2"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := s.Repair(context.Background(), dir, nil); err == nil {
		t.Error("Repair accepted a nil report")
	}
	if err := s.Repair(context.Background(), dir, &Report{Dir: dir, Files: make([]FileStatus, 99)}); err == nil {
		t.Error("Repair accepted a report describing a different number of files")
	}

	// A report for a different directory must be refused rather than used to
	// decide what to overwrite here.
	rep, err := s.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	rep.Dir = t.TempDir()
	if err := s.Repair(context.Background(), dir, rep); err == nil {
		t.Error("Repair accepted a report produced for a different directory")
	}
}

// TestOpenPicksTheSetTheIndexNames covers a directory holding two unrelated
// par2 sets, which is what a release with a sample folder looks like. Open
// must follow the index file it was given and pull in only that set's volumes.
func TestOpenPicksTheSetTheIndexNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.bin", "beta.bin", "gamma.bin"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/basic/original", name)))
	}
	writeTemp(t, dir, "delta.bin", readFixture(t, "testdata/sets/small/original/delta.bin"))
	for _, name := range []string{"basic.par2", "basic.vol00+1.par2", "basic.vol01+2.par2", "basic.vol03+4.par2", "basic.vol07+3.par2"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/basic/par2", name)))
	}
	for _, name := range []string{"small.par2", "small.vol0+4.par2"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/small/par2", name)))
	}

	basic, err := Open(filepath.Join(dir, "basic.par2"))
	if err != nil {
		t.Fatalf("Open basic: %v", err)
	}
	if len(basic.Files) != 3 || basic.SliceSize != 1024 || basic.RecoverySlices() != 10 {
		t.Fatalf("basic set = %d files, slice size %d, %d recovery slices; want 3, 1024, 10",
			len(basic.Files), basic.SliceSize, basic.RecoverySlices())
	}

	small, err := Open(filepath.Join(dir, "small.par2"))
	if err != nil {
		t.Fatalf("Open small: %v", err)
	}
	if len(small.Files) != 1 || small.SliceSize != 512 || small.RecoverySlices() != 4 {
		t.Fatalf("small set = %d files, slice size %d, %d recovery slices; want 1, 512, 4",
			len(small.Files), small.SliceSize, small.RecoverySlices())
	}

	// And both verify clean against the same directory.
	for _, s := range []*Set{basic, small} {
		rep, err := s.Verify(context.Background(), dir)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !rep.Complete() {
			t.Errorf("set %x is not complete in a directory holding both sets", s.ID[:4])
		}
	}
}

// writeSyntheticSet writes a hand-built par2 index describing data, with
// whatever whole-file MD5 the caller asks for. It carries no recovery slices,
// which is all these tests need: they are about what happens when the set's
// own numbers disagree with each other.
func writeSyntheticSet(t *testing.T, dir, setName, fileName string, data []byte, sliceSize int, fileMD5 [16]byte) string {
	t.Helper()

	head := data
	if len(head) > 16384 {
		head = head[:16384]
	}
	md5_16k := md5.Sum(head)

	length := binary.LittleEndian.AppendUint64(nil, uint64(len(data)))
	idSum := md5.New()
	idSum.Write(md5_16k[:])
	idSum.Write(length)
	idSum.Write([]byte(fileName))
	var fileID [16]byte
	copy(fileID[:], idSum.Sum(nil))

	desc := append([]byte(nil), fileID[:]...)
	desc = append(desc, fileMD5[:]...)
	desc = append(desc, md5_16k[:]...)
	desc = append(desc, length...)
	desc = append(desc, []byte(fileName)...)

	ifsc := append([]byte(nil), fileID[:]...)
	buf := make([]byte, sliceSize)
	for off := 0; off < len(data); off += sliceSize {
		for i := range buf {
			buf[i] = 0
		}
		copy(buf, data[off:])
		sum := md5.Sum(buf)
		ifsc = append(ifsc, sum[:]...)
		ifsc = binary.LittleEndian.AppendUint32(ifsc, crc32.ChecksumIEEE(buf))
	}

	mainBody := binary.LittleEndian.AppendUint64(nil, uint64(sliceSize))
	mainBody = binary.LittleEndian.AppendUint32(mainBody, 1)
	mainBody = append(mainBody, fileID[:]...)
	// The spec defines the recovery set ID as the MD5 of the main packet body.
	setID := md5.Sum(mainBody)

	var blob []byte
	blob = append(blob, buildPacket(setID, typeMain, mainBody)...)
	blob = append(blob, buildPacket(setID, typeFileDesc, desc)...)
	blob = append(blob, buildPacket(setID, typeIFSC, ifsc)...)
	blob = append(blob, buildPacket(setID, typeCreator, []byte("caravan synthetic"))...)

	writeTemp(t, dir, fileName, data)
	return writeTemp(t, dir, setName+".par2", blob)
}

// TestRepairAbortsWhenRebuiltFileFailsItsOwnMD5 drives the defensive path that
// should be unreachable: a set whose slice checksums all pass but whose
// whole-file MD5 does not. Rebuilding from those slices can only reproduce the
// same bytes, so the repair must abort with a typed error and leave the file on
// disk exactly as it found it: never rename a file it could not vouch for.
func TestRepairAbortsWhenRebuiltFileFailsItsOwnMD5(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("caravan"), 900) // 6300 bytes, not a whole number of slices
	index := writeSyntheticSet(t, dir, "synthetic", "payload.bin", data, 1024, [16]byte{0: 0xDE, 1: 0xAD})

	s, err := Open(index)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rep, err := s.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.MissingSlices != 0 {
		t.Fatalf("MissingSlices = %d, want 0: every slice checksum matches", rep.MissingSlices)
	}
	if rep.Complete() {
		t.Fatal("Complete = true despite a whole-file MD5 that does not match")
	}

	before, err := os.ReadFile(filepath.Join(dir, "payload.bin"))
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}

	err = s.Repair(context.Background(), dir, rep)
	var mismatch *ChecksumError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Repair error = %v, want *ChecksumError", err)
	}
	if !errors.Is(err, ErrRepairFailed) {
		t.Error("ChecksumError does not unwrap to ErrRepairFailed")
	}
	if mismatch.Name != "payload.bin" {
		t.Errorf("ChecksumError.Name = %q, want payload.bin", mismatch.Name)
	}

	after, err := os.ReadFile(filepath.Join(dir, "payload.bin"))
	if err != nil {
		t.Fatalf("read payload after failed repair: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a failed repair rewrote the file anyway")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name()[0] == '.' {
			t.Errorf("a failed repair left %s behind", e.Name())
		}
	}
}

func TestErrorMessages(t *testing.T) {
	// The deficit sentence is what the queue UI shows a user, so it is worth
	// pinning rather than leaving to whatever fmt produces next year.
	insufficient := &InsufficientError{Needed: 12, Available: 7}
	if got, want := insufficient.Deficit(), 5; got != want {
		t.Errorf("Deficit = %d, want %d", got, want)
	}
	if got := insufficient.Error(); got != "par2: repair needs 12 recovery blocks, only 7 available (5 short)" {
		t.Errorf("InsufficientError.Error() = %q", got)
	}
	if !errors.Is(insufficient, ErrUnrepairable) {
		t.Error("InsufficientError does not unwrap to ErrUnrepairable")
	}

	mismatch := &ChecksumError{Name: "movie.mkv", Expected: [16]byte{0: 1}, Actual: [16]byte{0: 2}}
	if got := mismatch.Error(); !bytes.Contains([]byte(got), []byte("movie.mkv")) {
		t.Errorf("ChecksumError.Error() = %q, want it to name the file", got)
	}

	for state, want := range map[FileState]string{
		FileComplete: "complete",
		FileDamaged:  "damaged",
		FileMissing:  "missing",
		FileState(9): "FileState(9)",
	} {
		if got := state.String(); got != want {
			t.Errorf("FileState(%d).String() = %q, want %q", int(state), got, want)
		}
	}
}

// writeBrokenForeignSet writes a par2 index for an unrelated recovery set whose
// file description packet passes its own MD5, so scanPackets hands it over, but
// whose file ID does not match the description it carries. That is what a
// bit-rotted or oddly-generated set from another release in the same download
// directory looks like.
func writeBrokenForeignSet(t *testing.T, dir, name string) {
	t.Helper()

	var fileID [16]byte
	copy(fileID[:], "not-a-real-id...") // the spec defines this as an MD5; it is not one

	desc := append([]byte(nil), fileID[:]...)
	desc = append(desc, make([]byte, 32)...) // whole-file MD5 and first-16k MD5
	desc = binary.LittleEndian.AppendUint64(desc, 1024)
	desc = append(desc, []byte("other.bin")...)

	mainBody := binary.LittleEndian.AppendUint64(nil, 512)
	mainBody = binary.LittleEndian.AppendUint32(mainBody, 1)
	mainBody = append(mainBody, fileID[:]...)
	setID := md5.Sum(mainBody)

	var blob []byte
	blob = append(blob, buildPacket(setID, typeMain, mainBody)...)
	blob = append(blob, buildPacket(setID, typeFileDesc, desc)...)
	writeTemp(t, dir, name, blob)
}

// A Usenet download directory routinely holds several releases' par2 files. One
// broken foreign set must not take a healthy release's recovery budget down
// with it: Open pulls in every *.par2 sibling, so before this the whole repair
// became impossible because of a set nobody asked about.
func TestOpenIgnoresADamagedSetItWasNotAskedFor(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.bin", "beta.bin", "gamma.bin"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/basic/original", name)))
	}
	for _, name := range []string{"basic.par2", "basic.vol00+1.par2", "basic.vol01+2.par2", "basic.vol03+4.par2", "basic.vol07+3.par2"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/basic/par2", name)))
	}
	writeBrokenForeignSet(t, dir, "other.par2")

	s, err := Open(filepath.Join(dir, "basic.par2"))
	if err != nil {
		t.Fatalf("Open with a broken foreign set alongside: %v", err)
	}
	if len(s.Files) != 3 || s.RecoverySlices() != 10 {
		t.Fatalf("set = %d files, %d recovery slices; want 3 and 10", len(s.Files), s.RecoverySlices())
	}
	rep, err := s.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.Complete() {
		t.Error("a healthy set did not verify clean next to a broken foreign one")
	}

	// And the broken set is still broken when it is the one being asked for:
	// the failure is scoped, not suppressed.
	if _, err := OpenFiles(filepath.Join(dir, "other.par2")); !errors.Is(err, ErrMalformed) {
		t.Fatalf("OpenFiles(other.par2) = %v, want ErrMalformed", err)
	}
}

// Damage inside the recovery budget but outside the memory budget has to be an
// error rather than an allocation the kernel kills mid-repair. It is not
// ErrUnrepairable: more recovery blocks would not help.
func TestRepairRefusesASolveTooLargeToHold(t *testing.T) {
	if err := checkRepairMemory(1, 4); err != nil {
		t.Fatalf("an ordinary repair was refused: %v", err)
	}
	// Two full sets of buffers, exactly at the cap, is still allowed.
	if err := checkRepairMemory(1, MaxRepairMemory/2); err != nil {
		t.Fatalf("a repair exactly at the cap was refused: %v", err)
	}

	// A 40 GB release at a 2 MB slice size with 10% loss.
	err := checkRepairMemory(2000, 2<<20)
	if !errors.Is(err, ErrRepairTooLarge) {
		t.Fatalf("error = %v, want ErrRepairTooLarge", err)
	}
	if errors.Is(err, ErrUnrepairable) {
		t.Error("a repair the set could cover was reported as beyond the recovery budget")
	}
	for _, want := range []string{"2000", "memory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}

	// A slice size that would overflow the multiplication must still be
	// refused, not wrap into a small number and be allowed.
	if err := checkRepairMemory(1<<40, 1<<40); !errors.Is(err, ErrRepairTooLarge) {
		t.Fatalf("overflowing sizes = %v, want ErrRepairTooLarge", err)
	}
}

// And the limit is actually consulted before a repair allocates: a real,
// perfectly repairable set is refused when the solve would not fit, and the
// damaged file is left exactly as it was found.
func TestRepairChecksTheMemoryLimitBeforeAllocating(t *testing.T) {
	dir := t.TempDir()
	pristine := readFixture(t, "testdata/sets/small/original/delta.bin")
	writeTemp(t, dir, "delta.bin", pristine)
	for _, name := range []string{"small.par2", "small.vol0+4.par2"} {
		writeTemp(t, dir, name, readFixture(t, filepath.Join("testdata/sets/small/par2", name)))
	}

	s, err := Open(filepath.Join(dir, "small.par2"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	damaged := append([]byte(nil), pristine...)
	damaged[0] ^= 0xFF
	writeTemp(t, dir, "delta.bin", damaged)

	rep, err := s.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.Repairable() {
		t.Fatalf("the fixture is not repairable: %d missing, %d recovery", rep.MissingSlices, rep.RecoverySlices)
	}

	restore := maxRepairMemory
	maxRepairMemory = 1
	defer func() { maxRepairMemory = restore }()

	err = s.Repair(context.Background(), dir, rep)
	if !errors.Is(err, ErrRepairTooLarge) {
		t.Fatalf("Repair = %v, want ErrRepairTooLarge", err)
	}
	if errors.Is(err, ErrUnrepairable) {
		t.Error("a repair the set could cover was reported as beyond the recovery budget")
	}
	if got := readFixture(t, filepath.Join(dir, "delta.bin")); !bytes.Equal(got, damaged) {
		t.Error("a refused repair changed the file on disk")
	}
}
