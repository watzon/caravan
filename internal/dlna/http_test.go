package dlna

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// soapPost issues a control request the way a renderer does: a SOAPACTION
// header plus a SOAP envelope.
func soapPost(t *testing.T, h http.Handler, path, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "caravan.lan:8677"
	req.Header.Set("Content-Type", contentTypeSOAP)
	req.Header.Set("SOAPACTION", `"`+action+`"`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func browseBody(objectID, flag string, start, count int) string {
	return `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body><u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
<ObjectID>` + objectID + `</ObjectID>
<BrowseFlag>` + flag + `</BrowseFlag>
<Filter>*</Filter>
<StartingIndex>` + itoa(start) + `</StartingIndex>
<RequestedCount>` + itoa(count) + `</RequestedCount>
<SortCriteria></SortCriteria>
</u:Browse></s:Body></s:Envelope>`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for ; n > 0; n /= 10 {
		digits = string(rune('0'+n%10)) + digits
	}
	return digits
}

// browseResponse is the shape a client decodes. Result arrives as an escaped
// string, so the DIDL inside is re-parsed separately.
type browseResponse struct {
	Result         string `xml:"Body>BrowseResponse>Result"`
	NumberReturned int    `xml:"Body>BrowseResponse>NumberReturned"`
	TotalMatches   int    `xml:"Body>BrowseResponse>TotalMatches"`
	UpdateID       string `xml:"Body>BrowseResponse>UpdateID"`
}

// didlProbe re-reads a DIDL document the way a client does: by local element
// name, ignoring prefixes.
//
// It cannot reuse didlLite. That type spells its elements "dc:title" and
// "upnp:class" so the marshaller emits those prefixes verbatim, which is what
// consumers expect on the wire — but encoding/xml's decoder matches tags
// against local names, so it would never fill those fields back in. Decoding
// through a separate shape is therefore the only honest round trip.
type didlProbe struct {
	XMLName    xml.Name `xml:"DIDL-Lite"`
	Containers []struct {
		ID         string `xml:"id,attr"`
		ParentID   string `xml:"parentID,attr"`
		Restricted int    `xml:"restricted,attr"`
		ChildCount int    `xml:"childCount,attr"`
		Title      string `xml:"title"`
		Class      string `xml:"class"`
		AlbumArt   string `xml:"albumArtURI"`
	} `xml:"container"`
	Items []struct {
		ID       string `xml:"id,attr"`
		ParentID string `xml:"parentID,attr"`
		Title    string `xml:"title"`
		Class    string `xml:"class"`
		AlbumArt string `xml:"albumArtURI"`
		Res      struct {
			ProtocolInfo string `xml:"protocolInfo,attr"`
			Size         int64  `xml:"size,attr"`
			URL          string `xml:",chardata"`
		} `xml:"res"`
	} `xml:"item"`
}

func decodeBrowseResponse(t *testing.T, rec *httptest.ResponseRecorder) (browseResponse, *didlProbe) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out browseResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode SOAP response: %v\n%s", err, rec.Body.String())
	}
	var didl didlProbe
	if err := xml.Unmarshal([]byte(out.Result), &didl); err != nil {
		t.Fatalf("decode DIDL result: %v\n%s", err, out.Result)
	}
	return out, &didl
}

