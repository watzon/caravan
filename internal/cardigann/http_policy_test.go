package cardigann

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRestrictedHTTPClientRejectsPrivateTargetBeforeRoundTrip(t *testing.T) {
	called := false
	client := restrictHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	})})

	_, err := client.Get("http://127.0.0.1/private")
	if err == nil || !strings.Contains(err.Error(), "public network") {
		t.Fatalf("Get error = %q, want public-network rejection", err)
	}
	if called {
		t.Fatal("private target reached the underlying transport")
	}
}

func TestRestrictedHTTPClientRejectsRedirectToPrivateTarget(t *testing.T) {
	requests := 0
	client := restrictHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://169.254.169.254/latest/meta-data"}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
			Request:    r,
		}, nil
	})})

	_, err := client.Get("http://93.184.216.34/start")
	if err == nil || !strings.Contains(err.Error(), "public network") {
		t.Fatalf("Get error = %q, want redirect rejection", err)
	}
	if requests != 1 {
		t.Fatalf("round trips = %d, want only the public origin", requests)
	}
}

func TestRestrictedHTTPClientRejectsSpecialUseGlobalUnicastRanges(t *testing.T) {
	unsafe := []string{
		"0.0.0.1",
		"100.64.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"240.0.0.1",
		"2001:db8::1",
		"2001:2::1",
		"100::1",
		"3fff::1",
		"5f00::1",
	}
	for _, raw := range unsafe {
		t.Run(raw, func(t *testing.T) {
			address := net.ParseIP(raw)
			if isPublicIP(address) {
				t.Fatalf("isPublicIP(%s) = true", address)
			}
		})
	}
}

func TestDialValidatedAddressNeverReResolvesHostname(t *testing.T) {
	lookupCalls := 0
	dialed := ""
	sentinel := errors.New("stop after capture")
	_, err := dialValidatedAddress(
		context.Background(), "tcp", "tracker.example:443",
		func(context.Context, string) ([]net.IP, error) {
			lookupCalls++
			if lookupCalls == 1 {
				return []net.IP{net.ParseIP("8.8.8.8")}, nil
			}
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
		func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = address
			return nil, sentinel
		},
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("dialValidatedAddress error = %v, want sentinel", err)
	}
	if lookupCalls != 1 {
		t.Fatalf("lookup calls = %d, want exactly one", lookupCalls)
	}
	if dialed != "8.8.8.8:443" {
		t.Fatalf("underlying dial address = %q, want validated IP literal", dialed)
	}
}

func TestEngineRequestErrorDoesNotExposeConfiguredURL(t *testing.T) {
	registry, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	const marker = "write-only-marker"
	client := NewClient(registry, core.IndexerConfig{
		DefinitionID: "thepiratebay",
		Name:         "error fixture",
		URL:          "https://thepiratebay.org",
		Settings:     map[string]string{"apiurl": "https://apibay.example/?token=" + marker},
	}, &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: errors.New("synthetic dial failure")}
		}),
	})

	_, err = client.Search(context.Background(), "ubuntu", nil)
	if err == nil {
		t.Fatal("Search unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), "apibay.example") {
		t.Fatalf("Search error leaked configured URL: %q", err)
	}
}

func TestEngineRenderedURLErrorDoesNotExposeSetting(t *testing.T) {
	registry, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	const marker = "super-secret-setting"
	client := NewClient(registry, core.IndexerConfig{
		DefinitionID: "thepiratebay",
		URL:          "https://thepiratebay.org",
		Settings:     map[string]string{"apiurl": "https://example.com/%zz" + marker},
	}, &http.Client{})
	_, err = client.Search(context.Background(), "ubuntu", nil)
	if err == nil {
		t.Fatal("Search succeeded with invalid rendered URL")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("Search error exposed setting: %q", err)
	}
}

func TestNewRestrictedHTTPClientSupportsCustomDefaultTransport(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    r,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })

	client := NewRestrictedHTTPClient(time.Second)
	response, err := client.Get("https://93.184.216.34/test")
	if err != nil {
		t.Fatalf("Get with custom default transport: %v", err)
	}
	_ = response.Body.Close()
}
