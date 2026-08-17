package cardigann

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// Dotted acronyms as written in metadata titles ("S.H.I.E.L.D."); release
// names on scraped sites drop the dots. Same shape as internal/parse's
// reAcronym, but flattening instead of preserving.
var reKeywordAcronym = regexp.MustCompile(`(?:\b[A-Za-z]\.){2,}`)

// sanitizeSearchKeywords rewrites free-text keywords for a scraped site.
// Remote Torznab servers tokenize queries themselves, but a scraped site
// matches the literal text, so punctuation that release names never carry
// has to go. Letters of any script, digits, hyphens, and dots between
// digits ("24.04") stay untouched.
func sanitizeSearchKeywords(q string) string {
	q = reKeywordAcronym.ReplaceAllStringFunc(q, func(acronym string) string {
		return strings.ReplaceAll(acronym, ".", "")
	})
	q = strings.ReplaceAll(q, "&", " and ")
	q = strings.NewReplacer("'", "", "’", "").Replace(q)
	q = strings.Map(func(r rune) rune {
		switch r {
		case ':', ',', '!', '?', '(', ')', '[', ']', '"':
			return ' '
		}
		return r
	}, q)
	return strings.Join(strings.Fields(q), " ")
}

// Client adapts a local definition engine to Caravan's IndexerClient contract.
type Client struct {
	cfg    core.IndexerConfig
	engine *Engine
	err    error
}

// Modes reports the search modes the underlying definition advertises.
func (c *Client) Modes() map[string]bool {
	if c == nil || c.engine == nil {
		return map[string]bool{"search": true}
	}
	return c.engine.def.Capabilities().Modes
}

// NewClient resolves cfg.DefinitionID and builds a local indexer client. Build
// errors are retained and returned by every operation because the application
// factory contract cannot itself return an error.
func NewClient(registry *Registry, cfg core.IndexerConfig, hc *http.Client) *Client {
	client := &Client{cfg: cfg}
	definition, ok := registry.Get(cfg.DefinitionID)
	if !ok {
		client.err = fmt.Errorf("unknown local indexer definition %q", cfg.DefinitionID)
		return client
	}
	engine, err := New(definition, Config{BaseURL: cfg.URL, Settings: cfg.Settings}, hc)
	if err != nil {
		client.err = err
		return client
	}
	client.engine = engine
	return client
}

func (c *Client) Search(ctx context.Context, q string, cats []int) ([]core.Release, error) {
	return c.search(ctx, Query{Keywords: sanitizeSearchKeywords(q), Categories: cats})
}

func (c *Client) SearchMovie(ctx context.Context, q string, cats []int) ([]core.Release, error) {
	return c.search(ctx, Query{Keywords: sanitizeSearchKeywords(q), Categories: cats})
}

func (c *Client) SearchTV(ctx context.Context, q string, season, episode int, cats []int) ([]core.Release, error) {
	keywords := sanitizeSearchKeywords(q)
	if season > 0 {
		keywords += fmt.Sprintf(" S%02d", season)
		if episode > 0 {
			keywords += fmt.Sprintf("E%02d", episode)
		}
	}
	return c.search(ctx, Query{
		Keywords: strings.TrimSpace(keywords), Season: season, Episode: episode, Categories: cats,
	})
}

func (c *Client) search(ctx context.Context, query Query) ([]core.Release, error) {
	if c.err != nil {
		return nil, c.err
	}
	releases, err := c.engine.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	for i := range releases {
		releases[i].IndexerID = c.cfg.ID
		releases[i].Indexer = c.cfg.Name
	}
	return releases, nil
}

// Test performs a real keyword-less tracker request. Local capabilities alone
// cannot prove that the tracker base, credentials, and extraction rules work.
func (c *Client) Test(ctx context.Context) error {
	_, err := c.search(ctx, Query{Categories: c.cfg.Categories})
	return err
}

func (c *Client) Categories(context.Context) ([]core.IndexerCategory, error) {
	if c.err != nil {
		return nil, c.err
	}
	categories := c.engine.def.Capabilities().Categories
	if categories == nil {
		categories = []core.IndexerCategory{}
	}
	return categories, nil
}
