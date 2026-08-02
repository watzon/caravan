// Package clients is the registry of external download-client backends
// (SPEC §5.1, §7 `download_clients`; PLAN phase 6).
//
// It owns two halves that change at different rates:
//
//   - The Type table: what backends exist, what they are called, which
//     protocol each speaks and which credentials each needs. This is static
//     data the settings screen and the request validator both read, so a
//     configuration can be entered, stored and validated before anything can
//     talk to it.
//   - The Registry: the connection probes, supplied by the packages that
//     actually speak each protocol. Until one registers, TestConnection
//     answers ErrNotSupported and says so in plain words.
//
// Like internal/download, this package does not import internal/store:
// configuration arrives as a core.DownloadClientConfig value, so the protocol
// implementations that register here stay testable without a database.
package clients

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/watzon/caravan/internal/core"
)

// ErrNotSupported is returned when a client type is known — the user could
// pick it, and the row is storable — but nothing has registered an
// implementation for it yet. It is distinct from "unknown type" because the
// two need different answers: one is a build that does not include the
// backend, the other is a bad request.
var ErrNotSupported = errors.New("clients: download client type not supported yet")

// TestFunc probes a configured client and reports whether it answered. The
// error is shown to the user, so it must say what went wrong ("401
// Unauthorized") without quoting the credential that failed (SPEC §12).
type TestFunc func(ctx context.Context, cfg core.DownloadClientConfig) error

// Type describes one supported backend. The credential flags are data rather
// than a per-type validator so the settings form, the request validator and
// the protocol implementation cannot disagree about which fields a backend
// uses.
type Type struct {
	// Name is the stored value of download_clients.kind, one of the
	// core.DownloadClient* constants.
	Name string
	// Label is how the backend is written in the UI.
	Label string
	// Protocol is core.ProtocolTorrent or core.ProtocolUsenet: which releases
	// this backend can take.
	Protocol string
	// UsesLogin means the backend authenticates with a username and password.
	UsesLogin bool
	// UsesAPIKey means the backend authenticates with an API key.
	UsesAPIKey bool
}

// types is every backend Caravan knows how to configure, in the order the
// settings screen offers them.
//
// NZBGet is login rather than API key: its JSON-RPC endpoint authenticates
// with HTTP basic auth (a control username and password set in nzbget.conf),
// where SABnzbd takes an `apikey` query parameter.
var types = []Type{
	{
		Name:      core.DownloadClientQBittorrent,
		Label:     "qBittorrent",
		Protocol:  core.ProtocolTorrent,
		UsesLogin: true,
	},
	{
		Name:       core.DownloadClientSABnzbd,
		Label:      "SABnzbd",
		Protocol:   core.ProtocolUsenet,
		UsesAPIKey: true,
	},
	{
		Name:      core.DownloadClientNZBGet,
		Label:     "NZBGet",
		Protocol:  core.ProtocolUsenet,
		UsesLogin: true,
	},
}

// Types returns every supported backend. The slice is a copy: the table is
// shared by every caller and must not be editable through one of them.
func Types() []Type {
	out := make([]Type, len(types))
	copy(out, types)
	return out
}

// Lookup returns the type with the given name.
func Lookup(name string) (Type, bool) {
	for _, t := range types {
		if t.Name == name {
			return t, true
		}
	}
	return Type{}, false
}

// Validate reports why cfg cannot be used, or nil when it can. It checks the
// shape every backend shares — a name, a reachable-looking base URL, the
// credentials this type needs — and nothing backend-specific: whether the
// credentials are *right* is what TestConnection is for.
func (t Type) Validate(cfg core.DownloadClientConfig) error {
	if strings.TrimSpace(cfg.Name) == "" {
		return errors.New("name is required")
	}
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return errors.New("url is required")
	}
	// Parsed rather than pattern-matched: the client builds request URLs from
	// this string, so a value it cannot use should fail here, where the user
	// can fix it, rather than inside a background grab.
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("url must be an http or https URL")
	}
	if t.UsesAPIKey && strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("%s needs an API key", t.Label)
	}
	if t.UsesLogin && strings.TrimSpace(cfg.Username) == "" {
		return fmt.Errorf("%s needs a username", t.Label)
	}
	if cfg.Priority < 0 {
		return errors.New("priority must not be negative")
	}
	return nil
}

// Registry holds the connection probes registered for each type.
//
// It is a value rather than only a package global so tests — and a serving
// process that wants to run without a backend — can build their own instead of
// mutating shared state.
type Registry struct {
	mu    sync.RWMutex
	tests map[string]TestFunc
}

// Default is the registry the serving process wires and the API uses when it
// is given no other. Protocol implementations register into it.
var Default = NewRegistry()

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tests: make(map[string]TestFunc)}
}

// Register installs the connection probe for a type. Registering an unknown
// type, or the same type twice, is a wiring mistake rather than a runtime
// condition, so it is reported as an error the caller is expected to treat as
// fatal.
func (r *Registry) Register(name string, test TestFunc) error {
	if _, ok := Lookup(name); !ok {
		return fmt.Errorf("clients: register %q: unknown download client type", name)
	}
	if test == nil {
		return fmt.Errorf("clients: register %q: nil test function", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.tests[name]; dup {
		return fmt.Errorf("clients: register %q: already registered", name)
	}
	r.tests[name] = test
	return nil
}

// Supported reports whether an implementation is registered for name.
func (r *Registry) Supported(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tests[name]
	return ok
}

// TestConnection validates cfg and then asks the registered implementation
// whether the client answers.
//
// An unknown type and an unimplemented one are different failures: the first
// is a bad request, the second is this build not carrying the backend yet, and
// only the second wraps ErrNotSupported.
func (r *Registry) TestConnection(ctx context.Context, cfg core.DownloadClientConfig) error {
	t, ok := Lookup(cfg.Type)
	if !ok {
		return fmt.Errorf("clients: unknown download client type %q", cfg.Type)
	}
	if err := t.Validate(cfg); err != nil {
		return err
	}

	r.mu.RLock()
	test := r.tests[cfg.Type]
	r.mu.RUnlock()
	if test == nil {
		return fmt.Errorf("%s: %w", t.Label, ErrNotSupported)
	}
	return test(ctx, cfg)
}

// TypeNames returns the supported type names, sorted, for error messages that
// have to list them.
func TypeNames() []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}
