package nntp

import (
	"strings"
	"testing"
)

func TestServerConfigDefaults(t *testing.T) {
	tests := []struct {
		name        string
		cfg         ServerConfig
		wantAddress string
		wantConns   int
	}{
		{
			name:        "plaintext default port",
			cfg:         ServerConfig{Host: "news.example"},
			wantAddress: "news.example:119",
			wantConns:   DefaultMaxConnections,
		},
		{
			name:        "tls default port",
			cfg:         ServerConfig{Host: "news.example", TLS: true},
			wantAddress: "news.example:563",
			wantConns:   DefaultMaxConnections,
		},
		{
			name:        "explicit values win",
			cfg:         ServerConfig{Host: " news.example ", Port: 1119, MaxConnections: 20},
			wantAddress: "news.example:1119",
			wantConns:   20,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Address(); got != tc.wantAddress {
				t.Fatalf("Address() = %q, want %q", got, tc.wantAddress)
			}
			n := tc.cfg.normalized()
			if n.MaxConnections != tc.wantConns {
				t.Fatalf("MaxConnections = %d, want %d", n.MaxConnections, tc.wantConns)
			}
			if n.Address() != tc.wantAddress {
				t.Fatalf("normalized Address() = %q, want %q", n.Address(), tc.wantAddress)
			}
		})
	}
}

func TestServerConfigLabelPrefersTheUsersName(t *testing.T) {
	if got := (ServerConfig{Host: "news.example", Name: "Eweka"}).Label(); got != "Eweka" {
		t.Fatalf("Label() = %q, want the configured name", got)
	}
	if got := (ServerConfig{Host: "news.example"}).Label(); got != "news.example:119" {
		t.Fatalf("Label() = %q, want the address", got)
	}
	if got := (ServerConfig{}).Label(); got != "(unnamed)" {
		t.Fatalf("Label() = %q", got)
	}
}

// Host, username and password are interpolated into command lines, so a line
// break in any of them is command injection with a stored credential.
func TestServerConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServerConfig
		wantErr string
	}{
		{"ok", ServerConfig{Host: "news.example", Username: "u", Password: "s3cr3t-do-not-log"}, ""},
		{"ok anonymous", ServerConfig{Host: "news.example"}, ""},
		{"no host", ServerConfig{}, "host is required"},
		{"blank host", ServerConfig{Host: "   "}, "host is required"},
		{"port too high", ServerConfig{Host: "news.example", Port: 70000}, "out of range"},
		{"negative port", ServerConfig{Host: "news.example", Port: -1}, "out of range"},
		{"negative connections", ServerConfig{Host: "news.example", MaxConnections: -1}, "negative"},
		{"newline in host", ServerConfig{Host: "news\r\nQUIT"}, "host contains a line break"},
		{"newline in username", ServerConfig{Host: "h", Username: "u\r\nQUIT"}, "username contains a line break"},
		{"newline in password", ServerConfig{Host: "h", Username: "u", Password: "s3cr3t-do-not-log\nQUIT"}, "password contains a line break"},
		{"password without username", ServerConfig{Host: "h", Password: "s3cr3t-do-not-log"}, "password without a username"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want it to mention %q", err, tc.wantErr)
			}
			// SPEC §12: the rejected value is named, never echoed.
			if tc.cfg.Password != "" && strings.Contains(err.Error(), tc.cfg.Password) {
				t.Fatalf("error text leaked the password: %v", err)
			}
		})
	}
}
