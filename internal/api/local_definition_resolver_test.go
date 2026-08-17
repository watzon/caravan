package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecutableUserDefinitionIsConfigurableButNotAdvertised(t *testing.T) {
	lookup := func(id string) (LocalDefinitionSchema, bool) {
		if id != "user:fixture" {
			return LocalDefinitionSchema{}, false
		}
		return LocalDefinitionSchema{Settings: []string{"token"}}, true
	}
	h, st, _ := newTestServer(t, WithLocalDefinitions(lookup))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/indexers", strings.NewReader(`{
		"name":"Fixture",
		"url":"https://tracker.example",
		"type":"torznab",
		"definition_id":"user:fixture",
		"settings":{"token":"write-only-secret"},
		"enabled":false
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	stored, err := st.ListIndexers(request.Context())
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(stored) != 1 || stored[0].DefinitionID != "user:fixture" || stored[0].Settings["token"] != "write-only-secret" {
		t.Fatalf("stored = %+v", stored)
	}

	catalogRequest := httptest.NewRequest(http.MethodGet, "/api/v1/indexers/catalog", nil)
	catalogResponse := httptest.NewRecorder()
	h.ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK {
		t.Fatalf("catalog status = %d, body = %s", catalogResponse.Code, catalogResponse.Body.String())
	}
	if strings.Contains(catalogResponse.Body.String(), "user:fixture") {
		t.Fatalf("runtime user definition leaked into advertised catalog: %s", catalogResponse.Body.String())
	}
}

func TestManagedDefinitionTracksCurrentRuntimeWithoutExactPin(t *testing.T) {
	lookup := func(id string) (LocalDefinitionSchema, bool) {
		return LocalDefinitionSchema{Settings: []string{"token"}}, id == "managed:fixture"
	}
	h, st, _ := newTestServer(t, WithLocalDefinitions(lookup))
	created := do(t, h, http.MethodPost, "/api/v1/indexers", `{
		"name":"Managed fixture",
		"url":"https://tracker.example",
		"type":"torznab",
		"definition_id":"managed:fixture",
		"settings":{"token":"write-only-secret"},
		"enabled":false
	}`)
	wantStatus(t, created, http.StatusCreated)
	stored, err := st.GetIndexer(t.Context(), 1)
	if err != nil || stored.DefinitionID != "managed:fixture" || stored.DefinitionSource != "" || stored.DefinitionRevision != "" || stored.DefinitionDigest != "" {
		t.Fatalf("managed current definition = %+v, err=%v", stored, err)
	}
}

func TestChangingDefinitionDoesNotPreservePreviousDefinitionSettings(t *testing.T) {
	lookup := func(id string) (LocalDefinitionSchema, bool) {
		switch id {
		case "user:a", "user:b":
			return LocalDefinitionSchema{Settings: []string{"token"}}, true
		default:
			return LocalDefinitionSchema{}, false
		}
	}
	h, st, _ := newTestServer(t, WithLocalDefinitions(lookup))

	created := do(t, h, http.MethodPost, "/api/v1/indexers", `{
		"name":"Fixture",
		"url":"https://tracker.example",
		"type":"torznab",
		"definition_id":"user:a",
		"settings":{"token":"definition-a-secret"},
		"enabled":false
	}`)
	wantStatus(t, created, http.StatusCreated)

	updated := do(t, h, http.MethodPut, "/api/v1/indexers/1", `{
		"name":"Fixture",
		"url":"https://tracker.example",
		"type":"torznab",
		"definition_id":"user:b",
		"enabled":false
	}`)
	wantStatus(t, updated, http.StatusOK)

	stored, err := st.GetIndexer(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Settings) != 0 {
		t.Fatalf("definition B inherited definition A settings: %#v", stored.Settings)
	}
}
