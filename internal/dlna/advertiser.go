package dlna

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// advertiser is the SSDP half of the server: it multicasts "I am here" on a
// timer, answers M-SEARCH probes, and multicasts "I am gone" on the way out.
//
// One socket does both jobs. It is bound to the multicast group with
// ListenMulticastUDP, which sets SO_REUSEADDR so another DLNA server on the
// same host (a Jellyfin, a minidlna) keeps working alongside Caravan instead of
// one of them failing to start.
type advertiser struct {
	uuid string
	port int
	log  *slog.Logger
	// trace records discovery traffic for GET /api/v1/dlna (trace.go). Never
	// nil; startAdvertiser substitutes a no-op.
	trace func(remote, detail string)

	group *net.UDPAddr
	// listener receives multicast traffic: other devices' notifications and
	// clients' searches.
	listener *net.UDPConn
	// sender is an ephemeral-port socket used for outbound notifications, kept
	// separate so a write cannot interfere with the read loop's socket state.
	sender *net.UDPConn

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// startAdvertiser joins the SSDP group and begins advertising. Every failure
// here is a working-but-unadvertised server, never a dead one: hosts without
// multicast (a container on a bridge network, a VPN-only interface) are
// expected, and the caller logs and carries on.
func startAdvertiser(uuid string, port int, log *slog.Logger, trace func(remote, detail string)) (*advertiser, error) {
	if trace == nil {
		trace = func(string, string) {}
	}
	group, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil, fmt.Errorf("dlna: resolve ssdp group: %w", err)
	}
	listener, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, fmt.Errorf("dlna: join ssdp group: %w", err)
	}
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("dlna: open ssdp sender: %w", err)
	}

	a := &advertiser{
		uuid: uuid, port: port, log: log, trace: trace,
		group: group, listener: listener, sender: sender,
		stop: make(chan struct{}),
	}
	a.wg.Add(2)
	go a.announce()
	go a.serveSearches()
	return a, nil
}

// Close sends byebye for every target and shuts the sockets down.
func (a *advertiser) Close() {
	a.stopOnce.Do(func() {
		close(a.stop)
		// Unblocks the read loop's ReadFromUDP.
		a.listener.Close()
		a.wg.Wait()
		a.byebye()
		a.sender.Close()
	})
}

// announce multicasts alive notifications immediately and then on a timer.
//
// The initial burst is sent three times: SSDP runs on UDP with no
// retransmission, and a device that is only announced once disappears from the
// network for half an hour whenever that one datagram is dropped.
func (a *advertiser) announce() {
	defer a.wg.Done()

	a.alive()
	ticker := time.NewTicker(ssdpInterval)
	defer ticker.Stop()
	burst := time.NewTimer(time.Second)
	defer burst.Stop()
	bursts := 2

	for {
		select {
		case <-a.stop:
			return
		case <-burst.C:
			a.alive()
			if bursts--; bursts > 0 {
				burst.Reset(time.Second)
			}
		case <-ticker.C:
			a.alive()
		}
	}
}

func (a *advertiser) alive() {
	ip := a.localIP(a.group)
	if ip == "" {
		return
	}
	location := ssdpLocation(ip, a.port)
	for _, t := range ssdpTargets(a.uuid) {
		a.send(notifyAlive(t, location), a.group)
	}
}

func (a *advertiser) byebye() {
	for _, t := range ssdpTargets(a.uuid) {
		a.send(notifyByeBye(t), a.group)
	}
}

// serveSearches answers M-SEARCH probes until the listener is closed.
func (a *advertiser) serveSearches() {
	defer a.wg.Done()

	buf := make([]byte, 2048)
	for {
		n, from, err := a.listener.ReadFromUDP(buf)
		if err != nil {
			// The only expected error is the socket being closed by Close.
			select {
			case <-a.stop:
			default:
				a.log.Warn("dlna: ssdp read failed", "error", err)
			}
			return
		}
		req, ok := parseSearch(buf[:n])
		if !ok {
			continue
		}
		targets := matchSearchTargets(req.ST, a.uuid)
		a.trace(from.String(), fmt.Sprintf("M-SEARCH %s → %d targets", req.ST, len(targets)))
		if len(targets) == 0 {
			continue
		}
		// The reply is sent from its own goroutine because MX asks us to wait,
		// and waiting on the read loop would drop every search that arrives in
		// the meantime. It joins the wait group so Close does not shut the
		// sender socket while a delayed reply is still pending; the Add happens
		// before this loop's own Done, so Wait cannot return early.
		a.wg.Add(1)
		go a.respond(targets, *from, req.MX)
	}
}

// respond answers one search after the random delay MX asks for. The delay is
// the point of MX: without it every device on the LAN replies at once and the
// client's socket buffer decides which ones it heard.
func (a *advertiser) respond(targets []ssdpTarget, to net.UDPAddr, mx int) {
	defer a.wg.Done()

	if mx > 0 {
		delay := time.Duration(rand.Int64N(int64(mx) * int64(time.Second)))
		select {
		case <-a.stop:
			return
		case <-time.After(delay):
		}
	}

	ip := a.localIP(&to)
	if ip == "" {
		return
	}
	location := ssdpLocation(ip, a.port)
	now := time.Now()
	for _, t := range targets {
		a.send(searchResponse(t, location, now), &to)
	}
}

func (a *advertiser) send(msg []byte, to *net.UDPAddr) {
	if _, err := a.sender.WriteToUDP(msg, to); err != nil {
		a.log.Debug("dlna: ssdp send failed", "to", to.String(), "error", err)
	}
}

// localIP reports which of this host's addresses reaches remote, which is the
// address the advertised LOCATION has to carry.
//
// It is resolved per message rather than once at startup: the kernel's routing
// table is the only thing that knows the answer, and on a multi-homed host the
// answer differs between the multicast group and a particular searcher. The UDP
// "connection" is bookkeeping, nothing is sent, so this costs a syscall and
// never blocks.
func (a *advertiser) localIP(remote *net.UDPAddr) string {
	c, err := net.DialUDP("udp4", nil, remote)
	if err != nil {
		a.log.Debug("dlna: no route to ssdp peer", "peer", remote.String(), "error", err)
		return ""
	}
	defer c.Close()
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil || addr.IP.IsUnspecified() {
		return ""
	}
	return addr.IP.String()
}
