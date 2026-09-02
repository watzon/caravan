package cardigann

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const challengeHTML = `<html><head><title>Just a moment...</title></head><body><div id="challenge-platform"></div></body></html>`

// challengedSite answers with a Cloudflare interstitial until the request
// carries the clearance cookie and the solver's user agent.
func challengedSite(t *testing.T, userAgent string) (*httptest.Server, *int) {
	t.Helper()
	challenges := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("cf_clearance")
		if cookie == nil || cookie.Value != "ok" || r.Header.Get("User-Agent") != userAgent {
			challenges++
			w.Header().Set("Server", "cloudflare")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, challengeHTML)
			return
		}
		switch r.URL.Path {
		case "/search":
			_, _ = io.WriteString(w, `<table><tr><td class="title">Cleared.Release.1080p</td><td><a class="download" href="/file.torrent">get</a></td></tr></table>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &challenges
}

func fakeFlareSolverr(t *testing.T, userAgent string, solves *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			_, _ = io.WriteString(w, `{"msg":"FlareSolverr is ready!","version":"3.3.21"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1":
			var command map[string]any
			if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
				t.Errorf("decode solver command: %v", err)
			}
			if command["cmd"] != "request.get" {
				t.Errorf("cmd = %v, want request.get", command["cmd"])
			}
			*solves++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"solution": map[string]any{
					"url": command["url"], "status": 200, "response": "<html>solved</html>",
					"cookies":   []map[string]any{{"name": "cf_clearance", "value": "ok", "path": "/"}},
					"userAgent": userAgent,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func challengedDefinition(t *testing.T, base string) *Definition {
	t.Helper()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: challenged-fixture
name: Challenged Fixture
links: [%s]
settings:
  - {name: info_flaresolverr, type: info_flaresolverr, label: FlareSolverr}
caps: {modes: {search: [q]}}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {selector: .title}
    download: {selector: .download, attribute: href}
`, base)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if !definition.RequiresFlareSolverr() {
		t.Fatal("definition should require FlareSolverr")
	}
	return definition
}

func TestEnginePassesBrowserChallengeThroughFlareSolverr(t *testing.T) {
	const userAgent = "Mozilla/5.0 (solver)"
	site, challenges := challengedSite(t, userAgent)
	solves := 0
	solverServer := fakeFlareSolverr(t, userAgent, &solves)
	solver, err := NewFlareSolverr(solverServer.URL+"/v1/", solverServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if version, pingErr := solver.Ping(context.Background()); pingErr != nil || version != "3.3.21" {
		t.Fatalf("Ping = %q, %v", version, pingErr)
	}
	engine, err := New(challengedDefinition(t, site.URL), Config{FlareSolverr: solver}, site.Client())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		results, searchErr := engine.Search(context.Background(), Query{Keywords: "cleared"})
		if searchErr != nil || len(results) != 1 || results[0].Title != "Cleared.Release.1080p" {
			t.Fatalf("Search %d = %+v, %v", i, results, searchErr)
		}
	}
	if solves != 1 || *challenges != 1 {
		t.Fatalf("solves=%d challenges=%d, want one solve and one challenge before the cookies stick", solves, *challenges)
	}
}

func TestEngineReportsBrowserChallengeWithoutFlareSolverr(t *testing.T) {
	site, _ := challengedSite(t, "unused")
	engine, err := New(challengedDefinition(t, site.URL), Config{}, site.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, searchErr := engine.Search(context.Background(), Query{Keywords: "cleared"})
	if searchErr == nil || !strings.Contains(searchErr.Error(), ErrBrowserChallenge.Error()) {
		t.Fatalf("Search error = %v, want the browser challenge guidance", searchErr)
	}
}

func TestEnginePassesOrdinaryForbiddenThrough(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "<html>banned</html>")
	}))
	defer site.Close()
	solves := 0
	solverServer := fakeFlareSolverr(t, "ua", &solves)
	solver, _ := NewFlareSolverr(solverServer.URL, solverServer.Client())
	engine, err := New(challengedDefinition(t, site.URL), Config{FlareSolverr: solver}, site.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, searchErr := engine.Search(context.Background(), Query{})
	if searchErr == nil || !strings.Contains(searchErr.Error(), "HTTP 403") || solves != 0 {
		t.Fatalf("Search error = %v (solves=%d), want plain HTTP 403 without a solve", searchErr, solves)
	}
}

func TestNewFlareSolverrRejectsBadEndpoints(t *testing.T) {
	for _, raw := range []string{"", "ftp://solver", "http://user:pass@solver:8191", "solver:8191"} {
		if _, err := NewFlareSolverr(raw, nil); err == nil {
			t.Errorf("NewFlareSolverr(%q) accepted an invalid endpoint", raw)
		}
	}
	solver, err := NewFlareSolverr("http://solver:8191/v1", nil)
	if err != nil || solver.Endpoint() != "http://solver:8191" {
		t.Fatalf("endpoint = %q, %v", solver.Endpoint(), err)
	}
}

func formLoginSite(t *testing.T, showCaptcha bool) (*httptest.Server, *int, *int) {
	t.Helper()
	logins, searches := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			if r.Method == http.MethodPost {
				logins++
				_ = r.ParseForm()
				if r.Form.Get("username") != "user" || r.Form.Get("password") != "pass" {
					_, _ = io.WriteString(w, `<div class="error">bad credentials</div>`)
					return
				}
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "logged-in", Path: "/"})
				_, _ = io.WriteString(w, `<html>ok</html>`)
				return
			}
			captcha := ""
			if showCaptcha {
				captcha = `<img id="captcha" src="/captcha.png"><input name="imagestring">`
			}
			_, _ = io.WriteString(w, `<form method="post" action="/login"><input name="username"><input name="password">`+captcha+`</form>`)
		case "/search":
			searches++
			cookie, _ := r.Cookie("session")
			if cookie == nil || cookie.Value != "logged-in" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, `<table><tr><td class="title">Private.Release</td><td><a class="download" href="/file.torrent">get</a></td></tr></table>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &logins, &searches
}

func formLoginDefinition(t *testing.T, base string) *Definition {
	t.Helper()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: captcha-login-fixture
name: Captcha Login Fixture
links: [%s]
settings:
  - {name: username, type: text, label: Username}
  - {name: password, type: password, label: Password}
caps: {modes: {search: [q]}}
login:
  path: login
  form: form
  captcha: {type: image, selector: img#captcha, input: imagestring}
  inputs:
    username: "{{ .Config.username }}"
    password: "{{ .Config.password }}"
  error:
    - selector: div.error
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {selector: .title}
    download: {selector: .download, attribute: href}
`, base)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	return definition
}

func TestLoginWithoutMethodDefaultsToForm(t *testing.T) {
	site, logins, _ := formLoginSite(t, false)
	engine, err := New(formLoginDefinition(t, site.URL), Config{Settings: map[string]string{"username": "user", "password": "pass"}}, site.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, searchErr := engine.Search(context.Background(), Query{})
	if searchErr != nil || len(results) != 1 || *logins != 1 {
		t.Fatalf("Search = %+v, %v (logins=%d)", results, searchErr, *logins)
	}
}

func TestFormLoginReportsCaptchaAndSuggestsSessionCookie(t *testing.T) {
	site, logins, _ := formLoginSite(t, true)
	engine, err := New(formLoginDefinition(t, site.URL), Config{Settings: map[string]string{"username": "user", "password": "pass"}}, site.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, searchErr := engine.Search(context.Background(), Query{})
	if searchErr == nil || !strings.Contains(searchErr.Error(), errLoginCaptcha.Error()) || *logins != 0 {
		t.Fatalf("Search error = %v (logins=%d), want captcha guidance without a submitted form", searchErr, *logins)
	}
}

func TestSessionCookieSettingReplacesFormLogin(t *testing.T) {
	site, logins, searches := formLoginSite(t, true)
	definition := formLoginDefinition(t, site.URL)
	names := map[string]bool{}
	for _, schema := range definition.SettingSchemas() {
		names[schema.Name] = schema.Secret
	}
	if secret, ok := names[sessionCookieSetting]; !ok || !secret {
		t.Fatalf("session cookie setting missing or not secret: %v", names)
	}
	engine, err := New(definition, Config{Settings: map[string]string{sessionCookieSetting: "session=logged-in"}}, site.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, searchErr := engine.Search(context.Background(), Query{})
	if searchErr != nil || len(results) != 1 || *logins != 0 || *searches != 1 {
		t.Fatalf("Search = %+v, %v (logins=%d searches=%d)", results, searchErr, *logins, *searches)
	}
}

func TestCookieLoginDefinitionsDoNotGetASecondCookieSetting(t *testing.T) {
	definition, err := ParseDefinition([]byte(`
id: cookie-only
name: Cookie Only
links: [https://tracker.example]
settings:
  - {name: cookie, type: password, label: Cookie}
login:
  method: cookie
  inputs: {cookie: "{{ .Config.cookie }}"}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	for _, setting := range definition.Settings {
		if setting.Name == sessionCookieSetting {
			t.Fatal("cookie-method definitions must not grow a session cookie setting")
		}
	}
}
