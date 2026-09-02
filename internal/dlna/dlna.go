// Package dlna is Caravan's built-in DLNA/UPnP-AV media server (SPEC §5.1): the
// thing that makes a smart TV on the LAN see the library in its own "media
// servers" list without any client software.
//
// Scope is deliberately narrow: browse and serve, nothing else. The library is
// exposed as a container tree (root → Movies / TV → series → seasons → items)
// and every item points at the file on disk, served over plain HTTP with byte
// ranges. There is no transcoding: Caravan's answer to "my TV cannot play this"
// is the convert-for-TV queue (SPEC §8), which produces a file every client can
// decode, rather than a per-client stream this server would have to keep alive.
//
// The protocol surface is hand-rolled on top of net/http and net's UDP support.
// The obvious dependency, huin/goupnp, is a control-point (client) library: it
// discovers and calls other people's devices, which is the opposite direction
// from a server. SSDP is a handful of datagram formats and ContentDirectory is
// three SOAP actions, so the wire format is written out here where it can be
// asserted on directly instead of being assembled through a generic SOAP stack.
//
// Client variance is unbounded, so this implements the spec and stops there: no
// per-TV workarounds. Reference clients that are known to work are listed in
// docs/dlna.md.
package dlna

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/watzon/caravan/internal/store"
)

// MountPath is where the DLNA HTTP surface hangs off Caravan's own server. It
// shares the listener with the API and the SPA: a DLNA device is identified by
// its LOCATION URL, not by owning a port, and one listener is one firewall rule
// for the user to think about instead of two.
const MountPath = "/dlna"

// UPnP device and service types this server implements. The versions are part
// of the identifier, not decoration: a client searching for MediaServer:1 will
// not match a bare "MediaServer".
const (
	deviceType            = "urn:schemas-upnp-org:device:MediaServer:1"
	contentDirectoryType  = "urn:schemas-upnp-org:service:ContentDirectory:1"
	connectionManagerType = "urn:schemas-upnp-org:service:ConnectionManager:1"
)

// DefaultFriendlyName is what the device calls itself on the network when the
// user has not renamed it.
const DefaultFriendlyName = "Caravan"

// RootFunc resolves the storage root in force right now. It is a function
// rather than a captured string for the same reason the library adapter reads
// it per call: the root is editable from the settings screen at runtime.
type RootFunc func(ctx context.Context) (string, error)

// Config is the DLNA half of the settings table.
type Config struct {
	Enabled      bool   `json:"enabled"`
	FriendlyName string `json:"friendly_name"`
	UUID         string `json:"uuid"`
}

// Status is Config plus what the server is actually doing, which is not always
// the same thing: a host with no usable multicast reports enabled-but-not-
// advertising rather than pretending the toggle failed.
type Status struct {
	Config
	Advertising bool `json:"advertising"`
	// Error is why advertising is off despite being enabled. Empty otherwise.
	Error string `json:"error"`
}

// Service owns the DLNA server: its configuration, its HTTP surface, and the
// SSDP advertiser that tells the LAN it exists.
type Service struct {
	st   *store.Store
	root RootFunc
	log  *slog.Logger
	// port is the port Caravan's HTTP server listens on, which is what the
	// advertised LOCATION URL has to point at.
	port int

	mu sync.Mutex
	// started records that Start has run. Reload before that only writes
	// settings: a server that is not serving HTTP yet must not advertise a
	// LOCATION nothing answers.
	started bool
	adv     *advertiser
	// uuid is resolved once and cached, so device.xml and the SSDP USNs cannot
	// disagree about who this device is.
	uuid    string
	lastErr string
	// subs is the GENA subscriber registry (events.go), created lazily.
	subs *subscribers
	// tr is the request-trace ring (trace.go), created lazily.
	tr *trace
}

// New builds the service. It does no I/O and starts nothing: advertising begins
// at Start, so a test or a headless build can mount the HTTP surface without
// touching the network.
func New(st *store.Store, root RootFunc, port int, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, root: root, log: log, port: port}
}

// Start begins advertising if DLNA is enabled. A failure to reach the network
// is logged and swallowed: SPEC §13 wants the failure visible, but a laptop on
// a VPN with no multicast must still run Caravan.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	s.Reload(ctx)
}

