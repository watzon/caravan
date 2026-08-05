package dlna

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/store"
)

// maxSOAPBody caps a control request. Browse arguments are a few hundred bytes;
// anything larger is a mistake or an attack, and neither deserves the memory.
const maxSOAPBody = 64 << 10

// defaultSystemUpdateID is what an install nobody has reconfigured reports.
const defaultSystemUpdateID = "1"

// systemUpdateID is the ContentDirectory's "has anything changed" counter.
//
// It does not track library contents: a counter that moved every time a file
// landed would make polling clients re-browse all day for a library that
// changes a few times an hour, and this server sends no event to carry the
// change anyway. It tracks the shape of the tree instead — which libraries are
// advertised (store.SettingDLNAUpdateID, bumped when dlna_visible flips) —
// because that is the change a client's cache renders as a shelf that is gone
// from the server but still on the television.
func (s *Service) systemUpdateID(ctx context.Context) (string, error) {
	raw, err := s.st.GetSetting(ctx, store.SettingDLNAUpdateID)
	if errors.Is(err, store.ErrNotFound) {
		return defaultSystemUpdateID, nil
	}
	if err != nil {
		return "", err
	}
	// A value the counter cannot be read out of reads as the default rather
	// than as an error: this is cache metadata, and refusing to Browse over it
	// would turn a bad row into a library that will not open.
	n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil || n == 0 {
		return defaultSystemUpdateID, nil
	}
	return strconv.FormatUint(n, 10), nil
}

// Handler is the DLNA HTTP surface: device and service descriptions, the SOAP
// control endpoints, and the file server.
//
// The patterns carry MountPath rather than being mounted under a StripPrefix,
// so the URLs written into device.xml and into every res element are literally
// the ones registered here.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+MountPath+"/device.xml", s.handleDeviceDescription)
	mux.HandleFunc("GET "+MountPath+"/cds.xml", staticXML(contentDirectorySCPD))
	mux.HandleFunc("GET "+MountPath+"/cms.xml", staticXML(connectionManagerSCPD))
	mux.HandleFunc("POST "+MountPath+"/control/cds", s.handleContentDirectory)
	mux.HandleFunc("POST "+MountPath+"/control/cms", s.handleConnectionManager)
	// GENA eventing (events.go). Both services' evented variables are static
	// while the server runs, but the subscription handshake itself is what
	// several clients require before they will browse at all.
	mux.HandleFunc("SUBSCRIBE "+contentDirectoryEventURL, func(w http.ResponseWriter, r *http.Request) {
		s.handleSubscribe(w, r, "cds")
	})
	mux.HandleFunc("SUBSCRIBE "+connectionManagerEventURL, func(w http.ResponseWriter, r *http.Request) {
		s.handleSubscribe(w, r, "cms")
	})
	mux.HandleFunc("UNSUBSCRIBE "+contentDirectoryEventURL, s.handleUnsubscribe)
	mux.HandleFunc("UNSUBSCRIBE "+connectionManagerEventURL, s.handleUnsubscribe)
	// A GET pattern also matches HEAD, which is how several renderers probe a
	// file's length and range support before they start playing it.
	mux.HandleFunc("GET "+MountPath+"/media/{name}", s.handleMedia)
	return s.whenEnabled(s.withTrace(mux))
}

