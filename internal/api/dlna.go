package api

import (
	"context"
	"net/http"

	"github.com/watzon/caravan/internal/dlna"
)

// DLNAService is the slice of internal/dlna the HTTP layer needs: the protocol
// handler to mount, the current state to report, and a way to re-read the
// settings after they change.
//
// It is an interface for the same reason EngineProvider is: a server built
// without one still serves the whole API, and GET /dlna reports the feature as
// off rather than the endpoint failing.
type DLNAService interface {
	// Handler is the DLNA protocol surface, mounted outside /api/v1 because
	// its URLs are fixed by what SSDP advertises.
	Handler() http.Handler
	// Status is the configuration plus whether SSDP is actually live.
	Status(ctx context.Context) (dlna.Status, error)
	// Reload re-reads the settings and starts or stops advertising to match.
	Reload(ctx context.Context)
}

// dlnaMountPrefix is where the protocol surface hangs off the root mux. The
// trailing slash makes it a subtree match; the package that owns the paths owns
// the constant.
const dlnaMountPrefix = dlna.MountPath + "/"

// WithDLNA supplies the built-in media server.
func WithDLNA(d DLNAService) Option {
	return func(s *server) { s.dlna = d }
}

// dlnaJSON is what the settings screen renders.
type dlnaJSON struct {
	Enabled      bool   `json:"enabled"`
	FriendlyName string `json:"friendly_name"`
	// UUID is the device identity clients see. It is shown so a user looking
	// at two Caravans, or at a stale entry in a TV's list, can tell which is
	// which.
	UUID string `json:"uuid"`
	// Advertising is whether SSDP is live right now, which is not the same as
	// Enabled: a host with no usable multicast reports enabled-but-silent.
	Advertising bool `json:"advertising"`
	// Error is why advertising is off despite being enabled. Empty otherwise.
	Error string `json:"error"`
}

// handleDLNAStatus reports the media server's state.
//
// There is no matching PUT: enabled and the friendly name are ordinary settings
// keys and go through PUT /settings with everything else (SPEC §10 — the table
// is the runtime configuration). This endpoint exists because the state SSDP is
// actually in cannot be read out of the settings table.
func (s *server) handleDLNAStatus(w http.ResponseWriter, r *http.Request) {
	if s.dlna == nil {
		// A build without the media server. Reporting "off" is the truth, and
		// lets the settings screen say so instead of showing a load error.
		writeJSON(w, http.StatusOK, dlnaJSON{})
		return
	}
	status, err := s.dlna.Status(r.Context())
	if err != nil {
		s.writeStoreError(w, "read dlna status", err)
		return
	}
	writeJSON(w, http.StatusOK, dlnaJSON{
		Enabled:      status.Enabled,
		FriendlyName: status.FriendlyName,
		UUID:         status.UUID,
		Advertising:  status.Advertising,
		Error:        status.Error,
	})
}
