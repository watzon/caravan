package api

import (
	"context"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/watzon/caravan/internal/dlna"
)

// stubDLNA stands in for the media server: it records reloads and serves a
// recognisable body from its protocol handler.
type stubDLNA struct {
	status  dlna.Status
	err     error
	reloads atomic.Int64
}

func (s *stubDLNA) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte("<root>" + r.URL.Path + "</root>"))
	})
}

func (s *stubDLNA) Status(context.Context) (dlna.Status, error) { return s.status, s.err }

func (s *stubDLNA) Reload(context.Context) { s.reloads.Add(1) }

func (s *stubDLNA) Recent() []dlna.TraceEntry { return nil }

func TestDLNAStatus(t *testing.T) {
	stub := &stubDLNA{status: dlna.Status{
		Config:      dlna.Config{Enabled: true, FriendlyName: "Den TV", UUID: "abc"},
		Advertising: true,
	}}
	h, _, _ := newTestServer(t, WithDLNA(stub))

	rec := do(t, h, "GET", "/api/v1/dlna", "")
	wantStatus(t, rec, 200)
	var got dlnaJSON
	decodeBody(t, rec, &got)
	want := dlnaJSON{Enabled: true, FriendlyName: "Den TV", UUID: "abc", Advertising: true}
	got.Recent = nil // the trace is additive detail; the identity fields are the assertion
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// Enabled-but-silent is a real state — a host with no usable multicast — and
// the UI has to be able to tell it apart from "switched off".
func TestDLNAStatusReportsWhyItIsNotAdvertising(t *testing.T) {
	stub := &stubDLNA{status: dlna.Status{
		Config: dlna.Config{Enabled: true, FriendlyName: "Caravan"},
		Error:  "join ssdp group: no such device",
	}}
	h, _, _ := newTestServer(t, WithDLNA(stub))

	rec := do(t, h, "GET", "/api/v1/dlna", "")
	wantStatus(t, rec, 200)
	var got dlnaJSON
	decodeBody(t, rec, &got)
	if !got.Enabled || got.Advertising {
		t.Fatalf("got %+v, want enabled and not advertising", got)
	}
	if got.Error == "" {
		t.Fatal("no reason given for not advertising")
	}
}

// A server built without the media server answers "off" rather than failing:
// the settings screen renders a disabled section instead of a load error.
func TestDLNAStatusWithoutTheService(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/v1/dlna", "")
	wantStatus(t, rec, 200)
	var got dlnaJSON
	decodeBody(t, rec, &got)
	if !reflect.DeepEqual(got, dlnaJSON{}) {
		t.Fatalf("got %+v, want the zero status", got)
	}
}

// The protocol surface is mounted outside /api/v1, on the URLs SSDP advertises.
func TestDLNAHandlerIsMounted(t *testing.T) {
	h, _, _ := newTestServer(t, WithDLNA(&stubDLNA{}))

	rec := do(t, h, "GET", dlna.MountPath+"/device.xml", "")
	wantStatus(t, rec, 200)
	if rec.Body.String() != "<root>"+dlna.MountPath+"/device.xml</root>" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	// It must not be swallowed by the SPA fallback, which would answer
	// index.html to a television.
	if ct := rec.Header().Get("Content-Type"); ct != "text/xml" {
		t.Fatalf("Content-Type = %q, want the DLNA handler's own", ct)
	}
}

// Without the service the path falls through to the SPA, not to a 500.
func TestDLNAPathWithoutTheService(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, "GET", dlna.MountPath+"/device.xml", "")
	wantStatus(t, rec, 200)
	if rec.Header().Get("Content-Type") == "text/xml" {
		t.Fatal("a DLNA response was served without a DLNA service")
	}
}

// The toggle rides on the ordinary settings flow, and saving it has to take
// effect without a restart.
func TestPutSettingsReloadsDLNA(t *testing.T) {
	stub := &stubDLNA{}
	h, st, _ := newTestServer(t, WithDLNA(stub))

	rec := do(t, h, "PUT", "/api/v1/settings", `{"dlna_enabled":"false","dlna_friendly_name":"Den TV"}`)
	wantStatus(t, rec, 200)
	if stub.reloads.Load() != 1 {
		t.Fatalf("reloads = %d, want 1", stub.reloads.Load())
	}

	settings, err := st.AllSettings(t.Context())
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if settings["dlna_enabled"] != "false" || settings["dlna_friendly_name"] != "Den TV" {
		t.Fatalf("settings = %v", settings)
	}
}

// A value the media server would silently reinterpret is rejected where the
// user can see it, not stored and quietly ignored.
func TestPutSettingsValidatesDLNA(t *testing.T) {
	stub := &stubDLNA{}
	h, st, _ := newTestServer(t, WithDLNA(stub))

	for _, body := range []string{
		`{"dlna_enabled":"sure"}`,
		`{"dlna_friendly_name":"   "}`,
		`{"dlna_friendly_name":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
	} {
		rec := do(t, h, "PUT", "/api/v1/settings", body)
		wantStatus(t, rec, 400)
		wantErrorBody(t, rec)
	}
	// A rejected save neither writes nor reloads.
	if stub.reloads.Load() != 0 {
		t.Fatalf("reloads = %d, want 0", stub.reloads.Load())
	}
	settings, err := st.AllSettings(t.Context())
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if _, ok := settings["dlna_enabled"]; ok {
		t.Fatalf("a rejected value was stored: %v", settings)
	}

	// The valid forms are accepted.
	for _, body := range []string{`{"dlna_enabled":"true"}`, `{"dlna_enabled":"false"}`} {
		rec := do(t, h, "PUT", "/api/v1/settings", body)
		wantStatus(t, rec, 200)
	}
}
