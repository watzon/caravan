package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseDevUIOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{name: "empty means embed", raw: "", want: ""},
		{name: "whitespace means embed", raw: "  \n", want: ""},
		{name: "loopback with port", raw: "http://127.0.0.1:5173", want: "http://127.0.0.1:5173"},
		{name: "localhost", raw: "http://localhost:5173", want: "http://localhost:5173"},
		{name: "ipv6 loopback", raw: "http://[::1]:5173", want: "http://[::1]:5173"},
		{name: "trailing slash is stripped", raw: "http://127.0.0.1:5173/", want: "http://127.0.0.1:5173"},
		{name: "https loopback", raw: "https://127.0.0.1:5173", want: "https://127.0.0.1:5173"},
		{name: "remote host", raw: "http://example.com:5173", wantErr: "localhost"},
		{name: "lan address", raw: "http://192.168.1.10:5173", wantErr: "localhost"},
		{name: "path is not an origin", raw: "http://127.0.0.1:5173/src", wantErr: "origin"},
		{name: "query is not an origin", raw: "http://127.0.0.1:5173?x=1", wantErr: "origin"},
		{name: "credentials refused", raw: "http://user:pass@127.0.0.1:5173", wantErr: "credentials"},
		{name: "ftp refused", raw: "ftp://127.0.0.1:5173", wantErr: "http"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDevUIOrigin(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseDevUIOrigin(%q) = %q, want error containing %q", tt.raw, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseDevUIOrigin(%q) error = %q, want it to contain %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDevUIOrigin(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDevUIOrigin(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDevUIProxiesSPAAndLeavesAPIAlone(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "vite:%s", r.URL.Path)
	}))
	t.Cleanup(upstream.Close)

	h, _, _ := newTestServer(t, WithDevUI(upstream.URL))

	rec := do(t, h, http.MethodGet, "/src/main.ts", "")
	wantStatus(t, rec, http.StatusOK)
	if got, want := rec.Body.String(), "vite:/src/main.ts"; got != want {
		t.Fatalf("proxied SPA body = %q, want %q", got, want)
	}

	// A client-side route must not be rewritten to the embedded index.html
	// when Vite is in front: Vite is what knows how to serve the module graph.
	rec = do(t, h, http.MethodGet, "/library/movies", "")
	wantStatus(t, rec, http.StatusOK)
	if got, want := rec.Body.String(), "vite:/library/movies"; got != want {
		t.Fatalf("proxied client route = %q, want %q", got, want)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "vite:") {
		t.Fatalf("API request was proxied to Vite: %q", rec.Body.String())
	}
}

func TestDevUIUnreachableIsBadGateway(t *testing.T) {
	h, _, _ := newTestServer(t, WithDevUI("http://127.0.0.1:1"))

	rec := do(t, h, http.MethodGet, "/", "")
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)
}

func TestDevUIStillRejectsNonGETMethods(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unsafe method was forwarded to Vite")
	}))
	t.Cleanup(upstream.Close)

	h, _, _ := newTestServer(t, WithDevUI(upstream.URL))
	rec := do(t, h, http.MethodPost, "/library", "")
	wantStatus(t, rec, http.StatusMethodNotAllowed)
	wantErrorBody(t, rec)
}

func TestDevUIForwardsViteHostHeader(t *testing.T) {
	var gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	target, err := ParseDevUIOrigin(upstream.URL)
	if err != nil {
		t.Fatalf("ParseDevUIOrigin(%q): %v", upstream.URL, err)
	}
	h, _, _ := newTestServer(t, WithDevUI(target))
	rec := do(t, h, http.MethodGet, "/@vite/client", "")
	wantStatus(t, rec, http.StatusOK)

	// httptest.Server URLs are 127.0.0.1:port. Vite must see its own host,
	// not the Caravan listener, or it generates HMR URLs that miss the proxy.
	wantHost := strings.TrimPrefix(target, "http://")
	if gotHost != wantHost {
		t.Fatalf("upstream Host = %q, want %q", gotHost, wantHost)
	}
}

func TestDevUIProxiesWebSocketUpgrade(t *testing.T) {
	// Vite's HMR client speaks websocket. The access-log wrapper used to hide
	// Hijack, which made ReverseProxy answer 502 and killed live reload.
	upgraded := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("upstream hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		_, _ = buf.WriteString("Upgrade: websocket\r\n")
		_, _ = buf.WriteString("Connection: Upgrade\r\n")
		_, _ = buf.WriteString("Sec-WebSocket-Protocol: vite-hmr\r\n\r\n")
		if err := buf.Flush(); err != nil {
			t.Errorf("upstream flush: %v", err)
			return
		}
		close(upgraded)
	}))
	t.Cleanup(upstream.Close)

	h, _, _ := newTestServer(t, WithDevUI(upstream.URL))
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)

	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatalf("dial front: %v", err)
	}
	defer conn.Close()

	req := "GET / HTTP/1.1\r\n" +
		"Host: " + strings.TrimPrefix(front.URL, "http://") + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Protocol: vite-hmr\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write upgrade: %v", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want 101", resp.StatusCode)
	}
	select {
	case <-upgraded:
	default:
		t.Fatal("upstream never saw the websocket upgrade")
	}
}
