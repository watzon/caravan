package clients

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func qbit() core.DownloadClientConfig {
	return core.DownloadClientConfig{
		Type:     core.DownloadClientQBittorrent,
		Name:     "qBit",
		URL:      "http://127.0.0.1:8080",
		Username: "admin",
		Password: "adminadmin",
		Enabled:  true,
	}
}

// Every type has to declare a protocol and exactly one credential shape:
// routing reads the first, the settings form and the validator read the
// second, and a type that declares neither would be unconfigurable.
func TestTypesDeclareProtocolAndCredentials(t *testing.T) {
	for _, ty := range Types() {
		if ty.Protocol != core.ProtocolTorrent && ty.Protocol != core.ProtocolUsenet {
			t.Errorf("%s protocol = %q, want torrent or usenet", ty.Name, ty.Protocol)
		}
		if ty.Label == "" {
			t.Errorf("%s has no label", ty.Name)
		}
		if !ty.UsesLogin && !ty.UsesAPIKey {
			t.Errorf("%s declares no credential shape", ty.Name)
		}
	}
	if _, ok := Lookup("transmission"); ok {
		t.Error("Lookup(transmission) = ok, want unknown")
	}
	if _, ok := Lookup(core.DownloadClientNZBGet); !ok {
		t.Error("Lookup(nzbget) = unknown, want ok")
	}
}

// Types returns a copy: a caller that edits the slice must not be able to
// rewrite what every other caller sees.
func TestTypesIsACopy(t *testing.T) {
	got := Types()
	got[0].Label = "clobbered"
	if Types()[0].Label == "clobbered" {
		t.Fatal("Types() handed out the shared table")
	}
}

func TestValidate(t *testing.T) {
	sab := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "SAB", URL: "http://127.0.0.1:8080", APIKey: "k",
	}

	tests := []struct {
		name    string
		typ     string
		cfg     core.DownloadClientConfig
		wantErr string
	}{
		{"valid login client", core.DownloadClientQBittorrent, qbit(), ""},
		{"valid api key client", core.DownloadClientSABnzbd, sab, ""},
		{"no name", core.DownloadClientQBittorrent, func() core.DownloadClientConfig {
			c := qbit()
			c.Name = "  "
			return c
		}(), "name is required"},
		{"no url", core.DownloadClientQBittorrent, func() core.DownloadClientConfig {
			c := qbit()
			c.URL = ""
			return c
		}(), "url is required"},
		{"url without scheme", core.DownloadClientQBittorrent, func() core.DownloadClientConfig {
			c := qbit()
			c.URL = "127.0.0.1:8080"
			return c
		}(), "http or https"},
		{"url with wrong scheme", core.DownloadClientQBittorrent, func() core.DownloadClientConfig {
			c := qbit()
			c.URL = "ftp://127.0.0.1"
			return c
		}(), "http or https"},
		{"login client without username", core.DownloadClientQBittorrent, func() core.DownloadClientConfig {
			c := qbit()
			c.Username = ""
			return c
		}(), "needs a username"},
		{"api key client without key", core.DownloadClientSABnzbd, func() core.DownloadClientConfig {
			c := sab
			c.APIKey = " "
			return c
		}(), "needs an API key"},
		{"negative priority", core.DownloadClientQBittorrent, func() core.DownloadClientConfig {
			c := qbit()
			c.Priority = -1
			return c
		}(), "priority"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ty, ok := Lookup(tt.typ)
			if !ok {
				t.Fatalf("Lookup(%q) unknown", tt.typ)
			}
			err := ty.Validate(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate = %v, want an error mentioning %q", err, tt.wantErr)
			}
		})
	}
}

// An empty password is not a validation failure: qBittorrent can be configured
// to skip authentication for localhost, and a wrong credential is what the
// connection test is for.
func TestValidateAllowsBlankPassword(t *testing.T) {
	ty, _ := Lookup(core.DownloadClientQBittorrent)
	cfg := qbit()
	cfg.Password = ""
	if err := ty.Validate(cfg); err != nil {
		t.Fatalf("Validate with a blank password = %v, want nil", err)
	}
}

