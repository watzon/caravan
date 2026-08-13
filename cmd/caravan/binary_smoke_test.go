package main

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBinaryServesEmbeddedSPA exercises the host binary at the process boundary
// without the long acquisition smokes. It proves version output, embedded
// index delivery, and clean shutdown from a temporary config and storage root.
func TestBinaryServesEmbeddedSPA(t *testing.T) {
	dirs := smokeDirs(t)
	bin := filepath.Join(t.TempDir(), "caravan")

	build := exec.Command("go", "build", "-o", bin, "./cmd/caravan")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	version, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("caravan version: %v", err)
	}
	if got := string(version); got != "caravan dev\n" {
		t.Fatalf("caravan version = %q, want %q", got, "caravan dev\n")
	}

	addr := freeAddr(t)
	cmd := exec.Command(bin, "serve", "--config", dirs.cfgPath, "--listen", addr)
	// A leftover CARAVAN_DEV_UI from `just dev` would proxy GET / to Vite
	// instead of serving the embedded bundle this test is proving.
	cmd.Env = append(os.Environ(), "CARAVAN_DEV_UI=")
	var logs strings.Builder
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start caravan: %v", err)
	}
	cleanedUp := false
	t.Cleanup(func() {
		if !cleanedUp {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		t.Logf("caravan output:\n%s", logs.String())
	})

	origin := "http://" + addr
	waitFor(t, 30*time.Second, "the binary to start listening", func() string {
		resp, err := http.Get(origin + "/api/v1/system/status")
		if err != nil {
			return err.Error()
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			return resp.Status
		}
		return ""
	})

	resp, err := http.Get(origin + "/")
	if err != nil {
		t.Fatalf("GET / from binary: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read GET / response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close GET / response: %v", closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: %s: %s", resp.Status, body)
	}
	if !strings.Contains(string(body), "<html") {
		t.Fatalf("GET / response is not HTML: %q", body)
	}

	signalProcessTerm(t, cmd.Process)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("caravan did not shut down cleanly: %v", err)
	}
	cleanedUp = true
}