// whenEnabled makes the whole DLNA surface disappear while the feature is
// switched off.
//
// This mount sits outside the API's password gate, because a television cannot
// log in. That exemption is only defensible for a feature the owner asked for:
// with dlna_enabled=false they have said the library is not to be served to the
// LAN, and without this check any device could still POST a ContentDirectory
// Browse to /dlna/control/cds, walk the container tree and GET every media file
// with no credential at all — past a password that was protecting only the JSON
// API.
//
// The settings are read per request rather than cached, so the toggle takes
// effect on the next request exactly as it does for SSDP (see Reload).
func (s *Service) whenEnabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := s.Config(r.Context())
		if err != nil {
			s.log.Error("dlna: read settings", "error", err)
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		if !cfg.Enabled {
			// 404, not 403: a disabled media server is one that is not here.
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// origin is the scheme and authority the client used to reach us, which every
// URL handed back has to be built on (see the urls type).
func origin(r *http.Request) urls {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return urls{origin: scheme + "://" + r.Host}
}

func staticXML(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeXML(w, body)
	}
}

func writeXML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

// handleDeviceDescription serves the document SSDP's LOCATION points at. It is
// rendered per request because the friendly name is editable at runtime and the
// service URLs are relative to whatever address the client reached us on.
func (s *Service) handleDeviceDescription(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Config(r.Context())
	if err != nil {
		s.log.Error("dlna: read settings", "error", err)
		http.Error(w, "device description unavailable", http.StatusInternalServerError)
		return
	}
	writeXML(w, deviceDescription(cfg.FriendlyName, cfg.UUID))
}

// handleContentDirectory is the ContentDirectory control endpoint.
func (s *Service) handleContentDirectory(w http.ResponseWriter, r *http.Request) {
	service, action, ok := parseSOAPAction(r.Header.Get("SOAPACTION"))
	if !ok || service != contentDirectoryType {
		writeSOAPFault(w, errInvalidArgs, "unrecognized SOAPACTION")
		return
	}

	switch action {
	case "Browse":
		s.handleBrowse(w, r, service)
	case "GetSearchCapabilities":
		writeSOAPResponse(w, service, action, []soapArg{{Name: "SearchCaps", Value: searchCaps}})
	case "GetSortCapabilities":
		// Empty means "results come back in the server's order". Caravan's
		// order — sort title, then season and episode — is already the one a
		// user wants, and claiming sortability we do not implement would have
		// clients ask for orderings we would silently ignore.
		writeSOAPResponse(w, service, action, []soapArg{{Name: "SortCaps", Value: ""}})
	case "GetSystemUpdateID":
		id, err := s.systemUpdateID(r.Context())
		if err != nil {
			s.log.Error("dlna: read system update id", "error", err)
			writeSOAPFault(w, errActionFailed, "system update id unavailable")
			return
		}
		writeSOAPResponse(w, service, action, []soapArg{{Name: "Id", Value: id}})
	case "Search":
		s.handleSearch(w, r, service)
	default:
		writeSOAPFault(w, errInvalidArgs, "unsupported action "+action)
	}
}

func (s *Service) handleBrowse(w http.ResponseWriter, r *http.Request, service string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSOAPBody))
	if err != nil {
		writeSOAPFault(w, errInvalidArgs, "unreadable request body")
		return
	}
	args, err := decodeBrowse(body)
	if err != nil {
		writeSOAPFault(w, errInvalidArgs, "invalid Browse arguments")
		return
	}

	ctx := r.Context()
	u := origin(r)
	var didl *didlLite
	if args.BrowseFlag == browseMetadata {
		didl, err = s.metadata(ctx, u, args.ObjectID)
	} else {
		didl, err = s.children(ctx, u, args.ObjectID)
	}
	if errors.Is(err, errNoObject) {
		writeSOAPFault(w, errNoSuchObject, "no such object")
		return
	}
	if err != nil {
		s.log.Error("dlna: browse failed", "object", args.ObjectID, "error", err)
		writeSOAPFault(w, errActionFailed, "browse failed")
		return
	}

	// TotalMatches counts the whole result set; NumberReturned counts the
	// window the client asked for. A client pages by walking the second until
	// it has the first.
	total := didl.count()
	page := didl.slice(args.StartingIndex, args.RequestedCount)
	encoded, err := page.encode()
	if err != nil {
		s.log.Error("dlna: encode browse result", "object", args.ObjectID, "error", err)
		writeSOAPFault(w, errActionFailed, "browse failed")
		return
	}
	updateID, err := s.systemUpdateID(ctx)
	if err != nil {
		s.log.Error("dlna: read system update id", "error", err)
		writeSOAPFault(w, errActionFailed, "browse failed")
		return
	}

	writeSOAPResponse(w, service, "Browse", []soapArg{
		{Name: "Result", Value: encoded},
		{Name: "NumberReturned", Value: strconv.Itoa(page.count())},
		{Name: "TotalMatches", Value: strconv.Itoa(total)},
		{Name: "UpdateID", Value: updateID},
	})
}

// handleSearch answers the Search action (search.go): the filtered flattening
// of the subtree under ContainerID, paged exactly like Browse.
func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request, service string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSOAPBody))
	if err != nil {
		writeSOAPFault(w, errInvalidArgs, "unreadable request body")
		return
	}
	args, err := decodeSearch(body)
	if err != nil {
		writeSOAPFault(w, errInvalidArgs, "invalid Search arguments")
		return
	}

	didl, err := s.search(r.Context(), origin(r), args.ContainerID, args.SearchCriteria)
	if errors.Is(err, errNoObject) {
		writeSOAPFault(w, errNoSuchObject, "no such container")
		return
	}
	if err != nil {
		s.log.Error("dlna: search failed", "container", args.ContainerID, "error", err)
		writeSOAPFault(w, errActionFailed, "search failed")
		return
	}

	total := didl.count()
	page := didl.slice(args.StartingIndex, args.RequestedCount)
	encoded, err := page.encode()
	if err != nil {
		s.log.Error("dlna: encode search result", "container", args.ContainerID, "error", err)
		writeSOAPFault(w, errActionFailed, "search failed")
		return
	}
	updateID, err := s.systemUpdateID(r.Context())
	if err != nil {
		s.log.Error("dlna: read system update id", "error", err)
		writeSOAPFault(w, errActionFailed, "search failed")
		return
	}

	writeSOAPResponse(w, service, "Search", []soapArg{
		{Name: "Result", Value: encoded},
		{Name: "NumberReturned", Value: strconv.Itoa(page.count())},
		{Name: "TotalMatches", Value: strconv.Itoa(total)},
		{Name: "UpdateID", Value: updateID},
	})
}

