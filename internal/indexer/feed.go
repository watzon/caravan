package indexer

import (
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
)

// The XML shapes below name elements by local name only, so the same structs
// read Torznab (`torznab:attr`) and Newznab (`newznab:attr`) documents:
// encoding/xml matches on local name when the tag carries no namespace.

// feedDoc is a search response: RSS 2.0 with per-item extension attributes.
type feedDoc struct {
	Channel struct {
		Items []feedItem `xml:"item"`
	} `xml:"channel"`
}

// feedItem is one search result as published.
type feedItem struct {
	Title   string `xml:"title"`
	GUID    string `xml:"guid"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	Size    string `xml:"size"`
	// Categories are the plain RSS <category> elements. Indexers publish the
	// same ids there and as `category` extension attributes, and which of the
	// two they use varies, so both are read.
	Categories []string        `xml:"category"`
	Enclosures []feedEnclosure `xml:"enclosure"`
	Attrs      []feedAttr      `xml:"attr"`
}

// feedEnclosure is one download link. A slice, not a struct: items can carry
// several — AnimeTosho publishes a .torrent and an .nzb enclosure on the same
// item — and a single field would silently keep only the last one
// (encoding/xml overwrites on repeat), handing a torrent grab the .nzb URL.
type feedEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// feedAttr is a torznab:attr / newznab:attr name-value pair.
type feedAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// capsDoc is the t=caps response. The search modes prove the endpoint is an
// indexer API rather than a login page that happened to return XML; the
// category tree is what the settings UI offers as a picker.
type capsDoc struct {
	Searching struct {
		Modes []struct {
			Available string `xml:"available,attr"`
		} `xml:",any"`
	} `xml:"searching"`
	Categories []capsCategory `xml:"categories>category"`
}

// capsCategory is one advertised category. Subcategories nest exactly one
// level in the Newznab spec (`<category><subcat/></category>`).
type capsCategory struct {
	ID      string         `xml:"id,attr"`
	Name    string         `xml:"name,attr"`
	Subcats []capsCategory `xml:"subcat"`
}

// categoryTree converts advertised categories into the shape the API serves,
// preserving the indexer's order. A node whose id is not a positive integer is
// dropped with its subtree: the id is what searches send and the configuration
// stores, so a label alone is not offerable.
func categoryTree(cats []capsCategory) []core.IndexerCategory {
	out := make([]core.IndexerCategory, 0, len(cats))
	for _, cat := range cats {
		id, err := strconv.Atoi(strings.TrimSpace(cat.ID))
		if err != nil || id <= 0 {
			continue
		}
		out = append(out, core.IndexerCategory{
			ID:      id,
			Name:    strings.TrimSpace(cat.Name),
			Subcats: categoryTree(cat.Subcats),
		})
	}
	return out
}

// errorDoc is the <error code="" description=""/> failure document.
type errorDoc struct {
	Code        int    `xml:"code,attr"`
	Description string `xml:"description,attr"`
}

// pubDateLayouts are the formats indexers publish dates in. RFC1123Z is what
// the spec asks for; the rest are what indexers actually send.
var pubDateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC3339,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"2006-01-02 15:04:05",
}

// releases converts feed items into releases, dropping the ones that carry no
// usable claim. An indexer serving one broken item must still return its good
// ones, so nothing here fails the batch.
func (c *Client) releases(items []feedItem) []core.Release {
	out := make([]core.Release, 0, len(items))
	for _, it := range items {
		r, ok := c.release(it)
		if !ok {
			continue
		}
		out = append(out, r)
	}
	return out
}

// release converts one item. It reports false when the item has no title or no
// way to download it, which are the two things a release cannot be without.
func (c *Client) release(it feedItem) (core.Release, bool) {
	title := strings.TrimSpace(it.Title)
	if title == "" {
		return core.Release{}, false
	}

	attrs := attrMap(it.Attrs)

	protocol := c.protocol(it, attrs)
	downloadURL := pickDownloadURL(it, attrs, protocol)
	if downloadURL == "" {
		return core.Release{}, false
	}

	guid := strings.TrimSpace(it.GUID)
	if guid == "" {
		// Without a guid the download URL is the only stable identity the
		// item has, and it is what dedup will key on.
		guid = downloadURL
	}

	seeders := atoiAttr(attrs["seeders"])
	leechers := atoiAttr(attrs["leechers"])
	if leechers == 0 {
		// Torznab's `peers` is the whole swarm, seeders included.
		if peers := atoiAttr(attrs["peers"]); peers > seeders {
			leechers = peers - seeders
		}
	}

	cats := categories(it)
	r := core.Release{
		IndexerID:   c.cfg.ID,
		Indexer:     c.cfg.Name,
		Title:       title,
		GUID:        guid,
		DownloadURL: downloadURL,
		InfoHash:    strings.ToLower(strings.TrimSpace(attrs["infohash"])),
		Protocol:    protocol,
		Size:        size(it, attrs),
		Seeders:     seeders,
		Leechers:    leechers,
		PublishedAt: publishedAt(it, attrs),
		Categories:  cats,
		Parsed:      parseTitle(title, cats),
		Attributes:  extensionAttributes(it.Attrs),
	}
	return r, true
}

func extensionAttributes(attributes []feedAttr) []core.ReleaseAttribute {
	out := make([]core.ReleaseAttribute, 0, len(attributes))
	for _, attribute := range attributes {
		out = append(out, core.ReleaseAttribute{Name: attribute.Name, Value: attribute.Value})
	}
	return core.NormalizeReleaseAttributes(out)
}

// parseTitle reads a result's name the way the category it was published under
// says it is named (PLAN phase 9 task 4).
//
// The category is the selector rather than the shape of the name itself,
// because a date in a television name is a daily episode and reading it as a
// scene date would change what an existing release means. An item the indexer
// filed under XXX is named the way scenes are named — and parse.Scene falls
// back to parse.Parse when the name turns out not to be date-shaped, so an
// indexer that mis-files a television release under 6000 still parses
// correctly.
func parseTitle(title string, cats []int) core.ParsedRelease {
	if core.HasAdultCategory(cats) {
		return parse.Scene(title)
	}
	return parse.Parse(title)
}

// protocol decides which engine a grab would route to. The configured type is
// the default; item evidence overrides it, because a Torznab proxy in front of
// a Usenet indexer is a real configuration.
func (c *Client) protocol(it feedItem, attrs map[string]string) string {
	switch {
	case attrs["infohash"] != "" || attrs["magneturl"] != "":
		return core.ProtocolTorrent
	// Torrent evidence outranks nzb evidence when an item carries both kinds
	// of enclosure: the embedded engine can act on a torrent today.
	case enclosureByType(it.Enclosures, "bittorrent") != "":
		return core.ProtocolTorrent
	case enclosureByType(it.Enclosures, "nzb") != "":
		return core.ProtocolUsenet
	case c.cfg.Type == core.IndexerTypeNewznab:
		return core.ProtocolUsenet
	default:
		return core.ProtocolTorrent
	}
}

// pickDownloadURL picks where a release downloads from, preferring the source
// that matches its protocol. The pick has to be type-aware: handing a torrent
// grab an item's .nzb enclosure fetches XML and dies at bencode parsing.
func pickDownloadURL(it feedItem, attrs map[string]string, protocol string) string {
	want := "bittorrent"
	if protocol == core.ProtocolUsenet {
		want = "nzb"
	}
	if u := enclosureByType(it.Enclosures, want); u != "" {
		return u
	}
	// A magnet is a complete torrent source; the item link is a web page as
	// often as a file, so it stays last.
	if protocol == core.ProtocolTorrent {
		if u := strings.TrimSpace(attrs["magneturl"]); u != "" {
			return u
		}
	}
	for _, enc := range it.Enclosures {
		if u := strings.TrimSpace(enc.URL); u != "" {
			return u
		}
	}
	return firstNonEmpty(strings.TrimSpace(it.Link), strings.TrimSpace(attrs["magneturl"]))
}

// enclosureByType returns the first enclosure whose MIME type contains kind,
// or "".
func enclosureByType(encs []feedEnclosure, kind string) string {
	for _, enc := range encs {
		if strings.Contains(enc.Type, kind) && strings.TrimSpace(enc.URL) != "" {
			return strings.TrimSpace(enc.URL)
		}
	}
	return ""
}

// size reads the release size from wherever this indexer put it. 0 means the
// indexer did not say, which is a fact the scorer has to handle anyway.
func size(it feedItem, attrs map[string]string) int64 {
	candidates := []string{it.Size, attrs["size"]}
	for _, enc := range it.Enclosures {
		candidates = append(candidates, enc.Length)
	}
	for _, s := range candidates {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// publishedAt reads the publication date, preferring the RSS pubDate and
// falling back to Newznab's usenetdate attribute. An unreadable date yields the
// zero time rather than dropping the release.
func publishedAt(it feedItem, attrs map[string]string) time.Time {
	for _, s := range []string{it.PubDate, attrs["usenetdate"]} {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		for _, layout := range pubDateLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// categories reads every category id the item was published in.
//
// It goes to the raw attribute list rather than through attrMap because
// `category` is the one attribute indexers deliberately repeat — an item in
// 5000 and 5040 carries both — and attrMap keeps only the first. Anything that
// is not a positive integer is dropped: a label alone cannot be matched against
// a configured id.
func categories(it feedItem) []int {
	out := []int{}
	seen := map[int]bool{}
	values := append([]string(nil), it.Categories...)
	for _, a := range it.Attrs {
		if strings.EqualFold(strings.TrimSpace(a.Name), "category") {
			values = append(values, a.Value)
		}
	}
	for _, v := range values {
		id, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// attrMap indexes extension attributes by lowercased name. Later duplicates
// lose: indexers that repeat an attribute mean the first one.
func attrMap(attrs []feedAttr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		name := strings.ToLower(strings.TrimSpace(a.Name))
		if name == "" {
			continue
		}
		if _, seen := m[name]; seen {
			continue
		}
		m[name] = strings.TrimSpace(a.Value)
	}
	return m
}

// atoiAttr reads a numeric attribute, treating junk as absent.
func atoiAttr(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// firstNonEmpty returns the first non-empty argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
