package dlna

import (
	"strings"
	"testing"
	"time"
)

const testUUID = "1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed"

// headersOf splits an SSDP datagram into its start line and header map. It
// asserts the framing at the same time: every line CRLF-terminated, a blank
// line at the end. Clients parse these with strict HTTP header readers, so a
// missing CRLF is a device that never appears on the network.
func headersOf(t *testing.T, msg []byte) (string, map[string]string) {
	t.Helper()
	text := string(msg)
	if !strings.HasSuffix(text, "\r\n\r\n") {
		t.Fatalf("message is not terminated by a blank line: %q", text)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\r\n\r\n"), "\r\n")
	headers := map[string]string{}
	for _, line := range lines[1:] {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("malformed header line %q", line)
		}
		headers[key] = value
	}
	return lines[0], headers
}

func TestSSDPTargetsCoverTheWholeDevice(t *testing.T) {
	targets := ssdpTargets(testUUID)
	want := map[string]string{
		"upnp:rootdevice":     "uuid:" + testUUID + "::upnp:rootdevice",
		"uuid:" + testUUID:    "uuid:" + testUUID,
		deviceType:            "uuid:" + testUUID + "::" + deviceType,
		contentDirectoryType:  "uuid:" + testUUID + "::" + contentDirectoryType,
		connectionManagerType: "uuid:" + testUUID + "::" + connectionManagerType,
	}
	if len(targets) != len(want) {
		t.Fatalf("got %d targets, want %d", len(targets), len(want))
	}
	for _, target := range targets {
		usn, ok := want[target.nt]
		if !ok {
			t.Fatalf("unexpected NT %q", target.nt)
		}
		if target.usn != usn {
			t.Fatalf("NT %q: USN = %q, want %q", target.nt, target.usn, usn)
		}
	}
}

func TestNotifyAliveFormat(t *testing.T) {
	target := ssdpTargets(testUUID)[0]
	start, headers := headersOf(t, notifyAlive(target, "http://192.168.1.5:8677/dlna/device.xml"))

	if start != "NOTIFY * HTTP/1.1" {
		t.Fatalf("start line = %q", start)
	}
	want := map[string]string{
		"HOST":          "239.255.255.250:1900",
		"CACHE-CONTROL": "max-age=1800",
		"LOCATION":      "http://192.168.1.5:8677/dlna/device.xml",
		"NT":            "upnp:rootdevice",
		"NTS":           "ssdp:alive",
		"USN":           "uuid:" + testUUID + "::upnp:rootdevice",
	}
	for key, value := range want {
		if headers[key] != value {
			t.Fatalf("%s = %q, want %q", key, headers[key], value)
		}
	}
	if !strings.Contains(headers["SERVER"], "UPnP/1.0") {
		t.Fatalf("SERVER = %q, want a UPnP/1.0 triple", headers["SERVER"])
	}
}

// A byebye must not carry a LOCATION: there is nothing left to fetch, and
// clients that see one have been known to re-add the device they were told to
// drop.
func TestNotifyByeByeCarriesNoLocation(t *testing.T) {
	target := ssdpTargets(testUUID)[2]
	start, headers := headersOf(t, notifyByeBye(target))

	if start != "NOTIFY * HTTP/1.1" {
		t.Fatalf("start line = %q", start)
	}
	if headers["NTS"] != "ssdp:byebye" {
		t.Fatalf("NTS = %q", headers["NTS"])
	}
	if headers["NT"] != deviceType {
		t.Fatalf("NT = %q, want %q", headers["NT"], deviceType)
	}
	if _, ok := headers["LOCATION"]; ok {
		t.Fatalf("byebye carries LOCATION: %v", headers)
	}
	if _, ok := headers["CACHE-CONTROL"]; ok {
		t.Fatalf("byebye carries CACHE-CONTROL: %v", headers)
	}
}