// faultOf reads the UPnP error code out of a SOAP fault.
func faultOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("fault status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	var fault struct {
		Code string `xml:"Body>Fault>detail>UPnPError>errorCode"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &fault); err != nil {
		t.Fatalf("decode fault: %v\n%s", err, rec.Body.String())
	}
	return fault.Code
}

func TestDeviceDescription(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	h := svc.Handler()

	req := httptest.NewRequest(http.MethodGet, MountPath+"/device.xml", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/xml") {
		t.Fatalf("Content-Type = %q", ct)
	}

	var device struct {
		DeviceType   string `xml:"device>deviceType"`
		FriendlyName string `xml:"device>friendlyName"`
		UDN          string `xml:"device>UDN"`
		DLNADoc      string `xml:"device>X_DLNADOC"`
		Services     []struct {
			Type       string `xml:"serviceType"`
			ID         string `xml:"serviceId"`
			SCPDURL    string `xml:"SCPDURL"`
			ControlURL string `xml:"controlURL"`
		} `xml:"device>serviceList>service"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &device); err != nil {
		t.Fatalf("parse device description: %v\n%s", err, rec.Body.String())
	}
	if device.DeviceType != deviceType {
		t.Fatalf("deviceType = %q", device.DeviceType)
	}
	if device.FriendlyName != DefaultFriendlyName {
		t.Fatalf("friendlyName = %q", device.FriendlyName)
	}
	// The UDN has to be the same identity SSDP advertises, or the client
	// discovers one device and describes another.
	cfg, err := svc.Config(t.Context())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if device.UDN != "uuid:"+cfg.UUID {
		t.Fatalf("UDN = %q, want uuid:%s", device.UDN, cfg.UUID)
	}
	// X_DLNADOC is what marks this as a DMS; several televisions filter on it.
	if device.DLNADoc != "DMS-1.50" {
		t.Fatalf("X_DLNADOC = %q", device.DLNADoc)
	}
	if len(device.Services) != 2 {
		t.Fatalf("services = %+v", device.Services)
	}
	for _, s := range device.Services {
		// Every advertised control URL has to be a route that answers, or a
		// renderer that probes the service list gives up on the device.
		probe := httptest.NewRequest(http.MethodPost, s.ControlURL, strings.NewReader(""))
		probe.Header.Set("SOAPACTION", `"`+s.Type+`#GetProtocolInfo"`)
		probeRec := httptest.NewRecorder()
		h.ServeHTTP(probeRec, probe)
		if probeRec.Code == http.StatusNotFound {
			t.Fatalf("control URL %q is not routed", s.ControlURL)
		}

		scpd := httptest.NewRequest(http.MethodGet, s.SCPDURL, nil)
		scpdRec := httptest.NewRecorder()
		h.ServeHTTP(scpdRec, scpd)
		if scpdRec.Code != http.StatusOK {
			t.Fatalf("SCPD %q status = %d", s.SCPDURL, scpdRec.Code)
		}
		var probeSCPD struct {
			Actions []string `xml:"actionList>action>name"`
		}
		if err := xml.Unmarshal(scpdRec.Body.Bytes(), &probeSCPD); err != nil {
			t.Fatalf("parse SCPD %q: %v", s.SCPDURL, err)
		}
		if len(probeSCPD.Actions) == 0 {
			t.Fatalf("SCPD %q declares no actions", s.SCPDURL)
		}
	}
}

// The friendly name is read per request, so renaming the device takes effect
// without restarting the server.
func TestDeviceDescriptionFollowsFriendlyName(t *testing.T) {
	svc, st, _ := newTestService(t)
	if err := st.SetSetting(t.Context(), "dlna_friendly_name", "Living Room"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, MountPath+"/device.xml", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "<friendlyName>Living Room</friendlyName>") {
		t.Fatalf("device description does not carry the configured name:\n%s", rec.Body.String())
	}
}

func TestBrowseOverSOAP(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	h := svc.Handler()

	rec := soapPost(t, h, MountPath+"/control/cds", contentDirectoryType+"#Browse",
		browseBody(rootID, browseDirectChildren, 0, 0))
	resp, didl := decodeBrowseResponse(t, rec)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/xml") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if resp.NumberReturned != 2 || resp.TotalMatches != 2 {
		t.Fatalf("returned %d of %d, want 2 of 2", resp.NumberReturned, resp.TotalMatches)
	}
	if len(didl.Containers) != 2 {
		t.Fatalf("containers = %+v", didl.Containers)
	}
	if didl.Containers[0].Title != "Movies" || didl.Containers[1].Title != "TV" {
		t.Fatalf("root children = %+v", didl.Containers)
	}
	// The prefixed elements have to survive the escape-into-Result round trip,
	// because that string is the only thing the client ever parses.
	if didl.Containers[0].Class != classContainer {
		t.Fatalf("upnp:class did not survive the round trip: %q", didl.Containers[0].Class)
	}

	// The res URL has to be built on the Host the client used, not on anything
	// captured at startup — that is what makes the same server work over the
	// LAN IP, a hostname and a reverse proxy.
	rec = soapPost(t, h, MountPath+"/control/cds", contentDirectoryType+"#Browse",
		browseBody(moviesID, browseDirectChildren, 0, 0))
	_, didl = decodeBrowseResponse(t, rec)
	if len(didl.Items) != 1 {
		t.Fatalf("movies = %+v", didl.Items)
	}
	if !strings.HasPrefix(didl.Items[0].Res.URL, "http://caravan.lan:8677/dlna/media/") {
		t.Fatalf("res URL = %q, want it built on the request host", didl.Items[0].Res.URL)
	}
	if didl.Items[0].Class != classVideoItem {
		t.Fatalf("item class = %q, want %q", didl.Items[0].Class, classVideoItem)
	}
	if didl.Items[0].Res.Size == 0 || didl.Items[0].Res.ProtocolInfo == "" {
		t.Fatalf("res lost its attributes: %+v", didl.Items[0].Res)
	}
}

