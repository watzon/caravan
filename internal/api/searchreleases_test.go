package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The universal search is genuinely unfiltered: no requested categories means
// NO cat parameter reaches the indexer — not the indexer's configured list,
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
