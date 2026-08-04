package stash

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/stash/stashtest"
)

// newFake starts a fake Stash and returns a client pointed at it.
func newFake(t *testing.T, opts stashtest.Options) (*Client, *stashtest.Server) {
	t.Helper()
	srv := stashtest.New(opts)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL(), "test-key", srv.Client()), srv
}

// stashtest duplicates the credential header rather than importing it, so the
// two have to be proved equal somewhere.
func TestFakeEndpointAgreesOnTheCredentialHeader(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{
		RequireAPIKey: true,
		Operations: map[string][]stashtest.Response{
			"Version": {stashtest.Data(`{"version":{"version":"v0.28.1","hash":"abc1234"}}`)},
		},
	})
	if _, err := client.Version(context.Background()); err != nil {
		t.Fatalf("Version: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].APIKey != "test-key" {
		t.Fatalf("requests = %+v, want one carrying the ApiKey header", reqs)
	}
	if reqs[0].Path != GraphQLPath {
		t.Errorf("path = %q, want %q", reqs[0].Path, GraphQLPath)
	}
}

func TestVersionIdentifiesTheServer(t *testing.T) {
	client, _ := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"Version": {stashtest.Data(`{"version":{"version":"v0.28.1","hash":"abc1234","build_time":"x"}}`)},
		},
	})
	got, err := client.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got.Version != "v0.28.1" || got.Hash != "abc1234" {
		t.Errorf("Version = %+v, want v0.28.1/abc1234", got)
	}
}

// A wrong or missing API key is the failure the settings card exists to catch,
// and it has to arrive as ErrUnauthorized rather than as an opaque HTTP error.
func TestUnauthorizedIsASentinel(t *testing.T) {
	client, _ := newFake(t, stashtest.Options{RequireAPIKey: true})
	anon := NewClient(client.BaseURL, "", client.hc)

	_, err := anon.Version(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Version without a key = %v, want ErrUnauthorized", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Operation != "Version" {
		t.Errorf("error = %v, want an *APIError naming the operation", err)
	}
}

// A proxy in front of Stash answers with HTML. That must read as "the server is
// down", not as a decode failure.
func TestNonJSONFailureIsReportedAsTheServerFailing(t *testing.T) {
	client, _ := newFake(t, stashtest.Options{
		Fallback: &stashtest.Response{Status: http.StatusBadGateway, Body: []byte("<html>502</html>")},
	})
	_, err := client.Version(context.Background())
	if err == nil {
		t.Fatal("Version against a 502 = nil, want an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("error = %v, want an *APIError carrying 502", err)
	}
}

// The scan is the exposure-critical call: it must name the adult root and
// nothing else. A scan with no paths would walk every library Stash has
// configured, including the television one it should never see.
func TestScanIsScopedToTheGivenPaths(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"MetadataScan": {stashtest.Data(`{"metadataScan":"job-7"}`)},
		},
	})

	id, err := client.Scan(context.Background(), []string{"/srv/media/library/Adult"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if id != "job-7" {
		t.Errorf("job id = %q, want job-7", id)
	}

	reqs := srv.Operations("MetadataScan")
	if len(reqs) != 1 {
		t.Fatalf("MetadataScan requests = %d, want 1", len(reqs))
	}
	paths := scanPaths(t, reqs[0].Variables)
	if !reflect.DeepEqual(paths, []string{"/srv/media/library/Adult"}) {
		t.Errorf("scan paths = %v, want only the adult root", paths)
	}
	// Nothing else may be asked for: generation is Stash's decision, and phash
	// in particular is permanently Stash's job.
	input, _ := reqs[0].Variables["input"].(map[string]any)
	if len(input) != 1 {
		t.Errorf("ScanMetadataInput = %v, want only paths", input)
	}
}

func TestScanRefusesAnEmptyPathList(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{})
	if _, err := client.Scan(context.Background(), []string{"  "}); err == nil {
		t.Fatal("Scan with no usable path = nil, want an error")
	}
	if n := srv.Count(); n != 0 {
		t.Errorf("requests = %d, want 0: an unscoped scan must never be sent", n)
	}
}

