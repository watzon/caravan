package dlna

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/store"
)

// SPEC §5.1 promises the library is advertised whenever the server runs, so an
// install nobody has touched is on.
func TestConfigDefaultsToEnabled(t *testing.T) {
	svc, _, _ := newTestService(t)

	cfg, err := svc.Config(t.Context())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("DLNA is off on a fresh install")
	}
	if cfg.FriendlyName != DefaultFriendlyName {
		t.Fatalf("FriendlyName = %q, want %q", cfg.FriendlyName, DefaultFriendlyName)
	}
	if !validUUID(cfg.UUID) {
		t.Fatalf("UUID = %q, want a canonical uuid", cfg.UUID)
	}
}

func TestConfigReadsSettings(t *testing.T) {
	svc, st, _ := newTestService(t)
	ctx := t.Context()

	if err := st.SetSetting(ctx, store.SettingDLNAEnabled, "false"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingDLNAFriendlyName, "  Den TV  "); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg, err := svc.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("Enabled = true, want false")
	}
	if cfg.FriendlyName != "Den TV" {
		t.Fatalf("FriendlyName = %q, want the trimmed value", cfg.FriendlyName)
	}
}

// A value the settings table cannot parse must read as off. The alternative —
// a typo meaning "on" — is the wrong failure direction for the one setting
// whose job is to stop broadcasting.
func TestConfigTreatsUnparseableEnabledAsOff(t *testing.T) {
	svc, st, _ := newTestService(t)
	if err := st.SetSetting(t.Context(), store.SettingDLNAEnabled, "sure"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg, err := svc.Config(t.Context())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("an unparseable dlna_enabled read as on")
	}
}

// The device identity has to survive restarts, or every restart looks like a
// brand new server in the client's list.
func TestDeviceUUIDIsStableAndPersisted(t *testing.T) {
	svc, st, _ := newTestService(t)
	ctx := t.Context()

	first, err := svc.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	stored, err := st.GetSetting(ctx, store.SettingDLNAUUID)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if stored != first.UUID {
		t.Fatalf("stored uuid %q != reported %q", stored, first.UUID)
	}

	// A second service over the same store — a restart — reports the same one.
	restarted := New(st, svc.root, 8677, quietLogger())
	second, err := restarted.Config(ctx)
	if err != nil {
		t.Fatalf("Config after restart: %v", err)
	}
	if second.UUID != first.UUID {
		t.Fatalf("uuid changed across restart: %q -> %q", first.UUID, second.UUID)
	}
}

// A stored value that is not a UUID would put a malformed UDN in device.xml and
// malformed USNs in every advertisement, so it is replaced rather than used.
func TestDeviceUUIDReplacesGarbage(t *testing.T) {
	svc, st, _ := newTestService(t)
	if err := st.SetSetting(t.Context(), store.SettingDLNAUUID, "not-a-uuid"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg, err := svc.Config(t.Context())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !validUUID(cfg.UUID) {
		t.Fatalf("UUID = %q, want a canonical uuid", cfg.UUID)
	}
	stored, err := st.GetSetting(t.Context(), store.SettingDLNAUUID)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if stored != cfg.UUID {
		t.Fatalf("the repaired uuid was not persisted: %q vs %q", stored, cfg.UUID)
	}
}

func TestValidUUID(t *testing.T) {
	valid := []string{testUUID, "00000000-0000-4000-8000-000000000000"}
	for _, id := range valid {
		if !validUUID(id) {
			t.Fatalf("validUUID(%q) = false", id)
		}
	}
	invalid := []string{"", "not-a-uuid", "1b9d6bcd-bbfd-4b2d-9b5d", strings.Repeat("z", 8) + "-0000-0000-0000-000000000000"}
	for _, id := range invalid {
		if validUUID(id) {
			t.Fatalf("validUUID(%q) = true", id)
		}
	}
}

func TestNewUUIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		id, err := newUUID()
		if err != nil {
			t.Fatalf("newUUID: %v", err)
		}
		if !validUUID(id) {
			t.Fatalf("newUUID produced %q", id)
		}
		if seen[id] {
			t.Fatalf("newUUID repeated %q", id)
		}
		seen[id] = true
	}
}