// Reload re-reads the settings and starts or stops the advertiser to match. It
// is what PUT /settings calls after committing, so the toggle takes effect
// without a restart.
func (s *Service) Reload(ctx context.Context) {
	cfg, err := s.Config(ctx)
	if err != nil {
		s.log.Error("dlna: read settings", "error", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !cfg.Enabled:
		s.stopLocked()
	case !s.started || s.adv != nil:
		// Nothing to do: either the HTTP server is not up yet, or the
		// advertiser is already running. The friendly name is read per request
		// from device.xml, so renaming needs no restart.
	case s.port <= 0:
		// Every advertisement carries a LOCATION with a port in it. Without one
		// there is nothing to point clients at, so the device stays silent and
		// says why rather than broadcasting a URL that cannot be fetched.
		s.lastErr = "no listen port to advertise"
		s.log.Warn("dlna: not advertising", "error", s.lastErr)
	default:
		adv, err := startAdvertiser(cfg.UUID, s.port, s.log, s.traceSSDP)
		if err != nil {
			s.lastErr = err.Error()
			s.log.Warn("dlna: not advertising", "error", err)
			return
		}
		s.adv, s.lastErr = adv, ""
		s.log.Info("dlna: advertising", "uuid", cfg.UUID, "port", s.port, "name", cfg.FriendlyName)
	}
}

// Close stops advertising, sending the byebye notifications that tell clients
// to drop the device now rather than waiting out its cache lifetime.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
	return nil
}

func (s *Service) stopLocked() {
	if s.adv == nil {
		return
	}
	s.adv.Close()
	s.adv = nil
	s.lastErr = ""
}

// Config reads the current configuration, generating and persisting the device
// UUID on first use. Enabled defaults to true: SPEC §5.1 says the library is
// advertised whenever the server is running, so an untouched install is on.
func (s *Service) Config(ctx context.Context) (Config, error) {
	values, err := s.st.AllSettings(ctx)
	if err != nil {
		return Config{}, err
	}

	enabled := true
	if raw, ok := values[store.SettingDLNAEnabled]; ok {
		// An unparseable value reads as off. Anything else would make a typo in
		// the settings table mean "on", which is the wrong default for a
		// setting whose whole job is to stop broadcasting.
		enabled, _ = strconv.ParseBool(strings.TrimSpace(raw))
	}

	name := strings.TrimSpace(values[store.SettingDLNAFriendlyName])
	if name == "" {
		name = DefaultFriendlyName
	}

	uuid, err := s.deviceUUID(ctx, values[store.SettingDLNAUUID])
	if err != nil {
		return Config{}, err
	}
	return Config{Enabled: enabled, FriendlyName: name, UUID: uuid}, nil
}

// Status reports the configuration plus whether SSDP is live.
func (s *Service) Status(ctx context.Context) (Status, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Config: cfg, Advertising: s.adv != nil, Error: s.lastErr}, nil
}

// deviceUUID returns the stable device identity, generating one when the stored
// value is missing or malformed.
//
// It is cached on the service because two callers racing to generate would
// otherwise hand out two identities for the same running server, which clients
// would render as two Caravans.
func (s *Service) deviceUUID(ctx context.Context, stored string) (string, error) {
	s.mu.Lock()
	if s.uuid != "" {
		defer s.mu.Unlock()
		return s.uuid, nil
	}
	s.mu.Unlock()

	if id := strings.TrimSpace(stored); validUUID(id) {
		s.mu.Lock()
		s.uuid = id
		s.mu.Unlock()
		return id, nil
	}

	id, err := newUUID()
	if err != nil {
		return "", err
	}
	if err := s.st.SetSetting(ctx, store.SettingDLNAUUID, id); err != nil {
		return "", err
	}
	s.mu.Lock()
	// A concurrent caller may have won; its value is already persisted and just
	// as good, so the first one to land wins rather than the last one to write.
	if s.uuid == "" {
		s.uuid = id
	}
	id = s.uuid
	s.mu.Unlock()
	return id, nil
}

// newUUID builds a random RFC 4122 version-4 UUID. This is the only identifier
// the package needs, so it is 10 lines here rather than a dependency.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("dlna: generate uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// validUUID accepts the canonical 8-4-4-4-12 hex form. A stored value that is
// not one is replaced rather than advertised: the UDN in device.xml and the
// USNs in every SSDP message have to be the same well-formed string or clients
// treat them as different devices.
func validUUID(id string) bool {
	groups := strings.Split(id, "-")
	if len(groups) != 5 {
		return false
	}
	for i, want := range []int{8, 4, 4, 4, 12} {
		if len(groups[i]) != want {
			return false
		}
		if _, err := hex.DecodeString(groups[i]); err != nil {
			return false
		}
	}
	return true
}
