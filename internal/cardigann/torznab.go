package cardigann

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
)

const (
	torznabNamespace        = "http://torznab.com/schemas/2015/feed"
	maxTorznabResponseBytes = 8 << 20
)

var errTorznabResponseTooLarge = errors.New("Torznab response exceeds output limit")

// NewTorznabHandler exposes one local scraper engine using the Torznab API
// contract. It deliberately carries no authentication policy; the server that
// mounts it owns API-key validation and route visibility.
func NewTorznabHandler(engine *Engine) http.Handler {
	if engine == nil {
		return &torznabHandler{}
	}
	return &torznabHandler{
		title:  engine.def.Name,
		source: engineFeedSource{engine: engine},
		modes:  engine.def.Capabilities().Modes,
	}
}

// NewClientTorznabHandler exposes an in-process configured indexer as a
// Torznab feed. Caravan uses this for stored local definitions so its normal
// searches do not recursively call the public HTTP listener.
func NewClientTorznabHandler(title string, source FeedSource) http.Handler {
	modes := map[string]bool{"search": true}
	if provider, ok := source.(interface{ Modes() map[string]bool }); ok {
		modes = provider.Modes()
	}
	return &torznabHandler{title: title, source: source, modes: modes}
}

// FeedSource is the part of Caravan's indexer client contract needed to render
// caps and search responses.
type FeedSource interface {
	Search(context.Context, string, []int) ([]core.Release, error)
	Categories(context.Context) ([]core.IndexerCategory, error)
}

type torznabHandler struct {
	title  string
	source FeedSource
	modes  map[string]bool
}

func (h *torznabHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.source == nil || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	mode := normalizeMode(r.URL.Query().Get("t"))
	switch mode {
	case "caps":
		h.writeCaps(w)
	case "search", "tvsearch", "movie", "music", "book":
		if mode != "search" && !h.modes[mode] {
			writeTorznabError(w, http.StatusBadRequest, 202, "unsupported function")
			return
		}
		h.writeSearch(w, r)
	default:
		writeTorznabError(w, http.StatusBadRequest, 202, "unsupported function")
	}
}

func (h *torznabHandler) writeCaps(w http.ResponseWriter) {
	modes := make([]capsModeXML, 0, len(h.modes))
	for _, mode := range []string{"search", "tvsearch", "movie", "music", "book"} {
		if !h.modes[mode] {
			continue
		}
		name := mode
		if mode == "tvsearch" {
			name = "tv-search"
		} else if mode != "search" {
			name += "-search"
		}
		modes = append(modes, capsModeXML{
			XMLName:   xml.Name{Local: name},
			Available: "yes",
			Supported: supportedParams(mode),
		})
	}
	// Search is intrinsic to every runnable definition even when an older
	// definition omitted caps.modes.search.
	if len(modes) == 0 || !h.modes["search"] {
		modes = append([]capsModeXML{{
			XMLName: xml.Name{Local: "search"}, Available: "yes", Supported: "q",
		}}, modes...)
	}
	advertised, err := h.source.Categories(context.Background())
	if err != nil {
		writeTorznabError(w, http.StatusBadGateway, 900, err.Error())
		return
	}
	categories := make([]capsCategoryXML, 0, len(advertised))
	for _, category := range advertised {
		categories = append(categories, capsCategoryXML{ID: category.ID, Name: category.Name})
	}
	writeXML(w, http.StatusOK, capsXML{
		Server:     capsServerXML{Title: h.title},
		Limits:     capsLimitsXML{Default: 100, Max: 100},
		Searching:  capsSearchingXML{Modes: modes},
		Categories: categories,
	})
}

func (h *torznabHandler) writeSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	categories := parseQueryCategories(q.Get("cat"))
	keywords := strings.TrimSpace(q.Get("q"))
	var results []core.Release
	var err error
	switch normalizeMode(q.Get("t")) {
	case "tvsearch":
		if source, ok := h.source.(interface {
			SearchTV(context.Context, string, int, int, []int) ([]core.Release, error)
		}); ok {
			results, err = source.SearchTV(r.Context(), keywords, queryInt(q.Get("season")), queryInt(firstNonEmpty(q.Get("ep"), q.Get("episode"))), categories)
		} else {
			results, err = h.source.Search(r.Context(), keywords, categories)
		}
	case "movie":
		if source, ok := h.source.(interface {
			SearchMovie(context.Context, string, []int) ([]core.Release, error)
		}); ok {
			results, err = source.SearchMovie(r.Context(), keywords, categories)
		} else {
			results, err = h.source.Search(r.Context(), keywords, categories)
		}
	default:
		results, err = h.source.Search(r.Context(), keywords, categories)
	}
	if err != nil {
		writeTorznabError(w, http.StatusBadGateway, 900, err.Error())
		return
	}
	items := make([]rssItemXML, 0, len(results))
	for _, result := range results {
		items = append(items, releaseXML(result))
	}
	writeXML(w, http.StatusOK, rssXML{
		Version: "2.0",
		Channel: rssChannelXML{
			Title:       h.title,
			Description: "Caravan local tracker proxy",
			Items:       items,
		},
	})
}