// The point of this track standing alone: a type nobody has implemented yet is
// configurable and testable, and the test says why it cannot run rather than
// pretending the client is broken.
func TestTestConnectionUnregisteredTypeIsNotSupported(t *testing.T) {
	r := NewRegistry()
	if r.Supported(core.DownloadClientQBittorrent) {
		t.Fatal("a fresh registry claims qbittorrent is supported")
	}

	err := r.TestConnection(context.Background(), qbit())
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("TestConnection = %v, want ErrNotSupported", err)
	}
	// The message names the backend, so the UI does not have to.
	if !strings.Contains(err.Error(), "qBittorrent") {
		t.Errorf("TestConnection error = %q, want it to name the backend", err)
	}
}

func TestTestConnectionUnknownTypeIsNotErrNotSupported(t *testing.T) {
	r := NewRegistry()
	cfg := qbit()
	cfg.Type = "transmission"

	err := r.TestConnection(context.Background(), cfg)
	if err == nil {
		t.Fatal("TestConnection with an unknown type = nil, want an error")
	}
	if errors.Is(err, ErrNotSupported) {
		t.Fatalf("TestConnection = %v, want a plain unknown-type error, not ErrNotSupported", err)
	}
	if !strings.Contains(err.Error(), "transmission") {
		t.Errorf("TestConnection error = %q, want it to name the rejected type", err)
	}
}

// Validation runs before the probe: a configuration that cannot possibly work
// must not cost a network round trip.
func TestTestConnectionValidatesBeforeProbing(t *testing.T) {
	r := NewRegistry()
	probed := false
	if err := r.Register(core.DownloadClientQBittorrent, func(context.Context, core.DownloadClientConfig) error {
		probed = true
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cfg := qbit()
	cfg.URL = "not a url"
	if err := r.TestConnection(context.Background(), cfg); err == nil {
		t.Fatal("TestConnection with a bad url = nil, want a validation error")
	}
	if probed {
		t.Fatal("the probe ran for a configuration that failed validation")
	}
}

func TestRegisterAndProbe(t *testing.T) {
	r := NewRegistry()
	want := errors.New("403 Forbidden")
	var got core.DownloadClientConfig
	if err := r.Register(core.DownloadClientQBittorrent, func(_ context.Context, cfg core.DownloadClientConfig) error {
		got = cfg
		return want
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !r.Supported(core.DownloadClientQBittorrent) {
		t.Fatal("Supported = false after Register")
	}
	if err := r.TestConnection(context.Background(), qbit()); !errors.Is(err, want) {
		t.Fatalf("TestConnection = %v, want the probe's own error", err)
	}
	if got.Username != "admin" || got.Password != "adminadmin" {
		t.Fatalf("probe saw %+v, want the whole configuration including credentials", got)
	}
	// One registry, one backend: a second registration is a wiring mistake.
	if err := r.Register(core.DownloadClientQBittorrent, func(context.Context, core.DownloadClientConfig) error {
		return nil
	}); err == nil {
		t.Fatal("Register twice = nil, want an error")
	}
}

func TestRegisterRejectsUnknownTypeAndNilProbe(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("transmission", func(context.Context, core.DownloadClientConfig) error {
		return nil
	}); err == nil {
		t.Error("Register(unknown type) = nil, want an error")
	}
	if err := r.Register(core.DownloadClientSABnzbd, nil); err == nil {
		t.Error("Register(nil probe) = nil, want an error")
	}
}

// The registry is read from HTTP handlers while the serving process may still
// be registering backends, so it has to be safe to use from several goroutines.
func TestRegistryIsConcurrencySafe(t *testing.T) {
	r := NewRegistry()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			_ = r.Supported(core.DownloadClientSABnzbd)
			_ = r.TestConnection(context.Background(), qbit())
		}
	}()
	for _, name := range []string{core.DownloadClientQBittorrent, core.DownloadClientSABnzbd, core.DownloadClientNZBGet} {
		_ = r.Register(name, func(context.Context, core.DownloadClientConfig) error { return nil })
	}
	<-done
}
