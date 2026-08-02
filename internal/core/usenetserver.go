package core

// Defaults for the fields a user may leave blank. They repeat
// internal/usenet/nntp's own defaults on purpose: core is the leaf domain
// package and imports nothing, so the constants live here for the API to
// resolve a submitted configuration with, and the transport still fills the
// same values in for anything that reaches it unresolved.
const (
	// UsenetDefaultPort is the plaintext NNTP port.
	UsenetDefaultPort = 119
	// UsenetDefaultTLSPort is the implicit-TLS NNTP port (NNTPS).
	UsenetDefaultTLSPort = 563
	// UsenetDefaultMaxConnections is the per-server connection cap used when a
	// server is configured without one.
	UsenetDefaultMaxConnections = 8
	// UsenetDefaultPriority matches the `usenet_servers.priority` column
	// default, so a server added without an opinion sorts alongside one added
	// before the field was on the form.
	UsenetDefaultPriority = 25
)

// UsenetServerConfig is one news server the embedded Usenet engine fetches
// article bodies from (SPEC §5.1, §7 `usenet_servers`).
//
// It is an article SOURCE, not a download client: nothing here is handed an
// NZB and polled for progress. The built-in engine reads from these servers
// itself, which is why a user who never configures an external client still
// needs at least one of these to download from Usenet at all.
//
// The field set is deliberately identical to internal/usenet/nntp.ServerConfig.
// core imports nothing, so the transport cannot be named here; internal/usenet
// owns the one conversion between the two.
type UsenetServerConfig struct {
	ID int64
	// Name is the user-facing label, unique across servers. It is what names
	// the server in engine errors, so it never has to be a hostname.
	Name string
	// Host is the news server hostname.
	Host string
	// Port is the TCP port. 0 means the protocol default; the API resolves it
	// before storing, so a row read back from the database names a real port.
	Port int
	// TLS selects implicit TLS (NNTPS) rather than a plaintext socket.
	TLS bool
	// Username authenticates with AUTHINFO. Empty means the server is used
	// anonymously and no AUTHINFO is sent.
	Username string
	// Password is a credential: it lives in the database, never in the
	// bootstrap YAML, never in a log line, and never in an API response
	// (SPEC §12).
	Password string
	// MaxConnections is the per-server connection cap, 0 for the default. It
	// is a hard cap, not a target: exceeding a provider's limit gets
	// connections refused rather than throughput reduced.
	MaxConnections int
	// Priority orders servers for failover: lowest wins, matching indexers and
	// download clients. Higher numbers are backup servers, asked only for the
	// articles the servers above them did not have.
	Priority int
	// Enabled excludes the server from fetching when false, without losing its
	// configuration.
	Enabled bool
}

// ResolvedPort is the port this server is dialled at, with the protocol
// default filled in for a configuration that left it blank.
func (c UsenetServerConfig) ResolvedPort() int {
	if c.Port > 0 {
		return c.Port
	}
	if c.TLS {
		return UsenetDefaultTLSPort
	}
	return UsenetDefaultPort
}

// ResolvedMaxConnections is the connection cap with the default filled in.
func (c UsenetServerConfig) ResolvedMaxConnections() int {
	if c.MaxConnections > 0 {
		return c.MaxConnections
	}
	return UsenetDefaultMaxConnections
}
