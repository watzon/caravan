package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/store"
)

// fakeJellyfin is a Jellyfin server that answers /System/Info however the test
// needs it to, and records what it was asked.
type fakeJellyfin struct {
	*httptest.Server
	tokens []string
	paths  []string
}

func newFakeJellyfin(t *testing.T, status int, body string) *fakeJellyfin {
	t.Helper()
	f := &fakeJellyfin{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.tokens = append(f.tokens, r.Header.Get("X-Emby-Token"))
		f.paths = append(f.paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

const jellyfinInfoBody = `{"ServerName":"basement","Version":"10.9.11","Id":"3b0e2f3a"}`

func TestJellyfinConfigRoundTrip(t *testing.T) {
	h, st, _ := newTestServer(t)

	// A fresh database reports the handoff off rather than erroring.
	rec := do(t, h, "GET", "/api/v1/handoff/jellyfin", "")
	wantStatus(t, rec, 200)
	var cfg jellyfinJSON
	decodeBody(t, rec, &cfg)
	if cfg != (jellyfinJSON{}) {
		t.Fatalf("fresh config = %+v, want zero", cfg)
	}

	rec = do(t, h, "POST", "/api/v1/handoff/jellyfin",
		`{"url":"http://jellyfin.lan:8096/","api_key":"jelly-secret","enabled":true}`)
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), "jelly-secret") ||
		strings.Contains(rec.Body.String(), `"api_key"`) {
		t.Fatalf("Jellyfin response leaked credential: %s", rec.Body.String())
	}
	decodeBody(t, rec, &cfg)
	// The trailing slash is normalized away so the client never builds a "//"
	// path out of it.
	if cfg.URL != "http://jellyfin.lan:8096" || !cfg.HasAPIKey || !cfg.Enabled {
		t.Fatalf("saved config = %+v", cfg)
	}

	// It landed in the settings table under the keys the handoff service reads.
	ctx := context.Background()
	for key, want := range map[string]string{
		store.SettingJellyfinURL:     "http://jellyfin.lan:8096",
		store.SettingJellyfinAPIKey:  "jelly-secret",
		store.SettingJellyfinEnabled: "true",
	} {
		got, err := st.GetSetting(ctx, key)
		if err != nil {
			t.Fatalf("GetSetting %s: %v", key, err)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	rec = do(t, h, "GET", "/api/v1/handoff/jellyfin", "")
	wantStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), "jelly-secret") ||
		strings.Contains(rec.Body.String(), `"api_key"`) {
		t.Fatalf("Jellyfin GET leaked credential: %s", rec.Body.String())
	}
	decodeBody(t, rec, &cfg)
	if cfg.URL != "http://jellyfin.lan:8096" || !cfg.HasAPIKey || !cfg.Enabled {
		t.Fatalf("re-read config = %+v", cfg)
	}

	// An omitted key preserves the stored value.
	rec = do(t, h, "POST", "/api/v1/handoff/jellyfin",
		`{"url":"http://new-jellyfin:8096","enabled":false}`)
	wantStatus(t, rec, 200)
	decodeBody(t, rec, &cfg)
	if cfg.URL != "http://new-jellyfin:8096" || !cfg.HasAPIKey || cfg.Enabled {
		t.Fatalf("omitted-key config = %+v", cfg)
	}
	stored, err := st.GetSetting(ctx, store.SettingJellyfinAPIKey)
	if err != nil {
		t.Fatalf("GetSetting after omitted key: %v", err)
	}
	if stored != "jelly-secret" {
		t.Fatalf("stored API key after omitted key = %q, want preserved secret", stored)
	}

	// An explicit empty key clears the stored value.
	rec = do(t, h, "POST", "/api/v1/handoff/jellyfin",
		`{"url":"http://new-jellyfin:8096","api_key":"","enabled":false}`)
	wantStatus(t, rec, 200)
	decodeBody(t, rec, &cfg)
	if cfg.HasAPIKey {
		t.Fatalf("cleared config = %+v, want has_api_key false", cfg)
	}
	stored, err = st.GetSetting(ctx, store.SettingJellyfinAPIKey)
	if err != nil {
		t.Fatalf("GetSetting after clear: %v", err)
	}
	if stored != "" {
		t.Fatalf("stored API key after clear = %q, want empty", stored)
	}
}

func TestJellyfinConfigRejectsBadRequests(t *testing.T) {
	h, _, _ := newTestServer(t)

	cases := map[string]string{
		"not a URL":           `{"url":"jellyfin.lan","enabled":false}`,
		"unsupported scheme":  `{"url":"ftp://jellyfin.lan","enabled":false}`,
		"enabled with no URL": `{"url":"","enabled":true}`,
		"enabled with spaces": `{"url":"   ","enabled":true}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, "POST", "/api/v1/handoff/jellyfin", body)
			wantStatus(t, rec, 400)
			wantErrorBody(t, rec)
		})
	}
}

// Turning the handoff off must not require re-typing the server address, so a
// disabled configuration with no URL is legal.
func TestJellyfinConfigCanBeDisabled(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, "POST", "/api/v1/handoff/jellyfin",
		`{"url":"http://jellyfin.lan:8096","api_key":"secret","enabled":false}`)
	wantStatus(t, rec, 200)
	var cfg jellyfinJSON
	decodeBody(t, rec, &cfg)
	if cfg.Enabled {
		t.Fatalf("config = %+v, want disabled", cfg)
	}
}

func TestJellyfinTestConnectionReportsTheServer(t *testing.T) {
	fake := newFakeJellyfin(t, 200, jellyfinInfoBody)
	h, _, _ := newTestServer(t)

	rec := do(t, h, "POST", "/api/v1/handoff/jellyfin/test",
		`{"url":"`+fake.URL+`","api_key":"typed-in-the-form"}`)
	wantStatus(t, rec, 200)

	var result jellyfinTestJSON
	decodeBody(t, rec, &result)
	if result.ServerName != "basement" || result.Version != "10.9.11" {
		t.Fatalf("test result = %+v", result)
	}
	if len(fake.paths) != 1 || fake.paths[0] != "/System/Info" {
		t.Fatalf("paths = %v, want one /System/Info", fake.paths)
	}
	// The credential under test is the one in the body, not the stored one:
	// that is the whole point of testing before saving.
	if fake.tokens[0] != "typed-in-the-form" {
		t.Fatalf("token = %q", fake.tokens[0])
	}
}

// An empty body tests what is already saved, which is what the settings screen
// does after a save.
func TestJellyfinTestConnectionFallsBackToStoredCredentials(t *testing.T) {
	fake := newFakeJellyfin(t, 200, jellyfinInfoBody)
	h, st, _ := newTestServer(t)

	ctx := context.Background()
	if err := st.SetSetting(ctx, store.SettingJellyfinURL, fake.URL); err != nil {
		t.Fatalf("SetSetting url: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingJellyfinAPIKey, "stored"); err != nil {
		t.Fatalf("SetSetting key: %v", err)
	}

	rec := do(t, h, "POST", "/api/v1/handoff/jellyfin/test", `{}`)
	wantStatus(t, rec, 200)
	if fake.tokens[0] != "stored" {
		t.Fatalf("token = %q, want the stored key", fake.tokens[0])
	}
}

func TestJellyfinTestConnectionSurfacesTheServersReason(t *testing.T) {
	fake := newFakeJellyfin(t, http.StatusUnauthorized, "Access token is invalid or expired.")
	h, _, _ := newTestServer(t)

	rec := do(t, h, "POST", "/api/v1/handoff/jellyfin/test", `{"url":"`+fake.URL+`","api_key":"wrong"}`)
	// 502: the request was fine, the upstream refused it.
	wantStatus(t, rec, 502)
	var body errorResponse
	decodeBody(t, rec, &body)
	if body.Error == "" || body.Error == "jellyfin test failed: " {
		t.Fatalf("error = %q, want the server's own reason", body.Error)
	}
}

func TestJellyfinTestConnectionNeedsAURL(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, "POST", "/api/v1/handoff/jellyfin/test", `{}`)
	wantStatus(t, rec, 400)
	wantErrorBody(t, rec)
}
