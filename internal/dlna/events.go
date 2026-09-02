package dlna

// GENA eventing (UPnP Device Architecture, chapter 4): the minimum a
// spec-correct MediaServer owes. ContentDirectory's SystemUpdateID and
// ConnectionManager's connection ids are *evented* state variables, and several
// client stacks SUBSCRIBE before they browse and treat a failed subscription as
// a dead service: the symptom of an empty eventSubURL is a device that is
// recognized on the network but whose library shows empty.
//
// Caravan's evented state moves rarely, SystemUpdateID advances only when a
// library is added to or removed from the advertised tree, so eventing here is
// exactly: accept the subscription, deliver the initial NOTIFY the spec
// requires with the current state, honor renewals and expiry, and send nothing
// further. A client that wants the newer value polls GetSystemUpdateID, which
// is what the ones that care about freshness already do.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event endpoints, one per service, referenced from the device description.
const (
	contentDirectoryEventURL  = MountPath + "/events/cds"
	connectionManagerEventURL = MountPath + "/events/cms"
)

const (
	// defaultSubTimeout is the lifetime granted when the client asks for
	// nothing usable. 1800s is the value the spec's examples use and what the
	// common servers grant.
	defaultSubTimeout = 1800 * time.Second
	// maxSubTimeout bounds what a client may ask for, so a subscription
	// cannot be parked for a week.
	maxSubTimeout = 24 * time.Hour
	// maxSubscribers bounds the registry. A LAN has a handful of renderers;
	// hitting this means something is leaking subscriptions, and refusing is
	// better than growing without bound.
	maxSubscribers = 64
	// notifyTimeout is how long one initial-event delivery may take. The
	// callback is on the LAN; a client that cannot take the event in this
	// time is not waiting for it.
	notifyTimeout = 10 * time.Second
)

// subscription is one GENA subscriber of one service.
type subscription struct {
	sid      string
	callback string
	service  string // "cds" or "cms"
	expires  time.Time
}

// subscribers is the registry, created lazily under Service.mu.
type subscribers struct {
	mu   sync.Mutex
	subs map[string]*subscription
}

// notifyClient delivers initial events. Package-level so tests exercise real
// HTTP against an in-process callback.
var notifyClient = &http.Client{Timeout: notifyTimeout}

func (s *Service) subscribersRegistry() *subscribers {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs == nil {
		s.subs = &subscribers{subs: map[string]*subscription{}}
	}
	return s.subs
}

// handleSubscribe implements SUBSCRIBE for both services: a new subscription
// when CALLBACK is present, a renewal when SID is.
func (s *Service) handleSubscribe(w http.ResponseWriter, r *http.Request, service string) {
	sid := strings.TrimSpace(r.Header.Get("SID"))
	callback := r.Header.Get("CALLBACK")
	nt := strings.TrimSpace(r.Header.Get("NT"))

	// The spec forbids mixing the two forms in one request.
	if sid != "" && (callback != "" || nt != "") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	reg := s.subscribersRegistry()
	timeout := parseSubTimeout(r.Header.Get("TIMEOUT"))

	if sid != "" { // renewal
		reg.mu.Lock()
		sub, ok := reg.subs[sid]
		if ok {
			sub.expires = time.Now().Add(timeout)
		}
		reg.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		writeSubscribeOK(w, sid, timeout)
		return
	}

	url := callbackURL(callback)
	if url == "" || nt != "upnp:event" {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}

	id, err := newUUID()
	if err != nil {
		http.Error(w, "subscription id unavailable", http.StatusInternalServerError)
		return
	}
	sub := &subscription{
		sid:      "uuid:" + id,
		callback: url,
		service:  service,
		expires:  time.Now().Add(timeout),
	}

	reg.mu.Lock()
	pruneExpiredLocked(reg)
	if len(reg.subs) >= maxSubscribers {
		reg.mu.Unlock()
		http.Error(w, "too many subscribers", http.StatusServiceUnavailable)
		return
	}
	reg.subs[sub.sid] = sub
	reg.mu.Unlock()

	writeSubscribeOK(w, sub.sid, timeout)

	// The initial event must follow the subscription response, not block it.
	go s.sendInitialEvent(sub)
}

