package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestIndexerProbeErrorDoesNotExposeConfigurationSecrets(t *testing.T) {
	apiSecret := strings.Repeat("api-key-", 2)
	cfg := core.IndexerConfig{
		URL:      "https://tracker.example/api?token=url-secret-marker",
		Settings: map[string]string{"password": "setting-secret-marker"},
	}
	cfg.APIKey = apiSecret
	probeErr := errors.New("HTTP 403 from " + cfg.URL + " using " + cfg.APIKey + " and " + cfg.Settings["password"])
	message := indexerProbeError(cfg, probeErr)
	for _, secret := range []string{"url-secret-marker", apiSecret, "setting-secret-marker"} {
		if strings.Contains(message, secret) {
			t.Fatalf("probe error exposed %q: %q", secret, message)
		}
	}
	if !strings.Contains(message, "website, not a Torznab or Newznab feed") {
		t.Fatalf("probe error = %q, want actionable feed guidance", message)
	}
}

func TestIndexerProbeErrorLocalAdapterForbiddenSkipsFeedGuidance(t *testing.T) {
	cfg := core.IndexerConfig{
		DefinitionID: "managed:torrentbyte",
		URL:          "https://torrentbyte.cc",
	}
	message := indexerProbeError(cfg, errors.New("tracker returned HTTP 403"))
	if strings.Contains(message, "Torznab or Newznab feed") {
		t.Fatalf("local adapter probe error gave feed guidance: %q", message)
	}
	if !strings.Contains(message, "HTTP 403") || !strings.Contains(message, "anti-bot") {
		t.Fatalf("probe error = %q, want the tracker refusal explained", message)
	}
}

func TestIndexerErrorRedactionHandlesOverlappingSecretsLongestFirst(t *testing.T) {
	cfg := core.IndexerConfig{
		APIKey:   "abc",
		Settings: map[string]string{"token": "abcdef"},
	}
	message := redactIndexerMessage(cfg, "tracker returned abcdef")
	if strings.Contains(message, "abc") || strings.Contains(message, "def") {
		t.Fatalf("overlapping secret was only partially redacted: %q", message)
	}
}