func releaseXML(release core.Release) rssItemXML {
	pubDate := ""
	if !release.PublishedAt.IsZero() {
		pubDate = release.PublishedAt.Format(time.RFC1123Z)
	}
	link := release.GUID
	if link == "" {
		link = release.DownloadURL
	}
	item := rssItemXML{
		Title:   release.Title,
		GUID:    release.GUID,
		Link:    link,
		PubDate: pubDate,
		Size:    release.Size,
		Enclosures: []rssEnclosureXML{{
			URL: release.DownloadURL, Length: release.Size, Type: "application/x-bittorrent",
		}},
		Attrs: []rssAttrXML{
			{Name: "size", Value: strconv.FormatInt(release.Size, 10)},
			{Name: "seeders", Value: strconv.Itoa(release.Seeders)},
			{Name: "peers", Value: strconv.Itoa(release.Seeders + release.Leechers)},
		},
	}
	if release.InfoHash != "" {
		item.Attrs = append(item.Attrs, rssAttrXML{Name: "infohash", Value: release.InfoHash})
	}
	if strings.HasPrefix(strings.ToLower(release.DownloadURL), "magnet:") {
		item.Attrs = append(item.Attrs, rssAttrXML{Name: "magneturl", Value: release.DownloadURL})
	}
	for _, category := range release.Categories {
		value := strconv.Itoa(category)
		item.Categories = append(item.Categories, value)
		item.Attrs = append(item.Attrs, rssAttrXML{Name: "category", Value: value})
	}
	for _, attribute := range core.NormalizeReleaseAttributes(release.Attributes) {
		item.Attrs = append(item.Attrs, rssAttrXML{Name: attribute.Name, Value: attribute.Value})
	}
	return item
}

func supportedParams(mode string) string {
	switch mode {
	case "tvsearch":
		return "q,season,ep"
	default:
		return "q"
	}
}

func queryInt(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

func parseQueryCategories(value string) []int {
	parts := strings.Split(value, ",")
	categories := make([]int, 0, len(parts))
	for _, part := range parts {
		if category := queryInt(part); category > 0 {
			categories = append(categories, category)
		}
	}
	return categories
}

type engineFeedSource struct{ engine *Engine }

func (s engineFeedSource) Search(ctx context.Context, query string, _ []int) ([]core.Release, error) {
	return s.engine.Search(ctx, Query{Keywords: query})
}

func (s engineFeedSource) SearchTV(ctx context.Context, query string, season, episode int, _ []int) ([]core.Release, error) {
	return s.engine.Search(ctx, Query{Keywords: query, Season: season, Episode: episode})
}

func (s engineFeedSource) SearchMovie(ctx context.Context, query string, _ []int) ([]core.Release, error) {
	return s.engine.Search(ctx, Query{Keywords: query})
}

func (s engineFeedSource) Categories(_ context.Context) ([]core.IndexerCategory, error) {
	return s.engine.def.Capabilities().Categories, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func writeTorznabError(w http.ResponseWriter, status, code int, description string) {
	writeXML(w, status, errorXML{Code: code, Description: description})
}

func writeXML(w http.ResponseWriter, status int, value any) {
	buffer := &boundedXMLBuffer{limit: maxTorznabResponseBytes}
	_, _ = buffer.Write([]byte(xml.Header))
	encoder := xml.NewEncoder(buffer)
	err := encoder.Encode(value)
	if errors.Is(err, errTorznabResponseTooLarge) {
		status = http.StatusBadGateway
		body, _ := xml.Marshal(errorXML{Code: 900, Description: "response exceeds output limit"})
		buffer.Reset()
		_, _ = buffer.Write([]byte(xml.Header))
		_, _ = buffer.Write(body)
		err = nil
	}
	if err != nil {
		http.Error(w, "encode XML", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buffer.Bytes())
}

type boundedXMLBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedXMLBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		return 0, errTorznabResponseTooLarge
	}
	return b.Buffer.Write(data)
}

type capsXML struct {
	XMLName    xml.Name          `xml:"caps"`
	Server     capsServerXML     `xml:"server"`
	Limits     capsLimitsXML     `xml:"limits"`
	Searching  capsSearchingXML  `xml:"searching"`
	Categories []capsCategoryXML `xml:"categories>category"`
}

type capsServerXML struct {
	Title string `xml:"title,attr"`
}

type capsLimitsXML struct {
	Default int `xml:"default,attr"`
	Max     int `xml:"max,attr"`
}

type capsSearchingXML struct {
	Modes []capsModeXML `xml:",any"`
}

type capsModeXML struct {
	XMLName   xml.Name
	Available string `xml:"available,attr"`
	Supported string `xml:"supportedParams,attr"`
}

type capsCategoryXML struct {
	ID   int    `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type rssXML struct {
	XMLName xml.Name      `xml:"rss"`
	Version string        `xml:"version,attr"`
	Channel rssChannelXML `xml:"channel"`
}

type rssChannelXML struct {
	Title       string       `xml:"title"`
	Description string       `xml:"description"`
	Items       []rssItemXML `xml:"item"`
}

type rssItemXML struct {
	Title      string            `xml:"title"`
	GUID       string            `xml:"guid"`
	Link       string            `xml:"link"`
	PubDate    string            `xml:"pubDate,omitempty"`
	Size       int64             `xml:"size,omitempty"`
	Categories []string          `xml:"category"`
	Enclosures []rssEnclosureXML `xml:"enclosure"`
	Attrs      []rssAttrXML      `xml:"attr"`
}

type rssEnclosureXML struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr,omitempty"`
	Type   string `xml:"type,attr"`
}

type rssAttrXML struct {
	XMLName xml.Name `xml:"http://torznab.com/schemas/2015/feed attr"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

type errorXML struct {
	XMLName     xml.Name `xml:"error"`
	Code        int      `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}
