package api

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// searchReleasesURL builds the endpoint with q escaped, so an expression's
// quotes, colons and minus signs survive the query string intact.
func searchReleasesURL(expression string) string {
	return "/api/v1/search/releases?q=" + url.QueryEscape(expression)
}

// The universal search is genuinely unfiltered: no requested categories means
// no cat parameter reaches the indexer, not the indexer's configured list,
// which is what the per-item fan-out sends. Rows come back scored against the
// store default profile and cached, so a grab is a lookup by id.
func TestUniversalSearchIsUnfilteredAndCached(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	addIndexer(t, st, fake, "alpha", 2000, 5000)
	fake.serve("alpha", torrentRelease("Ubuntu.22.04.ISO", "guid-u1", 40,
		core.ParsedRelease{Title: "Ubuntu", Quality: core.Quality1080p}))

	rec := do(t, h, http.MethodGet, "/api/v1/search/releases?q=ubuntu+iso", "")
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)

	if body.Query != "ubuntu iso" || len(body.Releases) != 1 {
		t.Fatalf("response = %+v, want the one release under the raw query", body)
	}
	searches := fake.recorded()
	if len(searches) != 1 || searches[0].cats != "" {
		t.Fatalf("searches = %+v, want one genuinely unfiltered query", searches)
	}
	if body.Releases[0].ID == 0 {
		t.Errorf("release row id = 0, want the cached row a grab can name")
	}
	if _, err := st.GetRelease(context.Background(), body.Releases[0].ID); err != nil {
		t.Errorf("cached release unreadable: %v", err)
	}
}

// Picked indexers and categories narrow the fan-out: only the named indexer
// is asked, and it is asked for exactly the requested categories rather than
// its configured ones. Unknown indexer ids are a stale client cache, dropped
// silently.
func TestUniversalSearchNarrowsIndexersAndCategories(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	addIndexer(t, st, fake, "alpha", 2000)
	beta := addIndexer(t, st, fake, "beta", 2000)
	fake.serve("beta", torrentRelease("Some.Movie.2024", "guid-b1", 10,
		core.ParsedRelease{Title: "Some Movie", Quality: core.Quality1080p}))

	rec := do(t, h, http.MethodGet,
		"/api/v1/search/releases?q=movie&cats=2010,2040&indexer_ids="+itoa(beta.ID)+",99999", "")
	wantStatus(t, rec, http.StatusOK)

	searches := fake.recorded()
	if len(searches) != 1 || searches[0].name != "beta" || searches[0].cats != "2010,2040" {
		t.Fatalf("searches = %+v, want only beta with the requested categories", searches)
	}
}

// A failing indexer costs its own rows and nothing else: partial results with
// a named failure, exactly the per-item picker's contract.
func TestUniversalSearchReportsPartialFailures(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	addIndexer(t, st, fake, "alpha", 2000)
	addIndexer(t, st, fake, "broken", 2000)
	fake.serve("alpha", torrentRelease("Fine.Release", "guid-f1", 5,
		core.ParsedRelease{Title: "Fine", Quality: core.Quality1080p}))
	fake.breaks("broken")

	rec := do(t, h, http.MethodGet, "/api/v1/search/releases?q=fine", "")
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)
	if len(body.Releases) != 1 || len(body.Errors) != 1 || body.Errors[0].Indexer != "broken" {
		t.Fatalf("response = %+v, want one release and the broken indexer named", body)
	}
}

// Bad parameters are refused at the edge.
func TestUniversalSearchValidatesParams(t *testing.T) {
	h, _, _ := newTestServer(t)
	for name, url := range map[string]string{
		"missing q":    "/api/v1/search/releases",
		"bad cats":     "/api/v1/search/releases?q=x&cats=0",
		"bad indexers": "/api/v1/search/releases?q=x&indexer_ids=-1",
		"bad limit":    "/api/v1/search/releases?q=x&limit=0",
		"bad library":  "/api/v1/search/releases?q=x&library_id=nope",
	} {
		rec := do(t, h, http.MethodGet, url, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
}

// The two halves of an expression go to the two places they can be enforced:
// the keyword is what the indexers are asked for, and the field term filters
// what they answered. A hidden row is counted, and cached anyway.
func TestUniversalSearchSendsKeywordsAndFiltersLocally(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	addIndexer(t, st, fake, "alpha")
	fake.serve("alpha",
		torrentRelease("Foo.2024.1080p.WEB-DL", "keep", 20,
			core.ParsedRelease{Title: "Foo", Quality: core.Quality1080p}),
		torrentRelease("Foo.2024.720p.WEB-DL", "hidden", 90,
			core.ParsedRelease{Title: "Foo", Quality: core.Quality720p}),
	)

	rec := do(t, h, http.MethodGet, searchReleasesURL("foo quality:1080p"), "")
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)

	// Only the keyword is searchable, so it is the whole upstream query.
	searches := fake.recorded()
	if len(searches) != 1 || searches[0].query != "foo" {
		t.Fatalf("searches = %+v, want the keyword alone upstream", searches)
	}
	if !slices.Equal(body.Queries, []string{"foo"}) {
		t.Errorf("queries = %v, want the upstream form", body.Queries)
	}
	if body.Query != "foo quality:1080p" {
		t.Errorf("query = %q, want the expression as typed", body.Query)
	}
	if len(body.Releases) != 1 || body.Releases[0].GUID != "keep" {
		t.Fatalf("releases = %+v, want only the 1080p row", titlesOf(body.Releases))
	}
	if body.Filtered != 1 {
		t.Errorf("filtered = %d, want the 720p row counted", body.Filtered)
	}
}

// A structurally broken expression is refused with the parser's own message,
// which names what to fix. It is a 400 and not an empty result list, because
// the query has no reading at all.
func TestUniversalSearchRejectsBrokenExpressionWithParserMessage(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, searchReleasesURL(`"unclosed`), "")
	wantStatus(t, rec, http.StatusBadRequest)
	if got := rec.Body.String(); !strings.Contains(got, "unclosed quote") {
		t.Fatalf("body = %q, want the parser's message verbatim", got)
	}
}

