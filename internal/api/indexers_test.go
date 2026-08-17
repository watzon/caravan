package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/indexer"
	"github.com/watzon/caravan/internal/store"
)

func TestIndexerCRUD(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
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

	body := fmt.Sprintf(`{"name":"nyaa","url":%q,"api_key":"index-secret","type":"torznab","categories":[5000],"priority":5}`, fake.url("nyaa"))
	rec = do(t, h, http.MethodPost, "/api/v1/indexers", body)
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
	if created.URL != strings.TrimRight(fake.url("nyaa"), "/") {
		t.Fatalf("url = %q, want the fake indexer address", created.URL)
	}
	if !created.Enabled || !created.HasAPIKey {
		t.Fatalf("created indexer = %+v, want enabled with a stored-key flag", created)
	}
	if created.Type != core.IndexerTypeTorznab || len(created.Categories) != 1 || created.Priority != 5 {
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
	if updated.ID != created.ID || !updated.HasAPIKey || updated.Type != core.IndexerTypeNewznab || updated.Enabled ||
		updated.Priority != core.IndexerDefaultPriority {
		t.Fatalf("updated indexer = %+v, want replacement fields, default priority, and preserved key state", updated)
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

func TestLocalIndexerDefinitionSettingsAreStoredWriteOnly(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	body := fmt.Sprintf(`{"name":"local tpb","definition_id":"thepiratebay","settings":{"apiurl":"https://adapter.example"},"url":%q,"type":"torznab","enabled":true}`, fake.url("tpb"))
	rec := do(t, h, http.MethodPost, "/api/v1/indexers", body)
	wantStatus(t, rec, http.StatusCreated)
	if strings.Contains(rec.Body.String(), "adapter.example") || strings.Contains(rec.Body.String(), `"settings"`) {
		t.Fatalf("local indexer response leaked settings: %s", rec.Body.String())
	}
	var created indexerJSON
	decodeBody(t, rec, &created)
	if created.DefinitionID != "thepiratebay" || len(created.HasSettings) != 1 || created.HasSettings[0] != "apiurl" {
		t.Fatalf("created local indexer = %+v", created)
	}
	stored, err := st.GetIndexer(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetIndexer: %v", err)
	}
	if stored.DefinitionID != "thepiratebay" || stored.Settings["apiurl"] != "https://adapter.example" {
		t.Fatalf("stored local indexer = %+v", stored)
	}

	// Omitted settings preserve write-only values during an edit.
	rec = do(t, h, http.MethodPut, "/api/v1/indexers/"+itoa(created.ID),
		fmt.Sprintf(`{"name":"local tpb","definition_id":"thepiratebay","url":%q,"type":"torznab","enabled":false}`, fake.url("tpb")))
	wantStatus(t, rec, http.StatusOK)
	stored, err = st.GetIndexer(context.Background(), created.ID)
	if err != nil || stored.Settings["apiurl"] != "https://adapter.example" {
		t.Fatalf("settings after omitted update = %+v, err=%v", stored, err)
	}

	// A settings object merges over the stored values: keys the request does
	// not mention stay, mentioned keys are replaced. The edit form relies on
	// this because stored values are write-only and cannot be resent.
	rec = do(t, h, http.MethodPut, "/api/v1/indexers/"+itoa(created.ID),
		fmt.Sprintf(`{"name":"local tpb","definition_id":"thepiratebay","settings":{},"url":%q,"type":"torznab","enabled":false}`, fake.url("tpb")))
	wantStatus(t, rec, http.StatusOK)
	stored, err = st.GetIndexer(context.Background(), created.ID)
	if err != nil || stored.Settings["apiurl"] != "https://adapter.example" {
		t.Fatalf("settings after empty-object update = %+v, err=%v", stored, err)
	}
	rec = do(t, h, http.MethodPut, "/api/v1/indexers/"+itoa(created.ID),
		fmt.Sprintf(`{"name":"local tpb","definition_id":"thepiratebay","settings":{"apiurl":"https://rotated.example"},"url":%q,"type":"torznab","enabled":false}`, fake.url("tpb")))
	wantStatus(t, rec, http.StatusOK)
	stored, err = st.GetIndexer(context.Background(), created.ID)
	if err != nil || stored.Settings["apiurl"] != "https://rotated.example" {
		t.Fatalf("settings after value update = %+v, err=%v", stored, err)
	}
}

func TestStoredLocalIndexerHasScopedTorznabFeed(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	cfg := addIndexer(t, st, fake, "local-feed")
	cfg.DefinitionID = "test-definition"
	if err := st.UpsertIndexer(context.Background(), &cfg); err != nil {
		t.Fatalf("store local indexer: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/indexers/"+itoa(cfg.ID)+"/feed?t=caps", "")
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "<caps>") {
		t.Fatalf("caps body = %s", rec.Body.String())
	}

	setPassword(t, st, testPassword)
	if err := st.SetSetting(context.Background(), store.SettingAPIKey, "feed-test-key"); err != nil {
		t.Fatalf("set API key: %v", err)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/indexers/"+itoa(cfg.ID)+"/feed?t=caps", "")
	wantStatus(t, rec, http.StatusUnauthorized)
	rec = do(t, h, http.MethodGet, "/api/v1/indexers/"+itoa(cfg.ID)+"/feed?t=caps&apikey=feed-test-key", "")
	wantStatus(t, rec, http.StatusOK)
}

func TestCreateDisabledLocalIndexerRejectsUnknownDefinitionID(t *testing.T) {
	h, st, _, _ := newAcquisitionServer(t)
	rec := do(t, h, http.MethodPost, "/api/v1/indexers", `{
		"name":"unknown local",
		"definition_id":"not-in-the-registry",
		"url":"https://example.com",
		"type":"torznab",
		"enabled":false
	}`)
	wantStatus(t, rec, http.StatusBadRequest)
	indexers, err := st.ListIndexers(context.Background())
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(indexers) != 0 {
		t.Fatalf("stored indexers = %+v, want none", indexers)
	}
}

func TestLocalIndexerRejectsUnknownSetting(t *testing.T) {
	h, st, _, _ := newAcquisitionServer(t)
	rec := do(t, h, http.MethodPost, "/api/v1/indexers", `{
		"name":"local with undeclared setting",
		"definition_id":"nyaa",
		"settings":{"authorization":"secret-value"},
		"url":"https://nyaa.si",
		"type":"torznab",
		"enabled":false
	}`)
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "unknown local indexer setting") {
		t.Fatalf("response = %s, want a non-secret unknown-setting error", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-value") {
		t.Fatalf("response leaked rejected setting value: %s", rec.Body.String())
	}
	indexers, err := st.ListIndexers(context.Background())
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(indexers) != 0 {
		t.Fatalf("indexers = %+v, want rejected configuration not stored", indexers)
	}
}

func TestLocalIndexerRejectsAmbiguousAndOversizeSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]string
		problem  string
	}{
		{
			name:     "canonical duplicate",
			settings: map[string]string{"apiurl": "https://one.example", " apiurl ": "https://two.example"},
			problem:  "duplicate local indexer setting",
		},
		{
			name:     "oversize value",
			settings: map[string]string{"apiurl": strings.Repeat("x", maxIndexerSettingValueBytes+1)},
			problem:  "local indexer setting is too large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := indexerRequest{
				Name: "local", DefinitionID: "thepiratebay", Settings: &tt.settings,
				URL: "https://thepiratebay.org", Type: core.IndexerTypeTorznab,
			}
			_, problem := body.config("")
			if problem != tt.problem {
				t.Fatalf("problem = %q, want %q", problem, tt.problem)
			}
		})
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
		{"url with credentials", `{"name":"a","url":"https://alice:do-not-leak@a.example","type":"torznab"}`},
		{"unknown type", `{"name":"a","url":"https://a.example","type":"rss"}`},
		{"missing type", `{"name":"a","url":"https://a.example"}`},
		{"negative category", `{"name":"a","url":"https://a.example","type":"torznab","categories":[-1]}`},
		{"negative priority", `{"name":"a","url":"https://a.example","type":"torznab","priority":-1}`},
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
	h, _, _, fake := newAcquisitionServer(t)

	body := fmt.Sprintf(`{"name":"dupe","url":%q,"type":"torznab"}`, fake.url("dupe"))
	rec := do(t, h, http.MethodPost, "/api/v1/indexers", body)
	wantStatus(t, rec, http.StatusCreated)

	rec = do(t, h, http.MethodPost, "/api/v1/indexers", body)
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)
}