func TestSearchResponseFormat(t *testing.T) {
	target := ssdpTargets(testUUID)[3]
	date := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	start, headers := headersOf(t, searchResponse(target, "http://10.0.0.2:8677/dlna/device.xml", date))

	if start != "HTTP/1.1 200 OK" {
		t.Fatalf("start line = %q", start)
	}
	// ST echoes the matched target, not the client's search string, so a
	// client that searched ssdp:all learns which service each answer is for.
	if headers["ST"] != contentDirectoryType {
		t.Fatalf("ST = %q, want %q", headers["ST"], contentDirectoryType)
	}
	if headers["USN"] != "uuid:"+testUUID+"::"+contentDirectoryType {
		t.Fatalf("USN = %q", headers["USN"])
	}
	if headers["LOCATION"] != "http://10.0.0.2:8677/dlna/device.xml" {
		t.Fatalf("LOCATION = %q", headers["LOCATION"])
	}
	// EXT must be present and empty: its presence is the acknowledgement.
	if value, ok := headers["EXT"]; !ok || value != "" {
		t.Fatalf("EXT = %q (present %v), want present and empty", value, ok)
	}
	if headers["DATE"] == "" {
		t.Fatal("DATE is missing")
	}
	if _, ok := headers["NTS"]; ok {
		t.Fatalf("search response carries NTS: %v", headers)
	}
}

func TestSSDPLocation(t *testing.T) {
	if got := ssdpLocation("192.168.1.5", 8677); got != "http://192.168.1.5:8677/dlna/device.xml" {
		t.Fatalf("ssdpLocation = %q", got)
	}
}

func TestParseSearch(t *testing.T) {
	tests := []struct {
		name     string
		datagram string
		want     searchRequest
		ok       bool
	}{
		{
			name:     "well formed",
			datagram: "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 3\r\nST: ssdp:all\r\n\r\n",
			want:     searchRequest{ST: "ssdp:all", MX: 3},
			ok:       true,
		},
		{
			// Not every client quotes MAN, and rejecting them would make the
			// server invisible to televisions that otherwise work.
			name:     "unquoted MAN",
			datagram: "M-SEARCH * HTTP/1.1\r\nMAN: ssdp:discover\r\nMX: 1\r\nST: upnp:rootdevice\r\n\r\n",
			want:     searchRequest{ST: "upnp:rootdevice", MX: 1},
			ok:       true,
		},
		{
			// The spec caps the honoured MX at 5 however large the ask.
			name:     "MX clamped",
			datagram: "M-SEARCH * HTTP/1.1\r\nMAN: \"ssdp:discover\"\r\nMX: 120\r\nST: ssdp:all\r\n\r\n",
			want:     searchRequest{ST: "ssdp:all", MX: 5},
			ok:       true,
		},
		{
			name:     "missing MX defaults to immediate",
			datagram: "M-SEARCH * HTTP/1.1\r\nMAN: \"ssdp:discover\"\r\nST: ssdp:all\r\n\r\n",
			want:     searchRequest{ST: "ssdp:all", MX: 0},
			ok:       true,
		},
		{
			// Another device's advertisement arrives on the same socket.
			name:     "notify is not a search",
			datagram: "NOTIFY * HTTP/1.1\r\nNTS: ssdp:alive\r\nNT: upnp:rootdevice\r\n\r\n",
		},
		{
			name:     "missing MAN",
			datagram: "M-SEARCH * HTTP/1.1\r\nMX: 3\r\nST: ssdp:all\r\n\r\n",
		},
		{
			name:     "missing ST",
			datagram: "M-SEARCH * HTTP/1.1\r\nMAN: \"ssdp:discover\"\r\nMX: 3\r\n\r\n",
		},
		{
			name:     "garbage",
			datagram: "\x00\x01\x02",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseSearch([]byte(tc.datagram))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMatchSearchTargets(t *testing.T) {
	tests := []struct {
		name string
		st   string
		want int
	}{
		{name: "ssdp:all matches everything", st: "ssdp:all", want: 5},
		{name: "root device", st: "upnp:rootdevice", want: 1},
		{name: "device uuid", st: "uuid:" + testUUID, want: 1},
		{name: "content directory service", st: contentDirectoryType, want: 1},
		{name: "another device's uuid", st: "uuid:00000000-0000-4000-8000-000000000000", want: 0},
		// Version-tolerant matching would have a MediaServer:1 answer a
		// MediaServer:2 search and then fail every action the client expects.
		{name: "a version we do not implement", st: "urn:schemas-upnp-org:device:MediaServer:2", want: 0},
		{name: "an unrelated device type", st: "urn:schemas-upnp-org:device:MediaRenderer:1", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchSearchTargets(tc.st, testUUID)
			if len(got) != tc.want {
				t.Fatalf("matched %d targets, want %d: %+v", len(got), tc.want, got)
			}
			for _, target := range got {
				if tc.st != "ssdp:all" && target.nt != tc.st {
					t.Fatalf("matched NT %q for ST %q", target.nt, tc.st)
				}
			}
		})
	}
}
