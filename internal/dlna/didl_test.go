package dlna

import (
	"encoding/xml"
	"math"
	"strings"
	"testing"
)

func TestDIDLLiteStructure(t *testing.T) {
	d := newDIDL()
	d.Containers = []didlContainer{container("movies", "0", "Movies", 3, "")}
	d.Items = []didlItem{{
		ID: "m:7", ParentID: "movies", Restricted: 1,
		Title: "Big Buck Bunny (2008)", Class: classVideoItem,
		AlbumArtURI: "http://host/api/v1/images/Movies/poster.jpg",
		Res: didlRes{
			ProtocolInfo: protocolInfo("Movies/bbb.mkv"),
			Size:         1234,
			URL:          "http://host/dlna/media/7.mkv",
		},
	}}

	out, err := d.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// The three namespace declarations have to survive verbatim: consumers key
	// off the conventional dc: and upnp: prefixes.
	for _, want := range []string{
		`xmlns="` + didlNS + `"`,
		`xmlns:dc="` + dcNS + `"`,
		`xmlns:upnp="` + upnpNS + `"`,
		`<dc:title>Movies</dc:title>`,
		`<upnp:class>` + classContainer + `</upnp:class>`,
		`<dc:title>Big Buck Bunny (2008)</dc:title>`,
		`<upnp:class>` + classVideoItem + `</upnp:class>`,
		`<upnp:albumArtURI>http://host/api/v1/images/Movies/poster.jpg</upnp:albumArtURI>`,
		`id="m:7"`,
		`parentID="movies"`,
		`restricted="1"`,
		`childCount="3"`,
		`protocolInfo="http-get:*:video/x-matroska:` + dlnaFlags + `"`,
		`size="1234"`,
		`>http://host/dlna/media/7.mkv</res>`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("encoded DIDL is missing %s\n%s", want, out)
		}
	}

	// It has to be parseable XML, not just the right-looking string.
	var probe struct {
		XMLName    xml.Name `xml:"DIDL-Lite"`
		Containers []struct {
			ID    string `xml:"id,attr"`
			Title string `xml:"title"`
		} `xml:"container"`
		Items []struct {
			ID  string `xml:"id,attr"`
			Res string `xml:"res"`
		} `xml:"item"`
	}
	if err := xml.Unmarshal([]byte(out), &probe); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(probe.Containers) != 1 || probe.Containers[0].ID != "movies" {
		t.Fatalf("containers = %+v", probe.Containers)
	}
	if len(probe.Items) != 1 || probe.Items[0].Res != "http://host/dlna/media/7.mkv" {
		t.Fatalf("items = %+v", probe.Items)
	}
}

// An empty albumArtURI must be absent rather than empty: a client handed an
// empty art URL fetches it, gets a 404, and shows a broken-image placeholder
// where it would otherwise have shown nothing.
func TestDIDLOmitsEmptyAlbumArt(t *testing.T) {
	d := newDIDL()
	d.Containers = []didlContainer{container("tv", "0", "TV", 0, "")}
	out, err := d.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(out, "albumArtURI") {
		t.Fatalf("empty album art was emitted: %s", out)
	}
}

// A zero size must be absent too: a client told the file is 0 bytes will not
// bother requesting it.
func TestDIDLOmitsUnknownSize(t *testing.T) {
	d := newDIDL()
	d.Items = []didlItem{{ID: "m:1", ParentID: "movies", Res: didlRes{ProtocolInfo: "http-get:*:video/mp4:*"}}}
	out, err := d.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(out, "size=") {
		t.Fatalf("zero size was emitted: %s", out)
	}
}

func TestProtocolInfoByContainer(t *testing.T) {
	tests := map[string]string{
		"TV/show.mkv":       "http-get:*:video/x-matroska:" + dlnaFlags,
		"Movies/film.MP4":   "http-get:*:video/mp4:" + dlnaFlags,
		"Movies/film.avi":   "http-get:*:video/x-msvideo:" + dlnaFlags,
		"Movies/film.m2ts":  "http-get:*:video/mp2t:" + dlnaFlags,
		"Movies/film.weird": "http-get:*:" + defaultMIME + ":" + dlnaFlags,
	}
	for path, want := range tests {
		if got := protocolInfo(path); got != want {
			t.Fatalf("protocolInfo(%q) = %q, want %q", path, got, want)
		}
	}
}

// DLNA.ORG_OP=01 is what tells a renderer it may seek. Without it a TV plays
// from the start and the scrub bar does nothing, which is the single most
// visible way this feature fails.
func TestProtocolInfoAdvertisesByteSeek(t *testing.T) {
	if !strings.Contains(protocolInfo("a.mkv"), "DLNA.ORG_OP=01") {
		t.Fatalf("protocolInfo does not advertise byte-range operation: %s", protocolInfo("a.mkv"))
	}
	if strings.Contains(protocolInfo("a.mkv"), "DLNA.ORG_PN") {
		t.Fatal("protocolInfo claims a profile name it cannot verify")
	}
}

func TestDIDLSlicePagesContainersThenItems(t *testing.T) {
	d := newDIDL()
	d.Containers = []didlContainer{
		container("a", "0", "A", 0, ""),
		container("b", "0", "B", 0, ""),
	}
	d.Items = []didlItem{
		{ID: "i1"}, {ID: "i2"}, {ID: "i3"},
	}

	tests := []struct {
		name           string
		start, count   int
		wantContainers []string
		wantItems      []string
	}{
		{name: "whole set", start: 0, count: 0, wantContainers: []string{"a", "b"}, wantItems: []string{"i1", "i2", "i3"}},
		{name: "first page", start: 0, count: 2, wantContainers: []string{"a", "b"}},
		{name: "straddles the boundary", start: 1, count: 2, wantContainers: []string{"b"}, wantItems: []string{"i1"}},
		{name: "items only", start: 2, count: 2, wantItems: []string{"i1", "i2"}},
		{name: "past the end", start: 99, count: 5},
		{name: "count past the end", start: 3, count: 99, wantItems: []string{"i2", "i3"}},
		// RequestedCount comes straight off the wire from an unauthenticated LAN
		// client. start+count overflowed on this, wrapped negative, and panicked
		// the Browse handler on a negative slice bound.
		{
			name: "count is the largest int a client can send", start: 1, count: math.MaxInt64,
			wantContainers: []string{"b"}, wantItems: []string{"i1", "i2", "i3"},
		},
		{
			name: "count is the largest int and the start is past the containers", start: 4, count: math.MaxInt64,
			wantItems: []string{"i3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page := d.slice(tc.start, tc.count)
			gotContainers := []string{}
			for _, c := range page.Containers {
				gotContainers = append(gotContainers, c.ID)
			}
			gotItems := []string{}
			for _, i := range page.Items {
				gotItems = append(gotItems, i.ID)
			}
			if strings.Join(gotContainers, ",") != strings.Join(tc.wantContainers, ",") {
				t.Fatalf("containers = %v, want %v", gotContainers, tc.wantContainers)
			}
			if strings.Join(gotItems, ",") != strings.Join(tc.wantItems, ",") {
				t.Fatalf("items = %v, want %v", gotItems, tc.wantItems)
			}
		})
	}
}