func TestCreateIndexerRefusesWhenUnreachable(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	fake.breaks("down")

	rec := do(t, h, http.MethodPost, "/api/v1/indexers",
		fmt.Sprintf(`{"name":"down","url":%q,"type":"torznab"}`, fake.url("down")))
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)

	all, err := st.ListIndexers(context.Background())
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("indexers = %+v, want nothing written when the probe fails", all)
	}
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
		{"categories missing", http.MethodGet, "/api/v1/indexers/99/categories", "", http.StatusNotFound},
		{"bad id", http.MethodDelete, "/api/v1/indexers/nope", "", http.StatusBadRequest},
		{"categories bad id", http.MethodGet, "/api/v1/indexers/nope/categories", "", http.StatusBadRequest},
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

func TestStoredIndexerCategoriesUsesStoredAPIKey(t *testing.T) {
	const apiKey = "stored-indexer-key"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "caps" {
			http.Error(w, "expected caps request", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("apikey") != apiKey {
			http.Error(w, "bad api key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<caps><searching><search available="yes"/></searching><categories><category id="5000" name="TV"><subcat id="5030" name="TV/SD"/><subcat id="5040" name="TV/HD"/></category></categories></caps>`))
	}))
	t.Cleanup(upstream.Close)

	h, st, _ := newTestServer(t, WithIndexerClients(func(cfg core.IndexerConfig) IndexerClient {
		return indexer.New(cfg, upstream.Client())
	}))
	cfg := core.IndexerConfig{
		Name:    "protected",
		URL:     upstream.URL,
		APIKey:  apiKey,
		Type:    core.IndexerTypeTorznab,
		Enabled: true,
	}
	if err := st.UpsertIndexer(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/indexers/"+itoa(cfg.ID)+"/categories", "")
	wantStatus(t, rec, http.StatusOK)

	var body struct {
		Categories []core.IndexerCategory `json:"categories"`
	}
	decodeBody(t, rec, &body)
	if len(body.Categories) != 1 ||
		body.Categories[0].ID != 5000 ||
		body.Categories[0].Name != "TV" ||
		len(body.Categories[0].Subcats) != 2 ||
		body.Categories[0].Subcats[0].ID != 5030 ||
		body.Categories[0].Subcats[0].Name != "TV/SD" ||
		body.Categories[0].Subcats[1].ID != 5040 ||
		body.Categories[0].Subcats[1].Name != "TV/HD" {
		t.Fatalf("categories = %+v, want the protected indexer's advertised tree", body.Categories)
	}
}

func TestIndexerCategoriesUsesSuppliedAPIKey(t *testing.T) {
	const apiKey = "unsaved-indexer-key"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != "caps" {
			http.Error(w, "expected caps request", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("apikey") != apiKey {
			http.Error(w, "bad api key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<caps><searching><search available="yes"/></searching><categories><category id="5070" name="Anime"/></categories></caps>`))
	}))
	t.Cleanup(upstream.Close)

	h, _, _ := newTestServer(t, WithIndexerClients(func(cfg core.IndexerConfig) IndexerClient {
		return indexer.New(cfg, upstream.Client())
	}))
	rec := do(t, h, http.MethodPost, "/api/v1/indexers/categories",
		fmt.Sprintf(`{"url":%q,"type":"torznab","api_key":%q}`, upstream.URL, apiKey))
	wantStatus(t, rec, http.StatusOK)

	var body struct {
		Categories []core.IndexerCategory `json:"categories"`
	}
	decodeBody(t, rec, &body)
	if len(body.Categories) != 1 || body.Categories[0].ID != 5070 {
		t.Fatalf("categories = %+v, want the protected indexer's advertised tree", body.Categories)
	}
}

func TestIndexerCatalogListsPresetsAndFiltersByKind(t *testing.T) {
	h, _, _, _ := newAcquisitionServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/indexers/catalog", "")
	wantStatus(t, rec, http.StatusOK)
	var all struct {
		Definitions []struct {
			ID           string `json:"id"`
			DefinitionID string `json:"definition_id"`
			Kind         string `json:"kind"`
			Protocol     string `json:"protocol"`
			Name         string `json:"name"`
			Settings     []any  `json:"settings"`
		} `json:"definitions"`
	}
	decodeBody(t, rec, &all)
	if len(all.Definitions) < 20 {
		t.Fatalf("catalog = %d definitions, want the curated native/generic list", len(all.Definitions))
	}
	found := map[string]bool{}
	for _, def := range all.Definitions {
		found[def.ID] = true
		if def.ID == "thepiratebay" && (def.DefinitionID != "thepiratebay" || len(def.Settings) != 1) {
			t.Fatalf("local Pirate Bay definition = %+v", def)
		}
	}
	for _, id := range []string{"nzbgeek", "jackett", "generic-torznab", "animetosho", "thepiratebay"} {
		if !found[id] {
			t.Fatalf("catalog missing %q", id)
		}
	}
	if found["1337x"] {
		t.Fatal("catalog exposed homepage-only 1337x as an addable Torznab preset")
	}

	rec = do(t, h, http.MethodGet, "/api/v1/indexers/catalog?kind=usenet&q=geek", "")
	wantStatus(t, rec, http.StatusOK)
	var usenet struct {
		Definitions []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			URL  string `json:"url"`
		} `json:"definitions"`
	}
	decodeBody(t, rec, &usenet)
	if len(usenet.Definitions) == 0 || usenet.Definitions[0].ID != "nzbgeek" {
		t.Fatalf("usenet geek search = %+v, want nzbgeek", usenet.Definitions)
	}
	if usenet.Definitions[0].URL != "https://api.nzbgeek.info" {
		t.Fatalf("nzbgeek url = %q", usenet.Definitions[0].URL)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/indexers/catalog?kind=nope", "")
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