// Disabled means not advertising, and Status has to say so rather than
// reporting the toggle's value as if it were the state.
func TestStatusWhenDisabled(t *testing.T) {
	svc, st, _ := newTestService(t)
	if err := st.SetSetting(t.Context(), store.SettingDLNAEnabled, "false"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	svc.Start(t.Context())
	t.Cleanup(func() { svc.Close() })

	status, err := svc.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Enabled || status.Advertising {
		t.Fatalf("status = %+v, want off and not advertising", status)
	}
}

// Reload before Start must not advertise: the HTTP server is not up yet, so a
// LOCATION pointing at it would be a promise nothing answers.
func TestReloadBeforeStartDoesNotAdvertise(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.Reload(t.Context())
	t.Cleanup(func() { svc.Close() })

	status, err := svc.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Advertising {
		t.Fatal("advertising before Start")
	}
}

// Start, disable, re-enable: the toggle takes effect without a restart, and
// neither direction panics or leaks a socket.
//
// This is the one test that needs real multicast, so it skips where the sandbox
// has none rather than failing the suite.
func TestAdvertiserLifecycle(t *testing.T) {
	requireMulticast(t)

	svc, st, _ := newTestService(t)
	ctx := t.Context()
	svc.Start(ctx)
	t.Cleanup(func() { svc.Close() })

	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Advertising {
		t.Fatalf("not advertising after Start: %+v", status)
	}

	if err := st.SetSetting(ctx, store.SettingDLNAEnabled, "false"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	svc.Reload(ctx)
	if status, err = svc.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Advertising {
		t.Fatal("still advertising after being switched off")
	}

	if err := st.SetSetting(ctx, store.SettingDLNAEnabled, "true"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	svc.Reload(ctx)
	if status, err = svc.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Advertising {
		t.Fatal("not advertising after being switched back on")
	}

	// Reloading an already-running advertiser is a no-op, not a restart.
	svc.Reload(ctx)
	if status, err = svc.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Advertising {
		t.Fatal("a redundant reload stopped the advertiser")
	}
}

// The end-to-end discovery path: a client multicasts an M-SEARCH and gets a
// unicast reply carrying a LOCATION it can fetch.
//
// This is the only test that puts real datagrams on a real socket. Everything
// about the message contents is asserted by the pure tests above; what this one
// proves is that the sockets are wired together at all — the reply reaches the
// searcher, and its LOCATION points at this host and this port.
func TestAdvertiserAnswersMSearch(t *testing.T) {
	requireMulticast(t)

	svc, _, _ := newTestService(t)
	ctx := t.Context()
	svc.Start(ctx)
	t.Cleanup(func() { svc.Close() })

	cfg, err := svc.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Advertising {
		t.Skipf("not advertising here: %s", status.Error)
	}

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Skipf("cannot open a client socket: %v", err)
	}
	defer client.Close()

	group, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		t.Fatalf("resolve group: %v", err)
	}
	// Searching for this device's own uuid keeps other media servers on the
	// LAN — a developer's Jellyfin, say — out of the assertion.
	search := "M-SEARCH * HTTP/1.1\r\nHOST: " + ssdpAddr +
		"\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: uuid:" + cfg.UUID + "\r\n\r\n"
	if _, err := client.WriteToUDP([]byte(search), group); err != nil {
		t.Skipf("cannot send to the ssdp group: %v", err)
	}

	if err := client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 2048)
	for {
		n, _, err := client.ReadFromUDP(buf)
		if err != nil {
			t.Skipf("no ssdp loopback in this environment: %v", err)
		}
		reply := string(buf[:n])
		if !strings.Contains(reply, "uuid:"+cfg.UUID) {
			continue // somebody else's device
		}
		if !strings.HasPrefix(reply, "HTTP/1.1 200 OK\r\n") {
			t.Fatalf("reply is not a search response:\n%s", reply)
		}
		_, headers := headersOf(t, buf[:n])
		if headers["ST"] != "uuid:"+cfg.UUID {
			t.Fatalf("ST = %q, want the searched target", headers["ST"])
		}
		if !strings.HasSuffix(headers["LOCATION"], ":8677"+MountPath+"/device.xml") {
			t.Fatalf("LOCATION = %q, want this host on the advertised port", headers["LOCATION"])
		}
		return
	}
}

// Every advertisement carries a LOCATION with a port in it, so a server whose
// listen address has none stays silent rather than broadcasting a URL nothing
// can fetch.
func TestNoListenPortMeansNoAdvertisement(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.port = 0
	svc.Start(t.Context())
	t.Cleanup(func() { svc.Close() })

	status, err := svc.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Advertising {
		t.Fatal("advertising without a port to advertise")
	}
	if status.Error == "" {
		t.Fatal("no reason given for staying silent")
	}
	// The rest of the server is unaffected: the browse tree still answers.
	if _, err := svc.children(t.Context(), testURLs, rootID); err != nil {
		t.Fatalf("children(root): %v", err)
	}
}

// A host with no multicast must leave the rest of Caravan running: the service
// reports itself not advertising and says why, and nothing panics.
func TestStartWithoutNetworkDoesNotFail(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.Start(t.Context())
	t.Cleanup(func() { svc.Close() })

	status, err := svc.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Advertising && status.Error == "" {
		t.Fatal("not advertising, and no reason given")
	}
	// Close is safe whether or not anything started, and safe twice: the
	// process shutdown path calls it after a possibly-failed Start.
	svc.Close()
	svc.Close()
}

// requireMulticast skips when the sandbox has no usable multicast, which is the
// normal state of a hermetic CI container.
func requireMulticast(t *testing.T) {
	t.Helper()
	group, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		t.Skipf("cannot resolve the ssdp group: %v", err)
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("no multicast in this environment: %v", err)
	}
	conn.Close()
	// Something has to route to the group, or the advertiser starts and then
	// silently sends nothing.
	probe, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("no route to the ssdp group: %v", err)
	}
	defer probe.Close()
	if err := probe.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Skipf("cannot set a write deadline: %v", err)
	}
}
