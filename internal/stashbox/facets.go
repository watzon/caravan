package stashbox

// The scene filter rail's typeaheads: the performers and tags a scene query
// narrows by.
//
// They are a file of their own rather than part of scenes.go because they
// answer a different question, "who and what can I filter by", not "which
// scenes", and because each has two dialects behind it: the GraphQL road here
// and TPDB's REST twin in tpdb.go. The dispatch is the same one SearchScenes
// makes and for the same reason: on TPDB the ids that the scene index filters
// on are its own numeric ones, which GraphQL never serves.

import (
	"context"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// GraphQL operation names for the facet half of the protocol.
const (
	opSearchPerformers = "SearchPerformers"
	opSearchTags       = "SearchTags"
)

// performerFields is deliberately narrow, for the reason sceneFields is: a
// field this client does not ask for is a field a thinner dialect cannot fail
// on. A typeahead row draws a name and a face.
const performerFields = `
    id
    name
    images { url width height }
`

const searchPerformersQuery = `query ` + opSearchPerformers + `($input: PerformerQueryInput!) {
  queryPerformers(input: $input) {
    performers {` + performerFields + `}
  }
}`

const searchTagsQuery = `query ` + opSearchTags + `($input: TagQueryInput!) {
  queryTags(input: $input) {
    tags { id name }
  }
}`

// SearchPerformers returns performer candidates for a free-text query.
//
// The result carries whichever id the configured endpoint filters scenes by,
// TPDB's numeric one, a stash-box's uuid, and core.SceneFilterRef holds both,
// so a caller hands back what it was given without learning which dialect it is
// talking to.
func (c *Client) SearchPerformers(ctx context.Context, query string) ([]core.ScenePerformerMeta, error) {
	query = strings.TrimSpace(query)
	if c.restPerformers != "" {
		return c.searchPerformersByREST(ctx, query)
	}

	var resp struct {
		QueryPerformers struct {
			Performers []struct {
				ID     string        `json:"id"`
				Name   string        `json:"name"`
				Images []imageResult `json:"images"`
			} `json:"performers"`
		} `json:"queryPerformers"`
	}
	input := map[string]any{
		"page":     1,
		"per_page": defaultPerPage,
	}
	if query != "" {
		// `names` rather than `name`: it matches aliases too, and a performer
		// is as often typed by an alias as by the name the endpoint filed
		// them under.
		input["names"] = query
	}
	if err := c.query(ctx, opSearchPerformers, searchPerformersQuery, map[string]any{"input": input}, &resp); err != nil {
		return nil, err
	}

	out := make([]core.ScenePerformerMeta, 0, len(resp.QueryPerformers.Performers))
	for _, p := range resp.QueryPerformers.Performers {
		if p.ID == "" {
			continue
		}
		out = append(out, core.ScenePerformerMeta{
			SceneFilterRef: core.SceneFilterRef{StashID: p.ID, Name: p.Name},
			ImageURL:       coverURL(p.Images),
		})
	}
	return out, nil
}

// SearchTags returns tag candidates for a free-text query.
func (c *Client) SearchTags(ctx context.Context, query string) ([]core.SceneFilterRef, error) {
	query = strings.TrimSpace(query)
	if c.restTags != "" {
		return c.searchTagsByREST(ctx, query)
	}

	var resp struct {
		QueryTags struct {
			Tags []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"tags"`
		} `json:"queryTags"`
	}
	input := map[string]any{
		"page":     1,
		"per_page": defaultPerPage,
	}
	if query != "" {
		// A tag has one name and no aliases, so there is no `names` here.
		input["name"] = query
	}
	if err := c.query(ctx, opSearchTags, searchTagsQuery, map[string]any{"input": input}, &resp); err != nil {
		return nil, err
	}

	out := make([]core.SceneFilterRef, 0, len(resp.QueryTags.Tags))
	for _, t := range resp.QueryTags.Tags {
		if t.ID == "" {
			continue
		}
		out = append(out, core.SceneFilterRef{StashID: t.ID, Name: t.Name})
	}
	return out, nil
}
