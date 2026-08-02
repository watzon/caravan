// Command gen writes the fixtures the embedded-Usenet end-to-end test reads:
// an archived release payload and a par2 recovery set over it.
//
// It lives under testdata/ so the go tool never builds it as part of the
// module. Regenerate with:
//
//	go run ./cmd/caravan/testdata/usenet/gen
//
// The par2 set is created by par2cmdline, the reference implementation, for
// the same reason internal/usenet/par2's corpus is: "our encoder and our
// decoder agree with each other" is worth nothing when the thing being proved
// is that a real release repairs. The recovery data here is a foreign
// implementation's, frozen, and the test never shells out to par2.
//
//	PAR2_BIN=/opt/homebrew/bin/par2 go run ./cmd/caravan/testdata/usenet/gen
//
// The archive is a zip rather than a rar. It is the same extract.Extract entry
// point either way — rar decoding is covered exhaustively by
// internal/usenet/extract's own committed rar corpus — and archive/zip means
// the payload is written by the standard library instead of by a second
// hand-rolled copy of the RAR 1.5 block layout.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// The release as the indexer publishes it, and the file inside the archive.
// They differ in punctuation the way real ones do, so the release parser is
// doing real work on the way into the library.
const (
	releaseTitle = "Big Buck Bunny 2008 1080p BluRay x264-CARAVAN"
	contentName  = "Big.Buck.Bunny.2008.1080p.BluRay.x264-CARAVAN.mkv"
	archiveName  = "Big.Buck.Bunny.2008.1080p.BluRay.x264-CARAVAN.zip"
	setName      = "caravan-e2e"

	// contentSize is small enough to move in milliseconds and large enough to
	// span many par2 blocks, which is what makes "repairable" and
	// "unrepairable" two different fixtures rather than two different words.
	contentSize = 96 << 10

	// blockSize and recoveryBlocks set the repair budget the test spends. One
	// lost 8 KiB article costs two blocks; five lost articles cost more than
	// six, which is the unrepairable case.
	blockSize      = 4096
	recoveryBlocks = 6
)

// fixedTime keeps the zip byte-identical between runs: a zip records a
// modification time per entry, and par2 protects exact bytes.
var fixedTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

type manifest struct {
	ReleaseTitle   string   `json:"release_title"`
	ArchiveName    string   `json:"archive_name"`
	ContentName    string   `json:"content_name"`
	ContentSize    int      `json:"content_size"`
	ContentSHA256  string   `json:"content_sha256"`
	SetName        string   `json:"set_name"`
	BlockSize      int      `json:"block_size"`
	RecoveryBlocks int      `json:"recovery_blocks"`
	Par2Files      []string `json:"par2_files"`
	GeneratedBy    string   `json:"generated_by"`
}

func main() {
	dir, err := filepath.Abs(filepath.Join("cmd", "caravan", "testdata", "usenet"))
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); err != nil {
		log.Fatalf("run this from the repository root: %v", err)
	}

	par2Bin := os.Getenv("PAR2_BIN")
	if par2Bin == "" {
		par2Bin = "par2"
	}
	version, err := exec.Command(par2Bin, "-V").CombinedOutput()
	if err != nil {
		log.Fatalf("%s not found; set PAR2_BIN: %v", par2Bin, err)
	}
	par2Version := firstLine(string(version))
	log.Printf("using %s", par2Version)

	// Clear whatever was here, so a rename in the fixture never leaves an
	// orphan the test might read.
	for _, old := range globAll(dir, "*.zip", "*.par2", "MANIFEST.json") {
		if err := os.Remove(old); err != nil {
			log.Fatal(err)
		}
	}

	content := payload(contentSize)
	archive := buildZip(contentName, content)
	if err := os.WriteFile(filepath.Join(dir, archiveName), archive, 0o644); err != nil {
		log.Fatal(err)
	}

	// Create in the fixture directory so the set records a bare filename.
	cmd := exec.Command(par2Bin, "create", "-q", "-q",
		fmt.Sprintf("-s%d", blockSize), fmt.Sprintf("-c%d", recoveryBlocks),
		setName+".par2", archiveName)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Fatalf("par2 create: %v\n%s", err, out)
	}

	sum := sha256.Sum256(content)
	m := manifest{
		ReleaseTitle:   releaseTitle,
		ArchiveName:    archiveName,
		ContentName:    contentName,
		ContentSize:    len(content),
		ContentSHA256:  hex.EncodeToString(sum[:]),
		SetName:        setName,
		BlockSize:      blockSize,
		RecoveryBlocks: recoveryBlocks,
		Par2Files:      names(globAll(dir, "*.par2")),
		GeneratedBy:    par2Version,
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MANIFEST.json"), append(encoded, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s (%d bytes) and %d par2 files", archiveName, len(archive), len(m.Par2Files))
}

// payload is deterministic pseudo-random content. Go's math/rand with an
// explicit source is stable across versions, so re-running this reproduces the
// fixture byte for byte — which it must, because the par2 set beside it
// protects these exact bytes.
func payload(n int) []byte {
	out := make([]byte, n)
	rng := rand.New(rand.NewSource(20080520))
	for i := range out {
		out[i] = byte(rng.Intn(256))
	}
	return out
}

// buildZip stores the payload uncompressed. Stored rather than deflated
// because the point of the fixture is the extract stage running at all, and an
// incompressible payload would not shrink anyway.
func buildZip(name string, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{
		Name:     name,
		Method:   zip.Store,
		Modified: fixedTime,
	})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

func globAll(dir string, patterns ...string) []string {
	var out []string
	for _, p := range patterns {
		matches, err := filepath.Glob(filepath.Join(dir, p))
		if err != nil {
			log.Fatal(err)
		}
		out = append(out, matches...)
	}
	sort.Strings(out)
	return out
}

func names(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return s[:i]
		}
	}
	return s
}
