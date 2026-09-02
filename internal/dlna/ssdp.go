package dlna

import (
	"bufio"
	"bytes"
	"fmt"
	"net/textproto"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ssdpAddr is the SSDP multicast group and port, fixed by UPnP.
const ssdpAddr = "239.255.255.250:1900"

// ssdpMaxAge is the advertised CACHE-CONTROL lifetime in seconds. UPnP requires
// at least 1800 and requires the device to re-announce well before it expires;
// ssdpInterval is that "well before".
const ssdpMaxAge = 1800

// ssdpInterval is how often alive notifications are re-sent. Half of max-age
// means a client has to miss two consecutive announcements, on a protocol with
// no delivery guarantee, before it drops the device.
const ssdpInterval = ssdpMaxAge / 2 * time.Second

// ssdpMaxMX caps how long a search response may be delayed. Clients ask for a
// spread with MX so a hundred devices do not answer in the same millisecond;
// the spec caps the honoured value at 5 seconds regardless of what was asked.
const ssdpMaxMX = 5

// ssdpServer is the SERVER header: "OS/version UPnP/version product/version".
// Clients log it and occasionally branch on it, so it is a real triple rather
// than a marketing string.
var ssdpServer = runtime.GOOS + "/1.0 UPnP/1.0 Caravan/1.0"

// ssdpTarget is one (notification type, unique service name) pair the device
// announces and answers searches for.
type ssdpTarget struct {
	// nt is the notification type in an advertisement and the search target in
	// a search response: the same string, named twice by the spec.
	nt string
	// usn uniquely names this (device, target) pair.
	usn string
}

// ssdpTargets is everything this device announces. UPnP requires one
// advertisement per root device, per embedded device and per service, and a
// client that only knows how to look for one of them has to find us anyway.
func ssdpTargets(uuid string) []ssdpTarget {
	id := "uuid:" + uuid
	return []ssdpTarget{
		{nt: "upnp:rootdevice", usn: id + "::upnp:rootdevice"},
		{nt: id, usn: id},
		{nt: deviceType, usn: id + "::" + deviceType},
		{nt: contentDirectoryType, usn: id + "::" + contentDirectoryType},
		{nt: connectionManagerType, usn: id + "::" + connectionManagerType},
	}
}

// matchSearchTargets returns the targets that must answer a search for st.
// "ssdp:all" matches everything; anything else is an exact match, because
// version-tolerant matching would have us answer a MediaServer:2 search with a
// MediaServer:1 device.
func matchSearchTargets(st, uuid string) []ssdpTarget {
	all := ssdpTargets(uuid)
	if st == "ssdp:all" {
		return all
	}
	out := []ssdpTarget{}
	for _, t := range all {
		if t.nt == st {
			out = append(out, t)
		}
	}
	return out
}

// ssdpLocation is the device description URL a client fetches after discovery.
// It carries a literal IP rather than a hostname: the client has just received
// a datagram from us and may have no working name resolution for this host.
func ssdpLocation(ip string, port int) string {
	return fmt.Sprintf("http://%s%s/device.xml", joinHostPort(ip, port), MountPath)
}

func joinHostPort(ip string, port int) string {
	return ip + ":" + strconv.Itoa(port)
}

// notifyAlive is the "this device exists" advertisement, multicast to the group.
func notifyAlive(t ssdpTarget, location string) []byte {
	return ssdpMessage("NOTIFY * HTTP/1.1", [][2]string{
		{"HOST", ssdpAddr},
		{"CACHE-CONTROL", "max-age=" + strconv.Itoa(ssdpMaxAge)},
		{"LOCATION", location},
		{"NT", t.nt},
		{"NTS", "ssdp:alive"},
		{"SERVER", ssdpServer},
		{"USN", t.usn},
	})
}

// notifyByeBye is the "this device is going away" advertisement. It carries no
// LOCATION and no CACHE-CONTROL: there is nothing left to fetch and nothing
// left to cache.
func notifyByeBye(t ssdpTarget) []byte {
	return ssdpMessage("NOTIFY * HTTP/1.1", [][2]string{
		{"HOST", ssdpAddr},
		{"NT", t.nt},
		{"NTS", "ssdp:byebye"},
		{"USN", t.usn},
	})
}

// searchResponse answers an M-SEARCH. It is sent unicast to the searcher, and
// its ST echoes the specific target that matched, even when the search was
// "ssdp:all", which is why one search can produce several responses.
//
// EXT is an empty header, not a missing one: its presence is how a client knows
// the MAN extension it asked for was understood.
func searchResponse(t ssdpTarget, location string, date time.Time) []byte {
	return ssdpMessage("HTTP/1.1 200 OK", [][2]string{
		{"CACHE-CONTROL", "max-age=" + strconv.Itoa(ssdpMaxAge)},
		{"DATE", date.UTC().Format(time.RFC1123)},
		{"EXT", ""},
		{"LOCATION", location},
		{"SERVER", ssdpServer},
		{"ST", t.nt},
		{"USN", t.usn},
	})
}

// ssdpMessage assembles an HTTPU message: a start line, headers, and a blank
// line, all CRLF-terminated.
func ssdpMessage(start string, headers [][2]string) []byte {
	var b bytes.Buffer
	b.WriteString(start)
	b.WriteString("\r\n")
	for _, h := range headers {
		b.WriteString(h[0])
		b.WriteString(": ")
		b.WriteString(h[1])
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return b.Bytes()
}

// searchRequest is the part of an M-SEARCH this server acts on.
type searchRequest struct {
	// ST is the search target.
	ST string
	// MX is the maximum response delay in seconds, already clamped to the
	// spec's ceiling.
	MX int
}

// parseSearch reads an M-SEARCH datagram. It returns false for anything else on
// the group: other devices' advertisements, other clients' searches, and
// malformed packets all land on the same socket.
//
// It is hand-parsed rather than handed to http.ReadRequest because "M-SEARCH *"
// is not a valid HTTP request line: the "*" request URI is only legal for
// OPTIONS, and net/url rejects it.
func parseSearch(datagram []byte) (searchRequest, bool) {
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(datagram)))
	line, err := r.ReadLine()
	if err != nil || !strings.HasPrefix(strings.ToUpper(line), "M-SEARCH ") {
		return searchRequest{}, false
	}

	// A datagram whose header block is unterminated still yields the headers
	// that were read, and a search is short enough that those are all of them.
	header, _ := r.ReadMIMEHeader()
	if header == nil {
		return searchRequest{}, false
	}
	// MAN is quoted per the spec, but not every client remembers the quotes.
	if strings.Trim(header.Get("Man"), `"`) != "ssdp:discover" {
		return searchRequest{}, false
	}
	st := strings.TrimSpace(header.Get("St"))
	if st == "" {
		return searchRequest{}, false
	}

	mx, err := strconv.Atoi(strings.TrimSpace(header.Get("Mx")))
	if err != nil || mx < 0 {
		mx = 0
	}
	return searchRequest{ST: st, MX: min(mx, ssdpMaxMX)}, true
}