func TestSceneByPathReturnsTheOneMatch(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath": {stashtest.Data(`{"findScenes":{"count":1,"scenes":[{"id":"42","title":"Deep Impact"}]}}`)},
		},
	})
	scene, err := client.SceneByPath(context.Background(), "/srv/media/library/Adult/Brazzers/x.mp4")
	if err != nil {
		t.Fatalf("SceneByPath: %v", err)
	}
	if scene.ID != "42" {
		t.Errorf("scene id = %q, want 42", scene.ID)
	}
	if got := srv.Operations("FindSceneByPath")[0].Variables["path"]; got != "/srv/media/library/Adult/Brazzers/x.mp4" {
		t.Errorf("path variable = %v", got)
	}
}

// The ordinary answer while a scan is still in flight. It has to be a sentinel,
// because the identity push retries on it and fails on everything else.
func TestSceneByPathReportsNotIndexedYet(t *testing.T) {
	client, _ := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath": {stashtest.Data(`{"findScenes":{"count":0,"scenes":[]}}`)},
		},
	})
	_, err := client.SceneByPath(context.Background(), "/x.mp4")
	if !errors.Is(err, ErrSceneNotFound) {
		t.Fatalf("SceneByPath with no match = %v, want ErrSceneNotFound", err)
	}
}

// Pushing identity onto the first of several matches would silently retitle the
// wrong scene, so two matches is a refusal rather than a guess.
func TestSceneByPathRefusesAnAmbiguousMatch(t *testing.T) {
	client, _ := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath": {stashtest.Data(`{"findScenes":{"count":2,"scenes":[{"id":"1"},{"id":"2"}]}}`)},
		},
	})
	_, err := client.SceneByPath(context.Background(), "/x.mp4")
	if !errors.Is(err, ErrAmbiguousScene) {
		t.Fatalf("SceneByPath with two matches = %v, want ErrAmbiguousScene", err)
	}
}

// The payload is the contract with Stash: a field named wrongly is a field
// silently dropped by a GraphQL server that validates inputs.
func TestUpdateScenePayload(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"SceneUpdate": {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})

	err := client.UpdateScene(context.Background(), SceneUpdate{
		ID:           "42",
		Title:        "Deep Impact",
		StashIDs:     []StashID{{Endpoint: "https://theporndb.net/graphql", StashID: "scene-a"}},
		StudioID:     "9",
		PerformerIDs: []string{"3", "4"},
		URLs:         []string{"https://example.test/scene-a"},
		Date:         "2022-03-14",
	})
	if err != nil {
		t.Fatalf("UpdateScene: %v", err)
	}

	input, _ := srv.Operations("SceneUpdate")[0].Variables["input"].(map[string]any)
	want := map[string]any{
		"id":            "42",
		"title":         "Deep Impact",
		"studio_id":     "9",
		"performer_ids": []any{"3", "4"},
		"urls":          []any{"https://example.test/scene-a"},
		"date":          "2022-03-14",
		"stash_ids": []any{map[string]any{
			"endpoint": "https://theporndb.net/graphql",
			"stash_id": "scene-a",
		}},
	}
	if !reflect.DeepEqual(input, want) {
		got, _ := json.Marshal(input)
		t.Errorf("SceneUpdateInput = %s, want %+v", got, want)
	}
}

// A best-effort field that could not be resolved must be absent, not empty: an
// empty studio_id would clear a studio the user set by hand.
func TestUpdateSceneOmitsUnresolvedFields(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"SceneUpdate": {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})
	if err := client.UpdateScene(context.Background(), SceneUpdate{ID: "42", Title: "Only A Title"}); err != nil {
		t.Fatalf("UpdateScene: %v", err)
	}
	input, _ := srv.Operations("SceneUpdate")[0].Variables["input"].(map[string]any)
	for _, key := range []string{"studio_id", "performer_ids", "stash_ids", "urls", "date"} {
		if _, present := input[key]; present {
			t.Errorf("SceneUpdateInput carries %q, want it omitted when unresolved", key)
		}
	}
}

