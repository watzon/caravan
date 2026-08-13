package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/watzon/caravan/internal/api"
)

func TestDevUIOriginIgnoredInReleaseBuilds(t *testing.T) {
	t.Setenv(api.EnvDevUI, "http://127.0.0.1:5173")
	orig := api.Version
	t.Cleanup(func() { api.Version = orig })

	api.Version = "0.1.0"
	got, err := devUIOrigin(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("devUIOrigin: %v", err)
	}
	if got != "" {
		t.Fatalf("devUIOrigin in a release build = %q, want empty", got)
	}

	api.Version = "dev"
	got, err = devUIOrigin(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("devUIOrigin: %v", err)
	}
	if got != "http://127.0.0.1:5173" {
		t.Fatalf("devUIOrigin in a dev build = %q, want the Vite origin", got)
	}
}

func TestDevUIOriginRejectsRemoteTargets(t *testing.T) {
	t.Setenv(api.EnvDevUI, "http://example.com:5173")
	orig := api.Version
	t.Cleanup(func() { api.Version = orig })
	api.Version = "dev"

	if _, err := devUIOrigin(slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("devUIOrigin accepted a remote origin")
	}
}
