package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/config"
	"github.com/watzon/caravan/internal/integrity"
)

// The portable integrity flow end to end (SPEC §2.3, §13, PLAN phase 5 task 3),
// driven through the real HTTP API against the real wiring in runServe:
//
//  1. a first start writes the marker and reports a clean session;
//  2. POST /system/shutdown stops the process and leaves the marker clean;
//  3. a marker left saying "running" — what a yanked drive leaves behind — is
//     detected on the next start, which refuses to resume downloads;
//  4. POST /system/verify clears it, and resumes are allowed again;
//  5. a SIGTERM shutdown lands in exactly the same clean state as the API one.
//
// No storage root is configured, deliberately: that keeps the download engine
// unbuilt, so the test starts no torrent client and touches no network. Every
// endpoint under test here answers before the engine is consulted.

type portableServer struct {
	baseURL string
	errCh   chan error
}

func writePortableConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "caravan.yaml")
	body := fmt.Sprintf("data_dir: %q\nportable: true\nlog_level: error\n", dir)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func startPortable(t *testing.T, cfgPath string) *portableServer {
	t.Helper()
	addr := freeAddr(t)
	p := &portableServer{
		baseURL: "http://" + addr + "/api/v1",
		errCh:   make(chan error, 1),
	}
	go func() { p.errCh <- runServe([]string{"--config", cfgPath, "--listen", addr}) }()

	waitFor(t, 30*time.Second, "caravan to start listening", func() string {
		resp, err := http.Get(p.baseURL + "/system/status")
		if err != nil {
			return err.Error()
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			return "status " + resp.Status
		}
		return ""
	})
	return p
}

// waitStopped waits for runServe to return, which is when its deferred
// teardown — engines, checkpoint, database, marker — has finished.
func (p *portableServer) waitStopped(t *testing.T) {
	t.Helper()
	select {
	case err := <-p.errCh:
		if err != nil {
			t.Fatalf("caravan exited with an error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("caravan did not shut down within 30s")
	}
}

func (p *portableServer) post(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Post(p.baseURL+path, "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func (p *portableServer) dirty(t *testing.T) bool {
	t.Helper()
	resp, err := http.Get(p.baseURL + "/system/status")
	if err != nil {
		t.Fatalf("GET /system/status: %v", err)
	}
	defer resp.Body.Close()
	var status struct {
		Mode  string `json:"mode"`
		Dirty bool   `json:"dirty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Mode != "portable" {
		t.Fatalf("mode = %q, want portable", status.Mode)
	}
	return status.Dirty
}

func wantMarker(t *testing.T, dir, want string) {
	t.Helper()
	state, err := integrity.NewMarker(filepath.Join(dir, config.StateFile)).State()
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if state != want {
		t.Fatalf("marker = %q, want %q", state, want)
	}
}

// A start that cannot open the database must leave the marker claimed. An
// unopenable database is the likeliest way a dirty eject shows itself, and
// releasing the marker there would erase the evidence before anyone saw it.
func TestAFailedStartKeepsTheMarkerClaimed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writePortableConfig(t, dir)
	// A directory where the database belongs: sqlite cannot open it.
	if err := os.Mkdir(filepath.Join(dir, config.DatabaseFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := runServe([]string{"--config", cfgPath, "--listen", freeAddr(t)}); err == nil {
		t.Fatal("runServe returned no error with an unopenable database")
	}
	wantMarker(t, dir, integrity.StateRunning)
}

func TestPortableDirtyEjectDetectionAndRecovery(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writePortableConfig(t, dir)

	// ---- 1: a first start is clean, and claims the directory ---------------
	server := startPortable(t, cfgPath)
	if server.dirty(t) {
		t.Fatal("a first start reported a dirty shutdown")
	}
	wantMarker(t, dir, integrity.StateRunning)

	// ---- 2: shutting down through the API leaves the marker clean ----------
	if code, body := server.post(t, "/system/shutdown"); code != http.StatusAccepted {
		t.Fatalf("POST /system/shutdown = %d (%s), want 202", code, body)
	}
	server.waitStopped(t)
	wantMarker(t, dir, integrity.StateClean)

	// A clean marker is not a dirty start.
	server = startPortable(t, cfgPath)
	if server.dirty(t) {
		t.Fatal("a start after a clean shutdown reported dirty")
	}

	// ---- 3: simulate the eject --------------------------------------------
	// Kill the process the way a pulled drive does: stop it without letting it
	// write the clean marker. Signalling would run the clean path, so the
	// marker is put back the way a killed process leaves it.
	if code, _ := server.post(t, "/system/shutdown"); code != http.StatusAccepted {
		t.Fatalf("POST /system/shutdown = %d, want 202", code)
	}
	server.waitStopped(t)
	if err := os.WriteFile(filepath.Join(dir, config.StateFile), []byte(integrity.StateRunning+"\n"), 0o644); err != nil {
		t.Fatalf("plant a dirty marker: %v", err)
	}

	server = startPortable(t, cfgPath)
	if !server.dirty(t) {
		t.Fatal("a start after a simulated dirty eject reported clean")
	}

	// Downloads stay paused: the resume endpoint refuses before it ever looks
	// for an engine, so this is the flag talking and not the missing engine.
	if code, body := server.post(t, "/downloads/deadbeef/resume"); code != http.StatusConflict {
		t.Fatalf("POST /downloads/{id}/resume while dirty = %d (%s), want 409", code, body)
	}

	// ---- 4: recovery clears it --------------------------------------------
	code, body := server.post(t, "/system/verify")
	if code != http.StatusOK {
		t.Fatalf("POST /system/verify = %d (%s), want 200", code, body)
	}
	if !strings.Contains(body, `"integrity":"ok"`) {
		t.Fatalf("verify body = %s, want integrity ok", body)
	}
	if server.dirty(t) {
		t.Fatal("status still reports dirty after a successful verify")
	}
	// The engine is unbuilt in this test, so a permitted resume gets as far as
	// the 503 that says so — which is the proof the dirty gate is no longer
	// what is stopping it.
	if code, body := server.post(t, "/downloads/deadbeef/resume"); code != http.StatusServiceUnavailable {
		t.Fatalf("POST /downloads/{id}/resume after verifying = %d (%s), want 503", code, body)
	}

	// ---- 5: the signal path ends in the same clean marker ------------------
	signalSelfTerm(t)
	server.waitStopped(t)
	wantMarker(t, dir, integrity.StateClean)
}
