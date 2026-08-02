// Package usenet joins the stored news-server configuration to the transport
// that dials it.
//
// internal/usenet/nntp holds no database knowledge on purpose — a server there
// is a plain struct, so the whole transport is testable with a literal — and
// internal/core imports nothing at all. This package is the one place the two
// shapes meet, so the mapping exists once rather than in every caller that
// wants to open a pool.
package usenet

import (
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/usenet/nntp"
)

// ServerConfig converts a stored server into the transport's configuration,
// with the protocol defaults resolved so the value that gets dialled is the
// value the settings screen showed.
func ServerConfig(c core.UsenetServerConfig) nntp.ServerConfig {
	return nntp.ServerConfig{
		ID:             c.ID,
		Name:           c.Name,
		Host:           c.Host,
		Port:           c.ResolvedPort(),
		TLS:            c.TLS,
		Username:       c.Username,
		Password:       c.Password,
		MaxConnections: c.ResolvedMaxConnections(),
		Priority:       c.Priority,
		Enabled:        c.Enabled,
	}
}

// ServerConfigs converts a list, preserving order. Feed it
// store.ListEnabledUsenetServers to build the engine's pool: that query
// already returns priority order, which is the order nntp.NewMultiPool fails
// over in.
func ServerConfigs(cs []core.UsenetServerConfig) []nntp.ServerConfig {
	out := make([]nntp.ServerConfig, 0, len(cs))
	for _, c := range cs {
		out = append(out, ServerConfig(c))
	}
	return out
}
