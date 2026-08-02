package dlna

import (
	"encoding/xml"
	"fmt"
	"path"
	"strings"
)

// UPnP object classes. Containers are storage folders rather than the
// media-specific container classes (albumContainer, and so on) because those
// carry semantics — an album has an artist — that a folder of videos does not.
//
// Items are the plain videoItem rather than videoItem.movie or
// videoItem.videoBroadcast: the subclasses add nothing a client needs to play
// the file, and every renderer understands the base class (PLAN phase 4 risk
// note: implement the spec, do not chase per-client behaviour).
const (
	classContainer = "object.container.storageFolder"
	classVideoItem = "object.item.videoItem"
)

// XML namespaces DIDL-Lite documents are written in.
const (
	didlNS = "urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"
	dcNS   = "http://purl.org/dc/elements/1.1/"
	upnpNS = "urn:schemas-upnp-org:metadata-1-0/upnp/"
)

// dlnaFlags is the fourth field of every protocolInfo this server emits.
//
// DLNA.ORG_OP=01 is the one that matters: it says the server honours byte-range
// requests, which is what lets a TV seek instead of only playing from the
// start. CI=0 says the stream is not transcoded, which is true by construction
// here. The FLAGS word is the conventional streaming set (DLNA v1.5,
// background and streaming transfer modes, connection stall allowed).
//
// There is deliberately no DLNA.ORG_PN. A PN is a claim that the file matches
// one exact profile — bitrate, level, the lot — and a wrong PN makes clients
// refuse a file they could otherwise play. Omitting it means "work it out from
// the stream", which is what a general-purpose library needs.
const dlnaFlags = "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"

// mimeByExt maps container extension to MIME type. The extension is all this
// server knows: probing every file to name its container would turn one browse
// into a disk read per item.
var mimeByExt = map[string]string{
	".mkv":  "video/x-matroska",
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".wmv":  "video/x-ms-wmv",
	".ts":   "video/mp2t",
	".m2ts": "video/mp2t",
	".mts":  "video/mp2t",
	".mpg":  "video/mpeg",
	".mpeg": "video/mpeg",
	".webm": "video/webm",
	".flv":  "video/x-flv",
	".ogv":  "video/ogg",
}

// defaultMIME is what an unrecognised extension is served as. It is a video
// type rather than application/octet-stream because a renderer handed an
// unknown binary type will not even try; handed a video type it will sniff the
// stream and usually succeed.
const defaultMIME = "video/mpeg"

// mimeForPath names the container of a library file.
func mimeForPath(p string) string {
	if mime, ok := mimeByExt[strings.ToLower(path.Ext(p))]; ok {
		return mime
	}
	return defaultMIME
}

// protocolInfo is the res@protocolInfo value: transport, network, MIME type,
// and the DLNA flags.
func protocolInfo(p string) string {
	return "http-get:*:" + mimeForPath(p) + ":" + dlnaFlags
}

// didlLite is a DIDL-Lite document: the payload of every Browse result.
//
// The namespace declarations are ordinary attributes rather than encoding/xml
// namespaces because Go's encoder rewrites prefixed names into its own
// generated prefixes, and DIDL-Lite consumers in the wild expect the
// conventional dc: and upnp: prefixes to appear verbatim.
type didlLite struct {
	XMLName    xml.Name        `xml:"DIDL-Lite"`
	XMLNS      string          `xml:"xmlns,attr"`
	DC         string          `xml:"xmlns:dc,attr"`
	UPnP       string          `xml:"xmlns:upnp,attr"`
	Containers []didlContainer `xml:"container"`
	Items      []didlItem      `xml:"item"`
}

// didlContainer is a browsable folder.
type didlContainer struct {
	ID       string `xml:"id,attr"`
	ParentID string `xml:"parentID,attr"`
	// Restricted says the client may not modify this object. Everything Caravan
	// exposes is read-only, so it is always 1.
	Restricted  int    `xml:"restricted,attr"`
	ChildCount  int    `xml:"childCount,attr"`
	Title       string `xml:"dc:title"`
	Class       string `xml:"upnp:class"`
	AlbumArtURI string `xml:"upnp:albumArtURI,omitempty"`
}

// didlItem is a playable object.
type didlItem struct {
	ID          string `xml:"id,attr"`
	ParentID    string `xml:"parentID,attr"`
	Restricted  int    `xml:"restricted,attr"`
	Title       string `xml:"dc:title"`
	Class       string `xml:"upnp:class"`
	AlbumArtURI string `xml:"upnp:albumArtURI,omitempty"`
	Res         didlRes
}

// didlRes is where the file actually is.
type didlRes struct {
	XMLName      xml.Name `xml:"res"`
	ProtocolInfo string   `xml:"protocolInfo,attr"`
	// Size is the file length in bytes; omitted when unknown, because a wrong
	// length is worse than an absent one for a client sizing its buffers.
	Size int64  `xml:"size,attr,omitempty"`
	URL  string `xml:",chardata"`
}

// newDIDL builds an empty document with the namespace declarations in place.
func newDIDL() *didlLite {
	return &didlLite{XMLNS: didlNS, DC: dcNS, UPnP: upnpNS}
}

// count is how many objects the document holds, which is Browse's
// NumberReturned.
func (d *didlLite) count() int { return len(d.Containers) + len(d.Items) }

// encode renders the document. The result is embedded, XML-escaped, inside the
// SOAP response's Result element — DIDL-Lite travels as a string, not as
// nested XML.
//
// Go's marshaller writes quotes and apostrophes as numeric character
// references (&#34;, &#39;). Legal XML — but the DIDL string is re-parsed by
// whatever unescaper a TV app ships, and the hand-rolled ones only know the
// five named entities. A title like "I'm Yani" must therefore travel as
// &apos;, or that client reads garbage and renders an empty folder.
var namedEntities = strings.NewReplacer("&#34;", "&quot;", "&#39;", "&apos;")

func (d *didlLite) encode() (string, error) {
	out, err := xml.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("dlna: encode didl-lite: %w", err)
	}
	return namedEntities.Replace(string(out)), nil
}

// slice applies Browse's StartingIndex and RequestedCount across the document,
// which the spec orders as containers first and then items.
//
// A RequestedCount of 0 means "everything from here", per the ContentDirectory
// specification — not "nothing".
func (d *didlLite) slice(start, count int) *didlLite {
	total := d.count()
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := total
	// count is whatever the client put in the SOAP body, so the remaining
	// distance is compared against rather than added to: start+count overflows
	// on a RequestedCount of math.MaxInt64, wraps negative, and slices the
	// document with a negative bound.
	if count > 0 && count < total-start {
		end = start + count
	}

	out := newDIDL()
	nContainers := len(d.Containers)
	out.Containers = d.Containers[min(start, nContainers):min(end, nContainers)]
	out.Items = d.Items[max(start-nContainers, 0):max(end-nContainers, 0)]
	return out
}