// handleUnsubscribe implements UNSUBSCRIBE.
func (s *Service) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("CALLBACK") != "" || r.Header.Get("NT") != "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	sid := strings.TrimSpace(r.Header.Get("SID"))
	reg := s.subscribersRegistry()

	reg.mu.Lock()
	_, ok := reg.subs[sid]
	delete(reg.subs, sid)
	pruneExpiredLocked(reg)
	reg.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func pruneExpiredLocked(reg *subscribers) {
	now := time.Now()
	for sid, sub := range reg.subs {
		if now.After(sub.expires) {
			delete(reg.subs, sid)
		}
	}
}

func writeSubscribeOK(w http.ResponseWriter, sid string, timeout time.Duration) {
	w.Header().Set("SID", sid)
	w.Header().Set("TIMEOUT", fmt.Sprintf("Second-%d", int(timeout.Seconds())))
	w.WriteHeader(http.StatusOK)
}

// parseSubTimeout reads "Second-1800". "infinite" and garbage both get the
// default: an infinite subscription is a leak with a spec name.
func parseSubTimeout(header string) time.Duration {
	raw, ok := strings.CutPrefix(strings.TrimSpace(header), "Second-")
	if !ok {
		return defaultSubTimeout
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return defaultSubTimeout
	}
	return min(time.Duration(secs)*time.Second, maxSubTimeout)
}

// callbackURL extracts the first http URL from a CALLBACK header, which is a
// list of "<url>" entries.
func callbackURL(header string) string {
	for rest := header; ; {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			return ""
		}
		close := strings.IndexByte(rest[open:], '>')
		if close < 0 {
			return ""
		}
		url := rest[open+1 : open+close]
		if strings.HasPrefix(url, "http://") {
			return url
		}
		rest = rest[open+close+1:]
	}
}

// sendInitialEvent delivers the SEQ-0 NOTIFY that carries the current value of
// every evented variable: the one event the spec requires. Failures are logged
// and dropped: the subscription stands either way, and a client that cannot
// receive its callback has a problem no retry of ours fixes.
func (s *Service) sendInitialEvent(sub *subscription) {
	// The subscription outlives the request that created it, so this runs on a
	// background context rather than a cancelled one.
	updateID, err := s.systemUpdateID(context.Background())
	if err != nil {
		s.log.Warn("dlna: initial event", "callback", sub.callback, "error", err)
		updateID = defaultSystemUpdateID
	}
	body := initialPropertySet(sub.service, updateID)
	req, err := http.NewRequest("NOTIFY", sub.callback, strings.NewReader(body))
	if err != nil {
		s.log.Warn("dlna: initial event", "callback", sub.callback, "error", err)
		return
	}
	req.Header.Set("Content-Type", contentTypeSOAP)
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SID", sub.sid)
	req.Header.Set("SEQ", "0")

	resp, err := notifyClient.Do(req)
	if err != nil {
		s.log.Warn("dlna: initial event", "callback", sub.callback, "error", err)
		return
	}
	resp.Body.Close()
}

// initialPropertySet is the GENA property set for one service's evented
// variables. Values are XML-escaped by construction: everything here is
// either constant or already-escaped protocolInfo tokens.
func initialPropertySet(service, updateID string) string {
	var b strings.Builder
	b.WriteString(`<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">`)
	if service == "cms" {
		b.WriteString(`<e:property><SourceProtocolInfo>` + sourceProtocolInfo() + `</SourceProtocolInfo></e:property>`)
		b.WriteString(`<e:property><SinkProtocolInfo></SinkProtocolInfo></e:property>`)
		b.WriteString(`<e:property><CurrentConnectionIDs>0</CurrentConnectionIDs></e:property>`)
	} else {
		// Only what the SCPD declares evented: SystemUpdateID and nothing else.
		b.WriteString(`<e:property><SystemUpdateID>` + updateID + `</SystemUpdateID></e:property>`)
	}
	b.WriteString(`</e:propertyset>`)
	return b.String()
}