// Paging: NumberReturned is the window, TotalMatches is the whole set, and a
// client walks the first until it has the second.
func TestBrowsePaging(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	h := svc.Handler()

	rec := soapPost(t, h, MountPath+"/control/cds", contentDirectoryType+"#Browse",
		browseBody("s:1:1", browseDirectChildren, 1, 1))
	resp, didl := decodeBrowseResponse(t, rec)

	if resp.TotalMatches != 3 {
		t.Fatalf("TotalMatches = %d, want 3", resp.TotalMatches)
	}
	if resp.NumberReturned != 1 {
		t.Fatalf("NumberReturned = %d, want 1", resp.NumberReturned)
	}
	if len(didl.Items) != 1 || didl.Items[0].Title != "S01E02 - Mountains" {
		t.Fatalf("page = %+v, want the second episode", didl.Items)
	}
}

func TestBrowseMetadataOverSOAP(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	rec := soapPost(t, svc.Handler(), MountPath+"/control/cds", contentDirectoryType+"#Browse",
		browseBody(rootID, browseMetadata, 0, 0))
	resp, didl := decodeBrowseResponse(t, rec)

	if resp.NumberReturned != 1 || resp.TotalMatches != 1 {
		t.Fatalf("returned %d of %d, want 1 of 1", resp.NumberReturned, resp.TotalMatches)
	}
	if len(didl.Containers) != 1 || didl.Containers[0].ID != rootID {
		t.Fatalf("metadata = %+v", didl.Containers)
	}
	if didl.Containers[0].ParentID != rootParentID {
		t.Fatalf("root parentID = %q, want %q", didl.Containers[0].ParentID, rootParentID)
	}
}

func TestBrowseUnknownObjectIsSOAPError701(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	rec := soapPost(t, svc.Handler(), MountPath+"/control/cds", contentDirectoryType+"#Browse",
		browseBody("m:4242", browseMetadata, 0, 0))
	if code := faultOf(t, rec); code != errNoSuchObject {
		t.Fatalf("errorCode = %q, want %q", code, errNoSuchObject)
	}
}

// Search is optional in ContentDirectory:1 and this server does not implement
// it. 720 is the "cannot process" fault the spec reserves for exactly that,
// and it pairs with the empty SearchCapabilities below.
func TestSearchIsNotImplemented(t *testing.T) {
	svc, _, _ := newTestService(t)

	rec := soapPost(t, svc.Handler(), MountPath+"/control/cds", contentDirectoryType+"#Search", "")
	if code := faultOf(t, rec); code != errUnsupportedSearch {
		t.Fatalf("errorCode = %q, want %q", code, errUnsupportedSearch)
	}
}

func TestContentDirectoryStubs(t *testing.T) {
	svc, _, _ := newTestService(t)
	h := svc.Handler()

	tests := []struct {
		action string
		field  string
		want   string
	}{
		{action: "GetSearchCapabilities", field: "SearchCaps", want: ""},
		{action: "GetSortCapabilities", field: "SortCaps", want: ""},
		{action: "GetSystemUpdateID", field: "Id", want: systemUpdateID},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			rec := soapPost(t, h, MountPath+"/control/cds", contentDirectoryType+"#"+tc.action, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, "<u:"+tc.action+"Response") {
				t.Fatalf("response is not %sResponse:\n%s", tc.action, body)
			}
			if !strings.Contains(body, "<"+tc.field+">"+tc.want+"</"+tc.field+">") {
				t.Fatalf("%s missing from:\n%s", tc.field, body)
			}
		})
	}
}

