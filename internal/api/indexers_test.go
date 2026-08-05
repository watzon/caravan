package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestIndexerCRUD(t *testing.T) {
	h, st, _, _ := newAcquisitionServer(t)
	ctx := context.Background()

	rec := do(t, h, http.MethodGet, "/api/v1/indexers", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Indexers []indexerJSON `json:"indexers"`
	}
	decodeBody(t, rec, &list)
	if len(list.Indexers) != 0 {
		t.Fatalf("indexers = %v, want none on a fresh database", list.Indexers)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/indexers",
		`{"name":"nyaa","url":"https://nyaa.example/api/","api_key":"index-secret","type":"torznab","categories":[5000]}`)
	wantStatus(t, rec, http.StatusCreated)
	if strings.Contains(rec.Body.String(), "index-secret") ||
		strings.Contains(rec.Body.String(), `"api_key"`) {
		t.Fatalf("indexer response leaked credential: %s", rec.Body.String())
	}
	var created indexerJSON
	decodeBody(t, rec, &created)
	if created.ID == 0 {
		t.Fatalf("created indexer = %+v, want an assigned id", created)
	}
	if created.URL != "https://nyaa.example/api" {
		t.Fatalf("url = %q, want the trailing slash trimmed", created.URL)
	}
	if !created.Enabled || !created.HasAPIKey {
		t.Fatalf("created indexer = %+v, want enabled with a stored-key flag", created)
	}
	if created.Type != core.IndexerTypeTorznab || len(created.Categories) != 1 {
		t.Fatalf("created indexer = %+v, want the submitted configuration", created)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/indexers", "")
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "index-secret") ||
		strings.Contains(rec.Body.String(), `"api_key"`) {
		t.Fatalf("indexer list leaked credential: %s", rec.Body.String())
	}
	decodeBody(t, rec, &list)
	if len(list.Indexers) != 1 || list.Indexers[0].Name != "nyaa" || !list.Indexers[0].HasAPIKey {
		t.Fatalf("indexers = %+v, want the one row with a stored-key flag", list.Indexers)
	}

	// An omitted key preserves the stored value.
	rec = do(t, h, http.MethodPut, "/api/v1/indexers/"+itoa(created.ID),
		`{"name":"nyaa","url":"https://nyaa.example/api","type":"newznab","enabled":false}`)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "index-secret") ||
		strings.Contains(rec.Body.String(), `"api_key"`) {
		t.Fatalf("indexer update leaked credential: %s", rec.Body.String())
	}
	var updated indexerJSON
	decodeBody(t, rec, &updated)
	if updated.ID != created.ID || !updated.HasAPIKey || updated.Type != core.IndexerTypeNewznab || updated.Enabled {
		t.Fatalf("updated indexer = %+v, want replacement fields and preserved key state", updated)
	}
	stored, err := st.GetIndexer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIndexer after omitted key: %v", err)
	}
	if stored.APIKey != "index-secret" {
		t.Fatalf("stored API key after omitted update = %q, want preserved secret", stored.APIKey)
	}

	// A non-empty key replaces the stored value.
	rec = do(t, h, http.MethodPut, "/api/v1/indexers/"+itoa(created.ID),
		`{"name":"nyaa","url":"https://nyaa.example/api","api_key":"index-secret-2","type":"newznab","enabled":false}`)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "index-secret-2") ||
		strings.Contains(rec.Body.String(), `"api_key"`) {
		t.Fatalf("indexer replacement leaked credential: %s", rec.Body.String())
	}
	var replaced indexerJSON
	decodeBody(t, rec, &replaced)
	if !replaced.HasAPIKey {
		t.Fatalf("replaced indexer = %+v, want has_api_key", replaced)
	}

	// A rejected update must not mutate the stored credential.
	rec = do(t, h, http.MethodPut, "/api/v1/indexers/"+itoa(created.ID),
		`{"name":"nyaa","url":"ftp://bad","api_key":"rejected-secret","type":"newznab"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	stored, err = st.GetIndexer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIndexer after rejected update: %v", err)
	}
	if stored.APIKey != "index-secret-2" {
		t.Fatalf("stored API key after rejected update = %q, want unchanged", stored.APIKey)
	}

	// An explicit empty key clears the stored value.
	rec = do(t, h, http.MethodPut, "/api/v1/indexers/"+itoa(created.ID),
		`{"name":"nyaa","url":"https://nyaa.example/api","api_key":"","type":"newznab","enabled":false}`)
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &updated)
	if updated.HasAPIKey {
		t.Fatalf("cleared indexer = %+v, want has_api_key false", updated)
	}
	stored, err = st.GetIndexer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIndexer after clear: %v", err)
	}
	if stored.APIKey != "" {
		t.Fatalf("stored API key after clear = %q, want empty", stored.APIKey)
	}

	// A disabled indexer keeps its configuration but drops out of search.
	enabled, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		t.Fatalf("ListEnabledIndexers: %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("enabled indexers = %+v, want none", enabled)
	}

	rec = do(t, h, http.MethodDelete, "/api/v1/indexers/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	all, err := st.ListIndexers(ctx)
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("indexers = %+v, want the row deleted", all)
	}
}

func TestIndexerRequestsAreValidated(t *testing.T) {
	h, st, _, _ := newAcquisitionServer(t)

	tests := []struct {
		name string
		body string
	}{
		{"no name", `{"url":"https://a.example","type":"torznab"}`},
		{"blank name", `{"name":"  ","url":"https://a.example","type":"torznab"}`},
		{"no url", `{"name":"a","type":"torznab"}`},
		{"url without scheme", `{"name":"a","url":"a.example","type":"torznab"}`},
		{"url with wrong scheme", `{"name":"a","url":"ftp://a.example","type":"torznab"}`},
		{"unknown type", `{"name":"a","url":"https://a.example","type":"rss"}`},
		{"missing type", `{"name":"a","url":"https://a.example"}`},
		{"negative category", `{"name":"a","url":"https://a.example","type":"torznab","categories":[-1]}`},
		{"malformed json", `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/indexers", tt.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}

	// Nothing was written by any of the rejected requests.
	all, err := st.ListIndexers(context.Background())
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("indexers = %+v, want no writes from rejected requests", all)
	}
}