// handleConnectionManager implements the minimum of the service the MediaServer
// device template requires. Nothing here is optional: a device that lists
// ConnectionManager and then 404s its control URL is one some renderers refuse
// outright, so the three actions that get called are answered.
func (s *Service) handleConnectionManager(w http.ResponseWriter, r *http.Request) {
	service, action, ok := parseSOAPAction(r.Header.Get("SOAPACTION"))
	if !ok || service != connectionManagerType {
		writeSOAPFault(w, errInvalidArgs, "unrecognized SOAPACTION")
		return
	}

	switch action {
	case "GetProtocolInfo":
		writeSOAPResponse(w, service, action, []soapArg{
			{Name: "Source", Value: sourceProtocolInfo()},
			// Empty: this is a server, it never sinks a stream.
			{Name: "Sink", Value: ""},
		})
	case "GetCurrentConnectionIDs":
		// 0 is the always-present default connection. HTTP GET needs no
		// connection setup, so it is the only one that ever exists.
		writeSOAPResponse(w, service, action, []soapArg{{Name: "ConnectionIDs", Value: "0"}})
	case "GetCurrentConnectionInfo":
		writeSOAPResponse(w, service, action, []soapArg{
			{Name: "RcsID", Value: "-1"},
			{Name: "AVTransportID", Value: "-1"},
			{Name: "ProtocolInfo", Value: ""},
			{Name: "PeerConnectionManager", Value: ""},
			{Name: "PeerConnectionID", Value: "-1"},
			{Name: "Direction", Value: "Output"},
			{Name: "Status", Value: "OK"},
		})
	default:
		writeSOAPFault(w, errInvalidArgs, "unsupported action "+action)
	}
}

// sourceProtocolInfo is every protocolInfo this server can hand out, which is
// what a renderer filters its browse against before it even asks.
func sourceProtocolInfo() string {
	seen := map[string]bool{}
	out := []string{}
	for _, mime := range mimeByExt {
		if seen[mime] {
			continue
		}
		seen[mime] = true
		out = append(out, "http-get:*:"+mime+":"+dlnaFlags)
	}
	// mimeByExt is a map, so the order it yields is not stable; sorted output
	// keeps this response byte-identical between calls.
	slices.Sort(out)
	return strings.Join(out, ",")
}

// handleMedia serves one library file, byte ranges and all, straight off disk.
//
// There is no transcoding here and there never will be: SPEC §8 answers "my TV
// cannot play this" with a converted file the whole library can use, not with a
// stream that only exists while one client is watching.
func (s *Service) handleMedia(w http.ResponseWriter, r *http.Request) {
	// The name is "<media file id><extension>"; the extension is there for
	// clients that pick a demuxer from the URL, and is not otherwise read.
	name := r.PathValue("name")
	idRaw := strings.TrimSuffix(name, path.Ext(name))
	id, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	kind, err := s.st.GetMediaFileLibraryKind(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	visible, err := s.visibleLibraries(ctx)
	if err != nil || !visible[kind] {
		http.NotFound(w, r)
		return
	}
	file, err := s.st.GetMediaFile(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, info, err := s.openInRoot(ctx, file.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	// Content-Type is set before ServeContent so the container's own mapping
	// wins over net/http's extension table, which does not know .mkv.
	w.Header().Set("Content-Type", mimeForPath(file.Path))
	w.Header().Set("Accept-Ranges", "bytes")
	// DLNA's transfer headers. The client asks for contentFeatures with a
	// request header; answering unasked confuses some older renderers.
	w.Header().Set("transferMode.dlna.org", "Streaming")
	if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
		w.Header().Set("contentFeatures.dlna.org", dlnaFlags)
	}
	http.ServeContent(w, r, path.Base(file.Path), info.ModTime(), f)
}

// openInRoot opens a storage-root-relative path, confined to the root.
//
// os.Root is the confinement: a "../" segment or a symlink pointing out of the
// library fails here rather than turning the media endpoint into an
// unauthenticated reader of the whole filesystem. Every path in the database is
// relative (SPEC §1.2), so this is also the only place the absolute path
// exists.
func (s *Service) openInRoot(ctx context.Context, rel string) (*os.File, os.FileInfo, error) {
	root, err := s.root(ctx)
	if err != nil {
		return nil, nil, err
	}
	if root == "" {
		return nil, nil, errors.New("dlna: no storage root configured")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer dir.Close()

	f, err := dir.Open(rel)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = errors.New("dlna: not a regular file")
		}
		return nil, nil, err
	}
	return f, info, nil
}
