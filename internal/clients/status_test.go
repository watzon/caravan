package clients

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestClamp01(t *testing.T) {
	tests := []struct{ in, want float64 }{
		{-1, 0},
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{1.5, 1},
	}
	for _, tt := range tests {
		if got := Clamp01(tt.in); got != tt.want {
			t.Fatalf("Clamp01(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestSnippet(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"trimmed", "  Fails.\n", "Fails."},
		{"first line only", "API Key Incorrect\nstack trace\nmore", "API Key Incorrect"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Snippet([]byte(tt.in)); got != tt.want {
				t.Fatalf("Snippet(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	long := Snippet([]byte(strings.Repeat("x", snippetMax+50)))
	if len([]rune(long)) != snippetMax+1 {
		t.Fatalf("long snippet = %d runes, want %d plus the ellipsis", len([]rune(long)), snippetMax)
	}
}

// A transport error quotes the whole URL, and a download client's URL carries
// API keys. The cause survives; the URL must not (SPEC §12).
func TestScrubDropsTheURL(t *testing.T) {
	cause := errors.New("connection refused")
	err := Scrub(&url.Error{
		Op:  "Get",
		URL: "http://sab.example/api?mode=queue&apikey=secret-key-sentinel",
		Err: cause,
	})
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want the wrapped cause", err)
	}
	if strings.Contains(err.Error(), "secret-key-sentinel") {
		t.Fatalf("scrubbed error still quotes the credential: %q", err.Error())
	}

	// A URL buried under another wrapper is still found.
	wrapped := Scrub(fmt.Errorf("call: %w", &url.Error{Op: "Post", URL: "http://u:p@host/jsonrpc", Err: cause}))
	if strings.Contains(wrapped.Error(), "u:p") {
		t.Fatalf("scrubbed error still quotes userinfo: %q", wrapped.Error())
	}

	// Anything that is not a URL error is returned untouched.
	plain := errors.New("plain")
	if got := Scrub(plain); got != plain {
		t.Fatalf("Scrub(%v) = %v, want it untouched", plain, got)
	}
}
