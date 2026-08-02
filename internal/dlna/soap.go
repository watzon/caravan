package dlna

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

// ContentDirectory error codes used here (UPnP AV ContentDirectory:1, §2.5).
const (
	// errNoSuchObject is the answer to a browse of an object id that no longer
	// resolves — a client's cached id after a rescan, typically.
	errNoSuchObject = "701"
	// errUnsupportedSearch is what Search returns. Search is optional in
	// ContentDirectory:1, and a server that advertises no search capabilities
	// and then rejects search is consistent; one that fakes it is not.
	errUnsupportedSearch = "720"
	// errInvalidArgs is the generic SOAP argument fault.
	errInvalidArgs = "402"
	// errActionFailed is the catch-all for a failure on our side.
	errActionFailed = "501"
)

// soapEnvelopeNS and soapEncoding are fixed by SOAP 1.1, which UPnP 1.0 pins.
const (
	soapEnvelopeNS = "http://schemas.xmlsoap.org/soap/envelope/"
	soapEncoding   = "http://schemas.xmlsoap.org/soap/encoding/"
	upnpControlNS  = "urn:schemas-upnp-org:control-1-0"
)

// contentTypeSOAP is what both requests and responses carry.
const contentTypeSOAP = `text/xml; charset="utf-8"`

// parseSOAPAction splits a SOAPACTION header — `"urn:…:ContentDirectory:1#Browse"`
// — into the service type and the action name.
//
// The header is the authority on which action was invoked, not the body's
// element name: it is the one part of the request a client cannot get subtly
// wrong through namespace handling, and it is what the spec says to dispatch on.
func parseSOAPAction(header string) (service, action string, ok bool) {
	raw := strings.Trim(strings.TrimSpace(header), `"`)
	service, action, ok = strings.Cut(raw, "#")
	if !ok || service == "" || action == "" {
		return "", "", false
	}
	return service, action, true
}

// browseArgs is the Browse action's input.
type browseArgs struct {
	ObjectID       string
	BrowseFlag     string
	StartingIndex  int
	RequestedCount int
}

// Browse flags, per ContentDirectory:1.
const (
	browseMetadata       = "BrowseMetadata"
	browseDirectChildren = "BrowseDirectChildren"
)

// decodeBrowse reads a Browse request body.
//
// The element paths are matched on local names only, so the request parses
// whichever namespace prefix the client happens to use for the service — which
// varies, and is not something a server gets to be strict about.
func decodeBrowse(body []byte) (browseArgs, error) {
	var envelope struct {
		XMLName xml.Name `xml:"Envelope"`
		Browse  struct {
			ObjectID       string `xml:"ObjectID"`
			BrowseFlag     string `xml:"BrowseFlag"`
			StartingIndex  int    `xml:"StartingIndex"`
			RequestedCount int    `xml:"RequestedCount"`
		} `xml:"Body>Browse"`
	}
	if err := xml.Unmarshal(body, &envelope); err != nil {
		return browseArgs{}, fmt.Errorf("dlna: decode browse: %w", err)
	}

	args := browseArgs{
		ObjectID:       strings.TrimSpace(envelope.Browse.ObjectID),
		BrowseFlag:     strings.TrimSpace(envelope.Browse.BrowseFlag),
		StartingIndex:  envelope.Browse.StartingIndex,
		RequestedCount: envelope.Browse.RequestedCount,
	}
	if args.ObjectID == "" {
		// Some clients omit ObjectID on their first call and mean the root.
		args.ObjectID = rootID
	}
	if args.BrowseFlag == "" {
		args.BrowseFlag = browseDirectChildren
	}
	if args.BrowseFlag != browseMetadata && args.BrowseFlag != browseDirectChildren {
		return browseArgs{}, fmt.Errorf("dlna: unknown BrowseFlag %q", args.BrowseFlag)
	}
	return args, nil
}

// soapArg is one named output value of an action response, in the order the
// service description declares it. Order is part of the contract: SOAP
// responses are sequences, not maps.
type soapArg struct {
	Name  string
	Value string
}

// writeSOAPResponse writes a successful action response.
func writeSOAPResponse(w http.ResponseWriter, service, action string, args []soapArg) {
	var b strings.Builder
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<s:Envelope xmlns:s="%s" s:encodingStyle="%s"><s:Body>`, soapEnvelopeNS, soapEncoding)
	fmt.Fprintf(&b, `<u:%sResponse xmlns:u="%s">`, action, service)
	for _, a := range args {
		fmt.Fprintf(&b, "<%s>", a.Name)
		_ = xml.EscapeText(&b, []byte(a.Value))
		fmt.Fprintf(&b, "</%s>", a.Name)
	}
	fmt.Fprintf(&b, "</u:%sResponse></s:Body></s:Envelope>", action)

	w.Header().Set("Content-Type", contentTypeSOAP)
	w.Header().Set("EXT", "")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// writeSOAPFault writes a UPnP error. The HTTP status is 500 whatever the UPnP
// code is: that is what the specification requires, and clients key off the
// errorCode in the body rather than the status line.
func writeSOAPFault(w http.ResponseWriter, code, description string) {
	var b strings.Builder
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, `<s:Envelope xmlns:s="%s" s:encodingStyle="%s"><s:Body><s:Fault>`,
		soapEnvelopeNS, soapEncoding)
	b.WriteString(`<faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring>`)
	fmt.Fprintf(&b, `<detail><UPnPError xmlns="%s"><errorCode>%s</errorCode><errorDescription>`,
		upnpControlNS, code)
	_ = xml.EscapeText(&b, []byte(description))
	b.WriteString(`</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)

	w.Header().Set("Content-Type", contentTypeSOAP)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(b.String()))
}