// An expression of nothing but filters has nothing to send upstream. Fanning
// it out would ask every indexer for the empty string.
func TestUniversalSearchRejectsFilterOnlyExpression(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	addIndexer(t, st, fake, "alpha")

	rec := do(t, h, http.MethodGet, searchReleasesURL("quality:1080p"), "")
	wantStatus(t, rec, http.StatusBadRequest)
	if got := rec.Body.String(); !strings.Contains(got, "at least one keyword") {
		t.Fatalf("body = %q, want the refusal to name what is missing", got)
	}
	if searches := fake.recorded(); len(searches) != 0 {
		t.Fatalf("searches = %+v, want nothing sent upstream", searches)
	}
}

// A title with a colon in it is a title, not a malformed field term: there is
// no `re:` field, so the whole token stays a keyword and reaches the indexer
// exactly as typed.
func TestUniversalSearchKeepsUnknownFieldPrefixAsAKeyword(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	addIndexer(t, st, fake, "alpha")

	rec := do(t, h, http.MethodGet, searchReleasesURL("Re:Zero"), "")
	wantStatus(t, rec, http.StatusOK)
	if searches := fake.recorded(); len(searches) != 1 || searches[0].query != "Re:Zero" {
		t.Fatalf("searches = %+v, want the title searched for literally", searches)
	}

	// The colon is left alone even while the rest of the expression is read as
	// an expression: the negation beside it is stripped from the query, the
	// title is not.
	rec = do(t, h, http.MethodGet, searchReleasesURL("Re:Zero -dub"), "")
	wantStatus(t, rec, http.StatusOK)
	if searches := fake.recorded(); len(searches) != 2 || searches[1].query != "Re:Zero" {
		t.Fatalf("searches = %+v, want the title alone upstream", searches)
	}
}

// A field expression fans out the same forms the movie picker does, so the
// seed a movie page hands the search box asks the indexers the same questions.
func TestUniversalSearchFansOutFieldExpressionLikeTheItemPicker(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	addIndexer(t, st, fake, "alpha")

	expression := `title:"Big Buck Bunny" year:2008`
	rec := do(t, h, http.MethodGet, searchReleasesURL(expression), "")
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)

	want := []string{"Big Buck Bunny 2008", "Big Buck Bunny"}
	got := []string{}
	for _, search := range fake.recorded() {
		got = append(got, search.query)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("searches = %v, want %v", got, want)
	}
	if !slices.Equal(body.Queries, want) {
		t.Errorf("queries = %v, want %v", body.Queries, want)
	}
	if body.Query != expression {
		t.Errorf("query = %q, want the expression as typed", body.Query)
	}
}

// A negation is never sent upstream (asking for what the user rejected) so the
// local test is the only one it gets, and it runs against the title.
func TestUniversalSearchHidesNegatedKeywordLocally(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	addIndexer(t, st, fake, "alpha")
	fake.serve("alpha",
		torrentRelease("Big.Buck.Bunny.2008.1080p", "keep", 20,
			core.ParsedRelease{Title: "Big Buck Bunny", Quality: core.Quality1080p}),
		torrentRelease("Big.Buck.Bunny.2008.NORDIC.1080p", "hidden", 90,
			core.ParsedRelease{Title: "Big Buck Bunny Nordic", Quality: core.Quality1080p}),
	)

	rec := do(t, h, http.MethodGet, searchReleasesURL("bunny -nordic"), "")
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)

	if searches := fake.recorded(); len(searches) != 1 || searches[0].query != "bunny" {
		t.Fatalf("searches = %+v, want the rejected word left out of the query", searches)
	}
	if len(body.Releases) != 1 || body.Releases[0].GUID != "keep" {
		t.Fatalf("releases = %+v, want the Nordic release hidden", titlesOf(body.Releases))
	}
	if body.Filtered != 1 {
		t.Errorf("filtered = %d, want the hidden row counted", body.Filtered)
	}
}
