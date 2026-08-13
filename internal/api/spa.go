package api

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

// EnvDevUI is the loopback origin of a Vite (or compatible) dev server.
// `just dev` sets it so the Go process can reverse-proxy the SPA and keep
// HMR on the same origin as /api/v1. Release binaries ignore it.
const EnvDevUI = "CARAVAN_DEV_UI"

// indexFile is the SPA entry point and the fallback for unknown paths.
const indexFile = "index.html"

// WithDevUI reverse-proxies the SPA to a Vite (or compatible) dev server so
// `just dev` can serve HMR from the same origin as the API. origin must already
// have been validated with ParseDevUIOrigin; the empty string is a no-op.
func WithDevUI(origin string) Option {
	return func(s *server) {
		if origin == "" {
			return
		}
		s.devUI = newDevUIProxy(origin)
	}
}

// ParseDevUIOrigin validates CARAVAN_DEV_UI. The empty string means "use the
// embedded bundle". Anything else must be a loopback http(s) origin: a remote
// target would let a local env var turn the production listener into an open
// proxy, which is not a trade this flag is allowed to make.
func ParseDevUIOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("must be an http(s) origin, got %q", u.Scheme)
	}
	if u.User != nil {
		return "", fmt.Errorf("must not include credentials")
	}
	if u.Host == "" {
		return "", fmt.Errorf("must be an origin (scheme://host[:port])")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("must be an origin (scheme://host[:port]), not a path")
	}
	host := u.Hostname()
	if !devUILoopback(host) {
		return "", fmt.Errorf("must point at localhost, got %q", host)
	}
	return u.Scheme + "://" + u.Host, nil
}

func devUILoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// newDevUIProxy forwards the SPA (and Vite's HMR websocket) to origin.
func newDevUIProxy(origin string) http.Handler {
	target, err := url.Parse(origin)
	if err != nil {
		// ParseDevUIOrigin already accepted this string; a failure here is a
		// programming error, not a runtime condition.
		panic("api: invalid dev UI origin: " + err.Error())
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(req *http.Request) {
		incomingHost := req.Host
		original(req)
		req.Host = target.Host
		if incomingHost != "" {
			req.Header.Set("X-Forwarded-Host", incomingHost)
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, http.StatusBadGateway, "vite dev server unreachable")
	}
	return proxy
}

// handleSPA serves the embedded Svelte build. A request for a path that is not
// a file in the bundle falls back to index.html so client-side routes survive
// a reload or a deep link; requests under /api are routed elsewhere and never
// reach here.
//
// When a dev UI origin is configured, every GET/HEAD is forwarded there
// instead so Vite can HMR without a Go rebuild.
func (s *server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.devUI != nil {
		s.devUI.ServeHTTP(w, r)
		return
	}
	if s.dist == nil {
		writeError(w, http.StatusNotFound, "no web UI bundled")
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = indexFile
	}
	if !fileExists(s.dist, name) {
		name = indexFile
		if !fileExists(s.dist, name) {
			writeError(w, http.StatusNotFound, "no web UI bundled")
			return
		}
	}
	http.ServeFileFS(w, r, s.dist, name)
}

// fileExists reports whether name is a regular file in fsys. Directories are
// not servable here: the SPA has no directory listings.
func fileExists(fsys fs.FS, name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	info, err := fs.Stat(fsys, name)
	return err == nil && info.Mode().IsRegular()
}
