package nntp

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Defaults for the fields a user may leave blank.
const (
	// DefaultPort is the plaintext NNTP port.
	DefaultPort = 119
	// DefaultTLSPort is the implicit-TLS NNTP port (NNTPS). Every commercial
	// provider offers it and most only offer it.
	DefaultTLSPort = 563
	// DefaultMaxConnections is the connection cap used when a server is
	// configured without one.
	//
	// Eight because that is the low end of what providers sell, and going over
	// a provider's cap is not a slow download but a refused connection: the
	// safe default is the one that works on the cheapest account.
	DefaultMaxConnections = 8
)

// ServerConfig is one news server the embedded Usenet engine may fetch from
// (PLAN phase 7 task 1, `usenet_servers`).
//
// It is a plain struct on purpose. Like internal/download and
// internal/clients, this package does not import internal/store: the row is
// mapped into this by whoever owns the database, so the transport is testable
// with a literal and nothing else.
//
// Username and Password are credentials: they live in the database, never in
// the bootstrap YAML, never in a log line, and never in an error string
// (SPEC §12). Nothing in this package formats them into a message — Label is
// what error text uses to name a server.
type ServerConfig struct {
	// ID is the `usenet_servers.id` row this came from, 0 when unsaved. It is
	// carried so callers can attribute a failure to a row.
	ID int64
	// Name is the user-facing label. Empty falls back to the address.
	Name string
	// Host is the news server hostname.
	Host string
	// Port is the TCP port, 0 for the protocol default.
	Port int
	// TLS selects implicit TLS (NNTPS) rather than a plaintext socket. There
	// is no STARTTLS here: providers that matter all speak implicit TLS, and
	// an upgrade that can be stripped is worse than none.
	TLS bool
	// Username and Password authenticate with AUTHINFO. Empty Username means
	// the server is used anonymously and no AUTHINFO is sent.
	Username string
	Password string
	// MaxConnections is the per-server connection cap, 0 for
	// DefaultMaxConnections. It is a hard cap, not a target: exceeding a
	// provider's limit gets connections refused for everyone.
	MaxConnections int
	// Priority orders servers for failover: lowest wins, matching indexers and
	// download clients. Backup servers (higher numbers) are only asked for
	// articles the servers above them do not have.
	Priority int
	// Enabled excludes the server from fetching when false, without losing its
	// configuration.
	Enabled bool
}

// Validate reports whether the configuration can be dialled.
//
// It rejects CR and LF in the host and the credentials because those three
// values are interpolated into command lines: a newline in a password would
// let a stored credential inject an NNTP command.
func (c ServerConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("nntp: news server %s: host is required", c.Label())
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("nntp: news server %s: port %d is out of range", c.Label(), c.Port)
	}
	if c.MaxConnections < 0 {
		return fmt.Errorf("nntp: news server %s: max connections %d is negative", c.Label(), c.MaxConnections)
	}
	for _, f := range []struct{ name, value string }{
		{"host", c.Host},
		{"username", c.Username},
		{"password", c.Password},
	} {
		if strings.ContainsAny(f.value, "\r\n") {
			// The value itself is never echoed: for password that would be a
			// credential in a log line (SPEC §12).
			return fmt.Errorf("nntp: news server %s: %s contains a line break", c.Label(), f.name)
		}
	}
	if c.Password != "" && c.Username == "" {
		return fmt.Errorf("nntp: news server %s: password without a username", c.Label())
	}
	return nil
}

// Address is the "host:port" this server is dialled at, with the protocol
// default filled in.
func (c ServerConfig) Address() string {
	return net.JoinHostPort(strings.TrimSpace(c.Host), strconv.Itoa(c.port()))
}

// Label names the server in errors and logs. It is the user's name when they
// gave one and the address otherwise, and it never contains credentials.
func (c ServerConfig) Label() string {
	if n := strings.TrimSpace(c.Name); n != "" {
		return n
	}
	if strings.TrimSpace(c.Host) == "" {
		return "(unnamed)"
	}
	return c.Address()
}

// port is the configured port or the protocol default.
func (c ServerConfig) port() int {
	if c.Port > 0 {
		return c.Port
	}
	if c.TLS {
		return DefaultTLSPort
	}
	return DefaultPort
}

// connections is the configured cap or the default.
func (c ServerConfig) connections() int {
	if c.MaxConnections > 0 {
		return c.MaxConnections
	}
	return DefaultMaxConnections
}

// normalized returns the configuration with defaults filled in and whitespace
// trimmed, so everything downstream sees one shape.
func (c ServerConfig) normalized() ServerConfig {
	c.Host = strings.TrimSpace(c.Host)
	c.Name = strings.TrimSpace(c.Name)
	c.Port = c.port()
	c.MaxConnections = c.connections()
	return c
}