// A duplicate name is a user mistake, not a server failure: indexers.name is
// unique, and without the pre-check the collision would surface as a 500.
func TestCreateIndexerRejectsDuplicateName(t *testing.T) {
	h, _, _, _ := newAcquisitionServer(t)

	body := `{"name":"dupe","url":"https://a.example","type":"torznab"}`
	rec := do(t, h, http.MethodPost, "/api/v1/indexers", body)
	wantStatus(t, rec, http.StatusCreated)

	rec = do(t, h, http.MethodPost, "/api/v1/indexers", body)
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)
}

func TestIndexerEndpointsReportMissingRows(t *testing.T) {
	h, _, _, _ := newAcquisitionServer(t)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"update missing", http.MethodPut, "/api/v1/indexers/99", `{"name":"a","url":"https://a.example","type":"torznab"}`, http.StatusNotFound},
		{"delete missing", http.MethodDelete, "/api/v1/indexers/99", "", http.StatusNotFound},
		{"test missing", http.MethodPost, "/api/v1/indexers/99/test", "", http.StatusNotFound},
		{"bad id", http.MethodDelete, "/api/v1/indexers/nope", "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, tt.method, tt.path, tt.body)
			wantStatus(t, rec, tt.want)
			wantErrorBody(t, rec)
		})
	}
}

