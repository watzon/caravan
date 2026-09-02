// Package catalog is the add-indexer directory: named Newznab/Torznab
// presets and generic Jackett/Prowlarr/Newznab/Torznab sources.
//
// Caravan speaks Torznab and Newznab (SPEC §5.1) and can execute explicitly
// supported local definitions. Homepage-only metadata stays hidden until the
// local engine accepts and tests its definition.
package catalog

import (
	"sort"
	"strings"
	"sync"
)

const (
	KindTorrent = "torrent"
	KindUsenet  = "usenet"
	KindGeneric = "generic"

	PrivacyPublic      = "public"
	PrivacyPrivate     = "private"
	PrivacySemiPrivate = "semi-private"

	ContentMovies = "movies"
	ContentTV     = "tv"
	ContentAnime  = "anime"
	ContentAudio  = "audio"
	ContentBooks  = "books"
	ContentAdult  = "adult"
	ContentPC     = "pc"
	ContentOther  = "other"
)

// Definition is one row in the add-indexer picker.
type Definition struct {
	ID             string   `json:"id"`
	DefinitionID   string   `json:"definition_id"`
	MetadataID     string   `json:"metadata_id,omitempty"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Protocol       string   `json:"protocol"`
	Privacy        string   `json:"privacy"`
	Language       string   `json:"language"`
	Description    string   `json:"description"`
	InfoURL        string   `json:"info_url"`
	URL            string   `json:"url"`
	URLs           []string `json:"urls"`
	URLPlaceholder string   `json:"url_placeholder"`
	RequiresAPIKey bool     `json:"requires_api_key"`
	Categories     []int    `json:"categories"`
	// Content is the coarse media kinds this source is good for (movies, TV,
	// anime, and so on) used by the add-indexer picker, not sent to the API.
	Content  []string  `json:"content"`
	Settings []Setting `json:"settings"`
}

// Setting describes one local-definition input in the add-indexer form.
type Setting struct {
	Name     string          `json:"name"`
	Label    string          `json:"label"`
	Type     string          `json:"type"`
	Default  string          `json:"default"`
	Options  []SettingOption `json:"options,omitempty"`
	Secret   bool            `json:"secret"`
	Editable bool            `json:"editable"`
}

// SettingOption is one select value shown by Add Indexer.
type SettingOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var (
	loadOnce sync.Once
	all      []Definition
	byID     map[string]Definition
)

func load() {
	loadOnce.Do(func() {
		merged := make([]Definition, 0, len(presets))
		for _, def := range presets {
			def = normalize(def)
			merged = append(merged, def)
		}
		sort.SliceStable(merged, func(i, j int) bool {
			if ki, kj := kindOrder(merged[i].Kind), kindOrder(merged[j].Kind); ki != kj {
				return ki < kj
			}
			return strings.ToLower(merged[i].Name) < strings.ToLower(merged[j].Name)
		})
		all = merged
		byID = make(map[string]Definition, len(merged))
		for _, def := range merged {
			byID[def.ID] = def
		}
	})
}

func jackettPlaceholder(id string) string {
	return "http://127.0.0.1:9117/api/v2.0/indexers/" + id + "/results/torznab"
}

func normalize(def Definition) Definition {
	def.ID = strings.TrimSpace(def.ID)
	def.DefinitionID = strings.TrimSpace(def.DefinitionID)
	def.Name = strings.TrimSpace(def.Name)
	if def.Name == "" {
		def.Name = def.ID
	}
	def.Kind = strings.TrimSpace(def.Kind)
	def.Protocol = strings.TrimSpace(def.Protocol)
	def.Privacy = strings.TrimSpace(def.Privacy)
	if def.Privacy == "" {
		def.Privacy = PrivacyPrivate
	}
	def.Language = strings.TrimSpace(def.Language)
	if def.Language == "" {
		def.Language = "en-US"
	}
	def.Description = strings.TrimSpace(def.Description)
	def.InfoURL = strings.TrimSpace(def.InfoURL)
	def.URLs = normalizeURLs(def.URLs)
	def.URL = strings.TrimRight(strings.TrimSpace(def.URL), "/")
	if def.URL == "" && len(def.URLs) > 0 {
		def.URL = def.URLs[0]
	}
	if def.URL != "" {
		def.URLs = prependURL(def.URLs, def.URL)
	}
	def.URLPlaceholder = strings.TrimSpace(def.URLPlaceholder)
	if def.URLPlaceholder == "" && def.Kind == KindTorrent && def.URL == "" {
		def.URLPlaceholder = jackettPlaceholder(def.ID)
	}
	if def.Categories == nil {
		def.Categories = []int{}
	}
	if def.Settings == nil {
		def.Settings = []Setting{}
	}
	for i := range def.Settings {
		if def.Settings[i].Label == "" {
			def.Settings[i].Label = def.Settings[i].Name
		}
		if def.Settings[i].Type != "info" {
			def.Settings[i].Editable = true
		}
		if def.Settings[i].Options == nil {
			def.Settings[i].Options = []SettingOption{}
		}
	}
	if len(def.Content) == 0 {
		def.Content = contentFromCategories(def.Categories)
	} else {
		def.Content = normalizeContent(def.Content)
	}
	return def
}

var contentOrder = []string{
	ContentMovies, ContentTV, ContentAnime, ContentAudio,
	ContentBooks, ContentAdult, ContentPC, ContentOther,
}

func contentFromCategories(cats []int) []string {
	found := map[string]bool{}
	for _, cat := range cats {
		switch {
		case cat == 5070 || (cat > 5070 && cat < 5080):
			found[ContentAnime] = true
		case cat >= 2000 && cat < 3000:
			found[ContentMovies] = true
		case cat >= 5000 && cat < 6000:
			found[ContentTV] = true
		case cat >= 3000 && cat < 4000:
			found[ContentAudio] = true
		case cat >= 4000 && cat < 5000:
			found[ContentPC] = true
		case cat >= 6000 && cat < 7000:
			found[ContentAdult] = true
		case cat >= 7000 && cat < 8000:
			found[ContentBooks] = true
		case cat > 0:
			found[ContentOther] = true
		}
	}
	if found[ContentOther] && len(found) > 1 {
		delete(found, ContentOther)
	}
	return normalizeContent(keys(found))
}

func normalizeContent(tags []string) []string {
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(strings.ToLower(tag))
		switch tag {
		case ContentMovies, ContentTV, ContentAnime, ContentAudio,
			ContentBooks, ContentAdult, ContentPC, ContentOther:
			seen[tag] = true
		}
	}
	out := make([]string, 0, len(seen))
	for _, tag := range contentOrder {
		if seen[tag] {
			out = append(out, tag)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func keys(seen map[string]bool) []string {
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	return out
}

func normalizeURLs(urls []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(urls))
	for _, raw := range urls {
		u := strings.TrimRight(strings.TrimSpace(raw), "/")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func prependURL(urls []string, url string) []string {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	out := []string{url}
	for _, u := range urls {
		if u != url {
			out = append(out, u)
		}
	}
	return out
}

func kindOrder(kind string) int {
	switch kind {
	case KindGeneric:
		return 0
	case KindUsenet:
		return 1
	case KindTorrent:
		return 2
	default:
		return 9
	}
}

// All returns every definition, generics first, then Usenet, then torrent
// sites, each group sorted by name.
func All() []Definition {
	load()
	out := make([]Definition, len(all))
	copy(out, all)
	return out
}

// ByKind returns the definitions of one kind, or nil when kind is unknown.
func ByKind(kind string) []Definition {
	load()
	switch kind {
	case KindTorrent, KindUsenet, KindGeneric:
	default:
		return nil
	}
	out := make([]Definition, 0)
	for _, def := range all {
		if def.Kind == kind {
			out = append(out, def)
		}
	}
	return out
}

// Lookup returns the definition with the given id.
func Lookup(id string) (Definition, bool) {
	load()
	def, ok := byID[strings.TrimSpace(id)]
	return def, ok
}

// Search returns definitions of kind whose id, name, or description contain q.
// A blank query returns the whole kind, in catalog order.
func Search(kind, q string) []Definition {
	defs := ByKind(kind)
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" || defs == nil {
		return defs
	}
	out := make([]Definition, 0)
	for _, def := range defs {
		if strings.Contains(strings.ToLower(def.ID), q) ||
			strings.Contains(strings.ToLower(def.Name), q) ||
			strings.Contains(strings.ToLower(def.Description), q) {
			out = append(out, def)
		}
	}
	return out
}
