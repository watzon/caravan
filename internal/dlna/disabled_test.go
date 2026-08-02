package dlna

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/store"
)

// The DLNA mount sits outside the API's password gate, because a television
// cannot log in. That exemption is only defensible for a feature the owner
// asked for.
//
// Before this was fixed, an owner who set a password under Settings -> Security
// and turned DLNA off still served the whole library to anyone on the LAN: a
// ContentDirectory Browse walked the container tree with no cookie and no API
// key, and every res element it handed back was a /dlna/media/<id> URL that
// downloaded the file in full. Every /api/v1 route answered 401 for the same
// caller, so the password protected the metadata API and nothing that mattered.
func TestDisabledDLNAServesNothing(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	h := svc.Handler()

	// While it is on, this is the attack path and it works.
	rec := soapPost(t, h, MountPath+"/control/cds", contentDirectoryType+"#Browse", browseBody("0", "BrowseDirectChildren", 0, 10))
	if rec.Code != http.StatusOK {
		t.Fatalf("browse with DLNA on = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	_, didl := decodeBrowseResponse(t, rec)
	if len(didl.Containers) == 0 {
		t.Fatal("browse with DLNA on returned no containers; the fixture is wrong")
	}

	// The owner turns it off.
	if err := st.SetSetting(context.Background(), store.SettingDLNAEnabled, "false"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	for _, tc := range []struct{ name, method, target string }{
		{"device description", http.MethodGet, MountPath + "/device.xml"},
		{"content directory scpd", http.MethodGet, MountPath + "/cds.xml"},
		{"media file", http.MethodGet, MountPath + "/media/anything.mkv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s = %d with DLNA off, want 404 (body %q)", tc.target, rec.Code, rec.Body.String())
			}
		})
	}

	rec = soapPost(t, h, MountPath+"/control/cds", contentDirectoryType+"#Browse", browseBody("0", "BrowseDirectChildren", 0, 10))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("browse with DLNA off = %d, want 404 (body %q)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "DIDL-Lite") {
		t.Fatal("a Browse against a disabled media server still enumerated the library")
	}
}