func TestIndexerTest(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)

	good := addIndexer(t, st, fake, "good")
	broken := addIndexer(t, st, fake, "broken")
	fake.breaks("broken")

	rec := do(t, h, http.MethodPost, "/api/v1/indexers/"+itoa(good.ID)+"/test", "")
	wantStatus(t, rec, http.StatusOK)
	var body map[string]string
	decodeBody(t, rec, &body)
	if body["status"] != "ok" {
		t.Fatalf("body = %v, want status ok", body)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/indexers/"+itoa(broken.ID)+"/test", "")
	wantStatus(t, rec, http.StatusBadGateway)
	var failure errorResponse
	decodeBody(t, rec, &failure)
	// The indexer's own message has to survive: "it did not work" without a
	// reason cannot be acted on.
	if failure.Error == "" || !strings.Contains(failure.Error, "401") {
		t.Fatalf("error = %q, want the indexer's own failure in it", failure.Error)
	}
}

// TestIndexerCategories drives the endpoint the settings picker is built on:
// the configuration arrives in the body, unsaved, because the form needs the
// tree before the indexer exists to have an id.
func TestIndexerCategories(t *testing.T) {
	h, _, _, fake := newAcquisitionServer(t)

	fake.servesCategories("anime",
		core.IndexerCategory{ID: 5070, Name: "Anime", Subcats: []core.IndexerCategory{}},
		core.IndexerCategory{ID: 2020, Name: "Anime Movies", Subcats: []core.IndexerCategory{}},
	)

	body := fmt.Sprintf(`{"url":%q,"type":"torznab","api_key":"k"}`, fake.url("anime"))
	rec := do(t, h, http.MethodPost, "/api/v1/indexers/categories", body)
	wantStatus(t, rec, http.StatusOK)
	var got struct {
		Categories []core.IndexerCategory `json:"categories"`
	}
	decodeBody(t, rec, &got)
	if len(got.Categories) != 2 || got.Categories[0].ID != 5070 || got.Categories[1].ID != 2020 {
		t.Fatalf("categories = %+v, want the advertised tree", got.Categories)
	}

	// An indexer advertising no categories is an empty list, never null: the
	// picker distinguishes "none advertised" from "request failed".
	fake.servesCategories("bare")
	body = fmt.Sprintf(`{"url":%q,"type":"torznab"}`, fake.url("bare"))
	rec = do(t, h, http.MethodPost, "/api/v1/indexers/categories", body)
	wantStatus(t, rec, http.StatusOK)
	if s := rec.Body.String(); !strings.Contains(s, `"categories":[]`) {
		t.Fatalf("body = %s, want an empty categories array", s)
	}

	// The indexer's own failure must survive to the response, as on /test.
	fake.breaks("anime")
	body = fmt.Sprintf(`{"url":%q,"type":"torznab"}`, fake.url("anime"))
	rec = do(t, h, http.MethodPost, "/api/v1/indexers/categories", body)
	wantStatus(t, rec, http.StatusBadGateway)
	var failure errorResponse
	decodeBody(t, rec, &failure)
	if !strings.Contains(failure.Error, "401") {
		t.Fatalf("error = %q, want the indexer's own failure in it", failure.Error)
	}

	// A body that cannot configure a client is the user's mistake, not a
	// gateway failure.
	rec = do(t, h, http.MethodPost, "/api/v1/indexers/categories", `{"url":"","type":"torznab"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestIndexerCategoriesWithoutClientFactory(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/indexers/categories",
		`{"url":"https://a.example","type":"torznab"}`)
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantErrorBody(t, rec)
}

// Without a client factory the endpoint has to say the feature is not
// configured rather than pretend the indexer failed.
func TestIndexerTestWithoutClientFactory(t *testing.T) {
	h, st, _ := newTestServer(t)

	cfg := core.IndexerConfig{Name: "a", URL: "https://a.example", Type: core.IndexerTypeTorznab, Enabled: true}
	if err := st.UpsertIndexer(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/indexers/"+itoa(cfg.ID)+"/test", "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantErrorBody(t, rec)
}