func TestUpdateSceneNeedsAnID(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{})
	if err := client.UpdateScene(context.Background(), SceneUpdate{Title: "x"}); err == nil {
		t.Fatal("UpdateScene with no id = nil, want an error")
	}
	if n := srv.Count(); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestStudioLookupAndCreate(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindStudioByName": {
				stashtest.Data(`{"findStudios":{"studios":[]}}`),
				stashtest.Data(`{"findStudios":{"studios":[{"id":"9","name":"Brazzers"}]}}`),
			},
			"StudioCreate": {stashtest.Data(`{"studioCreate":{"id":"9"}}`)},
		},
	})
	ctx := context.Background()

	id, err := client.StudioByName(ctx, "Brazzers")
	if err != nil {
		t.Fatalf("StudioByName: %v", err)
	}
	if id != "" {
		t.Fatalf("StudioByName on an empty Stash = %q, want \"\"", id)
	}

	created, err := client.CreateStudio(ctx, "Brazzers", []StashID{{Endpoint: "https://tpdb.test/graphql", StashID: "site-1"}})
	if err != nil {
		t.Fatalf("CreateStudio: %v", err)
	}
	if created != "9" {
		t.Errorf("CreateStudio = %q, want 9", created)
	}
	input, _ := srv.Operations("StudioCreate")[0].Variables["input"].(map[string]any)
	want := map[string]any{
		"name": "Brazzers",
		"stash_ids": []any{map[string]any{
			"endpoint": "https://tpdb.test/graphql",
			"stash_id": "site-1",
		}},
	}
	if !reflect.DeepEqual(input, want) {
		t.Errorf("StudioCreateInput = %+v, want %+v", input, want)
	}

	// The second lookup finds it, which is what makes the push idempotent.
	again, err := client.StudioByName(ctx, "Brazzers")
	if err != nil {
		t.Fatalf("StudioByName again: %v", err)
	}
	if again != "9" {
		t.Errorf("StudioByName after create = %q, want 9", again)
	}
}

func TestPerformerLookupAndCreate(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindPerformerByName": {stashtest.Data(`{"findPerformers":{"performers":[]}}`)},
			"PerformerCreate":     {stashtest.Data(`{"performerCreate":{"id":"3"}}`)},
		},
	})
	ctx := context.Background()

	if id, err := client.PerformerByName(ctx, "Abella Danger"); err != nil || id != "" {
		t.Fatalf("PerformerByName = (%q, %v), want (\"\", nil)", id, err)
	}
	id, err := client.CreatePerformer(ctx, "Abella Danger")
	if err != nil {
		t.Fatalf("CreatePerformer: %v", err)
	}
	if id != "3" {
		t.Errorf("CreatePerformer = %q, want 3", id)
	}
	// Names and nothing else: Caravan stores credited names, not the provider's
	// performer ids, so there is no truthful stash id to attach.
	input, _ := srv.Operations("PerformerCreate")[0].Variables["input"].(map[string]any)
	if !reflect.DeepEqual(input, map[string]any{"name": "Abella Danger"}) {
		t.Errorf("PerformerCreateInput = %+v, want only a name", input)
	}
}

// A blank name is nothing to look up, and must not become a request.
func TestBlankNamesAreNotLookedUp(t *testing.T) {
	client, srv := newFake(t, stashtest.Options{})
	ctx := context.Background()
	if id, err := client.StudioByName(ctx, "  "); err != nil || id != "" {
		t.Errorf("StudioByName(blank) = (%q, %v)", id, err)
	}
	if id, err := client.PerformerByName(ctx, ""); err != nil || id != "" {
		t.Errorf("PerformerByName(blank) = (%q, %v)", id, err)
	}
	if n := srv.Count(); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

// The selection sets are the compatibility surface: a field added carelessly is
// a field an older Stash rejects the whole query for.
func TestSelectionSetsStayNarrow(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		banned    []string
	}{
		{"scene lookup", findSceneByPathDoc, []string{"stash_ids_endpoint", "o_counter", "url "}},
		{"scene update", sceneUpdateDoc, []string{"movies", "url:"}},
		{"studio lookup", findStudioDoc, []string{"stash_ids_endpoint", "stash_id_endpoint"}},
		{"performer lookup", findPerformerDoc, []string{"stash_ids_endpoint", "stash_id_endpoint"}},
	} {
		for _, banned := range tc.banned {
			if strings.Contains(tc.doc, banned) {
				t.Errorf("%s document mentions %q, which older Stash releases do not have", tc.name, banned)
			}
		}
	}
}

// scanPaths pulls ScanMetadataInput.paths out of a recorded request.
func scanPaths(t *testing.T, vars map[string]any) []string {
	t.Helper()
	input, ok := vars["input"].(map[string]any)
	if !ok {
		t.Fatalf("variables = %+v, want an input object", vars)
	}
	raw, ok := input["paths"].([]any)
	if !ok {
		t.Fatalf("input = %+v, want a paths array", input)
	}
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		s, _ := p.(string)
		out = append(out, s)
	}
	return out
}