func TestConnectionManagerGetProtocolInfo(t *testing.T) {
	svc, _, _ := newTestService(t)

	rec := soapPost(t, svc.Handler(), MountPath+"/control/cms",
		connectionManagerType+"#GetProtocolInfo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Source string `xml:"Body>GetProtocolInfoResponse>Source"`
		Sink   string `xml:"Body>GetProtocolInfoResponse>Sink"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	if !strings.Contains(out.Source, "http-get:*:video/x-matroska:") {
		t.Fatalf("Source does not advertise matroska: %q", out.Source)
	}
	// A server never sinks a stream.
	if out.Sink != "" {
		t.Fatalf("Sink = %q, want empty", out.Sink)
	}
	// Stable between calls, so a client that caches it never sees it churn.
	if sourceProtocolInfo() != sourceProtocolInfo() {
		t.Fatal("GetProtocolInfo is not deterministic")
	}
}

func TestUnknownSOAPActionFaults(t *testing.T) {
	svc, _, _ := newTestService(t)
	h := svc.Handler()

	for _, tc := range []struct{ name, path, action string }{
		{name: "unknown action", path: MountPath + "/control/cds", action: contentDirectoryType + "#Frobnicate"},
		{name: "wrong service", path: MountPath + "/control/cds", action: connectionManagerType + "#Browse"},
		{name: "unparseable header", path: MountPath + "/control/cds", action: "nonsense"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := soapPost(t, h, tc.path, tc.action, "")
			if code := faultOf(t, rec); code != errInvalidArgs {
				t.Fatalf("errorCode = %q, want %q", code, errInvalidArgs)
			}
		})
	}
}

// writeMedia puts a file on disk under the storage root and returns its
// storage-root-relative path.
func writeMedia(t *testing.T, root, rel string, body []byte) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestServeMediaWithRange(t *testing.T) {
	svc, st, root := newTestService(t)
	body := []byte("0123456789abcdef")
	writeMedia(t, root, "Movies/film.mkv", body)
	file := &core.MediaFile{Path: "Movies/film.mkv", Size: int64(len(body))}
	if err := st.UpsertMediaFile(t.Context(), file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	h := svc.Handler()

	// Whole file: the container's own MIME type wins over net/http's table,
	// which does not know .mkv.
	req := httptest.NewRequest(http.MethodGet, MountPath+"/media/1.mkv", nil)
	req.Header.Set("getcontentFeatures.dlna.org", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != string(body) {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "video/x-matroska" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
	if got := rec.Header().Get("transferMode.dlna.org"); got != "Streaming" {
		t.Fatalf("transferMode.dlna.org = %q", got)
	}
	// Only answered when asked, which is what the DLNA guidelines require.
	if got := rec.Header().Get("contentFeatures.dlna.org"); got != dlnaFlags {
		t.Fatalf("contentFeatures.dlna.org = %q", got)
	}

	// A seek: this is what a TV's scrub bar turns into, and it must come back
	// 206 with exactly the requested bytes.
	req = httptest.NewRequest(http.MethodGet, MountPath+"/media/1.mkv", nil)
	req.Header.Set("Range", "bytes=4-7")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", rec.Code)
	}
	if rec.Body.String() != "4567" {
		t.Fatalf("range body = %q, want %q", rec.Body.String(), "4567")
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 4-7/16" {
		t.Fatalf("Content-Range = %q", got)
	}

	// An open-ended range: "from here to the end", which is what a client
	// issues when it resumes.
	req = httptest.NewRequest(http.MethodGet, MountPath+"/media/1.mkv", nil)
	req.Header.Set("Range", "bytes=10-")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "abcdef" {
		t.Fatalf("open range: status %d body %q", rec.Code, rec.Body.String())
	}

	// HEAD is how several renderers size a file before playing it. Go's mux
	// routes it through the GET pattern.
	req = httptest.NewRequest(http.MethodHead, MountPath+"/media/1.mkv", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Length"); got != "16" {
		t.Fatalf("HEAD Content-Length = %q", got)
	}
}

// The features header is only sent when the client asks for it; sending it
// unasked confuses older renderers.
func TestServeMediaOmitsContentFeaturesUnlessAsked(t *testing.T) {
	svc, st, root := newTestService(t)
	writeMedia(t, root, "Movies/film.mp4", []byte("x"))
	if err := st.UpsertMediaFile(t.Context(), &core.MediaFile{Path: "Movies/film.mp4", Size: 1}); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, MountPath+"/media/1.mp4", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("contentFeatures.dlna.org"); got != "" {
		t.Fatalf("contentFeatures.dlna.org = %q, want absent", got)
	}
}

func TestServeMediaRejectsUnknownAndUnsafeIDs(t *testing.T) {
	svc, st, root := newTestService(t)
	writeMedia(t, root, "Movies/film.mkv", []byte("x"))
	if err := st.UpsertMediaFile(t.Context(), &core.MediaFile{Path: "Movies/film.mkv", Size: 1}); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	// A row whose file is not on disk: the database is a cache, so this is a
	// normal state, not a server error.
	if err := st.UpsertMediaFile(t.Context(), &core.MediaFile{Path: "Movies/gone.mkv", Size: 1}); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	h := svc.Handler()

	for _, name := range []string{"9999.mkv", "nope.mkv", "0.mkv", "-1.mkv", "2.mkv"} {
		req := httptest.NewRequest(http.MethodGet, MountPath+"/media/"+name, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("media/%s status = %d, want 404", name, rec.Code)
		}
	}
}

// The media endpoint is not a general reader of the host filesystem: a row
// whose path escapes the storage root fails at os.Root rather than serving.
func TestServeMediaIsConfinedToStorageRoot(t *testing.T) {
	svc, st, root := newTestService(t)
	outside := filepath.Join(filepath.Dir(root), "secret.mkv")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := st.UpsertMediaFile(t.Context(), &core.MediaFile{Path: "../secret.mkv", Size: 6}); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, MountPath+"/media/1.mkv", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("a path outside the storage root was served")
	}
}

func TestDecodeBrowseDefaults(t *testing.T) {
	// A client that omits ObjectID and BrowseFlag means "the root, children",
	// which is what its first call always is.
	args, err := decodeBrowse([]byte(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
		<s:Body><u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
		</u:Browse></s:Body></s:Envelope>`))
	if err != nil {
		t.Fatalf("decodeBrowse: %v", err)
	}
	if args.ObjectID != rootID || args.BrowseFlag != browseDirectChildren {
		t.Fatalf("defaults = %+v", args)
	}

	if _, err := decodeBrowse([]byte(browseBody("0", "BrowseSideways", 0, 0))); err == nil {
		t.Fatal("an unknown BrowseFlag was accepted")
	}
	if _, err := decodeBrowse([]byte("not xml at all <<<")); err == nil {
		t.Fatal("malformed XML was accepted")
	}
}

func TestParseSOAPAction(t *testing.T) {
	tests := []struct {
		header          string
		service, action string
		ok              bool
	}{
		{header: `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`,
			service: contentDirectoryType, action: "Browse", ok: true},
		// Not every client quotes the header.
		{header: `urn:schemas-upnp-org:service:ContentDirectory:1#Browse`,
			service: contentDirectoryType, action: "Browse", ok: true},
		{header: ``},
		{header: `"urn:schemas-upnp-org:service:ContentDirectory:1"`},
		{header: `"#Browse"`},
	}
	for _, tc := range tests {
		service, action, ok := parseSOAPAction(tc.header)
		if ok != tc.ok {
			t.Fatalf("parseSOAPAction(%q) ok = %v, want %v", tc.header, ok, tc.ok)
		}
		if ok && (service != tc.service || action != tc.action) {
			t.Fatalf("parseSOAPAction(%q) = %q, %q", tc.header, service, action)
		}
	}
}
