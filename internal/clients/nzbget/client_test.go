package nzbget

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// The fixtures are NZBGet's real payloads, including the fields Caravan does
// not read and the split 64-bit sizes. Decoding them is the contract this
// package exists to keep.
func TestListGroupsDecodesTheRealPayload(t *testing.T) {
	srv := fixtureServer(t, "listgroups.json")
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	groups, err := c.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 4 {
		t.Fatalf("groups = %d, want 4", len(groups))
	}

	got := groups[0]
	want := Group{
		NZBID:           41,
		NZBName:         "Arrival.2016.1080p.BluRay.x264-GROUP",
		Kind:            "NZB",
		Status:          statusDownload,
		Category:        "caravan-movies",
		DestDir:         "/downloads/intermediate/Arrival.2016.1080p.BluRay.x264-GROUP",
		FinalDir:        "",
		FileSizeLo:      0,
		FileSizeHi:      2,
		RemainingSizeLo: 0,
		RemainingSizeHi: 1,
		Health:          1000,
	}
	if got != want {
		t.Fatalf("group =\n%+v\nwant\n%+v", got, want)
	}
	// The split halves are the whole reason this decodes into two fields.
	if size(got.FileSizeHi, got.FileSizeLo) != 8<<30 {
		t.Fatalf("file size = %d, want 8 GiB", size(got.FileSizeHi, got.FileSizeLo))
	}
}

func TestHistoryDecodesTheRealPayload(t *testing.T) {
	srv := fixtureServer(t, "history.json")
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	items, err := c.History(context.Background())
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4", len(items))
	}

	done := items[0]
	if done.NZBID != 39 || done.Status != "SUCCESS/ALL" {
		t.Fatalf("item = %+v", done)
	}
	if done.FinalDir != "/downloads/complete/caravan-movies/Sicario.2015.1080p.BluRay.x264-GROUP" {
		t.Fatalf("final dir = %q: the completed payload path is the whole point of reading history", done.FinalDir)
	}
	if items[1].Status != "FAILURE/PAR" || items[1].ParStatus != "FAILURE" {
		t.Fatalf("failed item = %+v", items[1])
	}
}

// The hidden flag is false so NZBGet's duplicate-detection tombstones stay out
// of the queue.
func TestHistoryDoesNotAskForHiddenRecords(t *testing.T) {
	c, f := newClient(t)

	if _, err := c.History(context.Background()); err != nil {
		t.Fatalf("History: %v", err)
	}
	params := f.seen("history")[0].Params
	if len(params) != 1 || params[0] != false {
		t.Fatalf("params = %#v, want [false]", params)
	}
}

func TestVersion(t *testing.T) {
	c, f := newClient(t)

	version, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "24.3" {
		t.Fatalf("version = %q", version)
	}
	if got := f.seen("version")[0].Auth; got != testUser {
		t.Fatalf("auth user = %q, want the configured login", got)
	}
}

func TestWrongLoginIsUnauthorizedAndNeverQuoted(t *testing.T) {
	_, srv := newFake(t)
	cfg := config(srv)
	cfg.Password = "wrong-password-sentinel"
	c, err := New(cfg, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.Version(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), cfg.Password) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

// A user may well have pasted credentials into the base URL, and net/http
// quotes the whole URL back in a transport error.
func TestTransportErrorsNeverQuoteTheURL(t *testing.T) {
	_, srv := newFake(t)
	cfg := config(srv)
	cfg.URL = strings.Replace(srv.URL, "http://", "http://user:transport-secret@", 1)
	c, err := New(cfg, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.Close()

	_, err = c.Version(context.Background())
	if err == nil {
		t.Fatalf("Version against a closed server succeeded")
	}
	if strings.Contains(err.Error(), "transport-secret") {
		t.Fatalf("transport error quoted the URL's credentials: %q", err.Error())
	}
}

// A fault arrives inside a 200, so the status code alone cannot be trusted.
func TestRPCFaultIsReported(t *testing.T) {
	c, _ := newClient(t)

	var out string
	err := c.call(context.Background(), "nosuchmethod", nil, &out)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v, want an RPCError", err)
	}
	if rpcErr.Method != "nosuchmethod" || rpcErr.Message == "" {
		t.Fatalf("rpc error = %+v", rpcErr)
	}
}

func TestHTTPFailureIsAnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Version(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, want an APIError with status 502", err)
	}
	if strings.Contains(err.Error(), testPass) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

func TestNonRPCAnswerIsReportedAsSuch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>Sign in</body></html>"))
	}))
	t.Cleanup(srv.Close)

	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Version(context.Background()); err == nil {
		t.Fatalf("Version accepted an HTML page")
	}
}

// NZBGet reads parameters positionally and ignores their names, so the exact
// list — and its order — is the wire format.
func TestAppendSendsNZBGetsPositionalParameters(t *testing.T) {
	c, f := newClient(t)

	nzbID, err := c.Append(context.Background(), AppendRequest{
		Filename: "Sicario.2015.1080p.BluRay-GROUP.nzb",
		Content:  []byte(nzbBody),
		Category: "caravan-movies",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if nzbID <= 0 {
		t.Fatalf("Append returned id %d", nzbID)
	}

	params := f.seen("append")[0].Params
	if len(params) != 9 {
		t.Fatalf("params = %d, want the 9 NZBGet parses", len(params))
	}
	if params[0] != "Sicario.2015.1080p.BluRay-GROUP.nzb" {
		t.Fatalf("filename = %v", params[0])
	}
	content, _ := params[1].(string)
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		t.Fatalf("content is not base64: %v", err)
	}
	if string(decoded) != nzbBody {
		t.Fatalf("content did not round-trip through base64")
	}
	if params[2] != "caravan-movies" {
		t.Fatalf("category = %v", params[2])
	}
	if params[3] != float64(0) {
		t.Fatalf("priority = %v, want 0", params[3])
	}
	if params[4] != false {
		t.Fatalf("addTop = %v, want false: Caravan does not reorder the user's queue", params[4])
	}
	if params[5] != false {
		t.Fatalf("addPaused = %v, want false", params[5])
	}
	// DupeMode is mandatory once AddPaused is sent, and NZBGet rejects
	// anything but score/all/force.
	if params[8] != dupeModeScore {
		t.Fatalf("dupeMode = %v, want %q", params[8], dupeModeScore)
	}
}

// A refused append is a zero id with no message at all, so the client has to
// notice the id rather than wait for a fault.
func TestAppendReportsARefusedNZB(t *testing.T) {
	c, f := newClient(t)
	f.appendFails = true

	_, err := c.Append(context.Background(), AppendRequest{
		Filename: "Nope.nzb",
		Content:  []byte(nzbBody),
	})
	if err == nil {
		t.Fatalf("Append accepted an NZB NZBGet refused")
	}
}

// The leading zero is the pre-18.0 Offset parameter: current NZBGet skips it
// when it is not an integer, older NZBGet requires it, and sending it works
// against both.
func TestEditQueueSendsTheCompatibleParameterList(t *testing.T) {
	c, f := newClient(t)

	ok, err := c.EditQueue(context.Background(), EditGroupPause, 41)
	if err != nil {
		t.Fatalf("EditQueue: %v", err)
	}
	if !ok {
		t.Fatalf("EditQueue reported no match for a queued group")
	}
	params := f.seen("editqueue")[0].Params
	if len(params) != 4 {
		t.Fatalf("params = %#v, want command, offset, arg and one id", params)
	}
	if params[0] != EditGroupPause || params[1] != float64(0) || params[2] != "" || params[3] != float64(41) {
		t.Fatalf("params = %#v", params)
	}
}

// An edit aimed at the list an id is not in answers a plain false, which is
// information rather than a failure.
func TestEditQueueReportsNoMatchWithoutAnError(t *testing.T) {
	c, _ := newClient(t)

	ok, err := c.EditQueue(context.Background(), EditGroupPause, 999999)
	if err != nil {
		t.Fatalf("EditQueue: %v", err)
	}
	if ok {
		t.Fatalf("EditQueue claimed to have matched an unknown id")
	}
}

func TestNewRejectsAnUnusableConfiguration(t *testing.T) {
	tests := []struct {
		name string
		cfg  core.DownloadClientConfig
	}{
		{"no name", core.DownloadClientConfig{URL: "http://localhost:6789", Username: "u"}},
		{"no url", core.DownloadClientConfig{Name: "n", Username: "u"}},
		{"url without scheme", core.DownloadClientConfig{Name: "n", URL: "localhost:6789", Username: "u"}},
		{"no username", core.DownloadClientConfig{Name: "n", URL: "http://localhost:6789"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg, nil); err == nil {
				t.Fatalf("New accepted %+v", tt.cfg)
			}
		})
	}
}

func TestClientAppendsTheRPCPathToTheBaseURL(t *testing.T) {
	_, srv := newFake(t)
	cfg := config(srv)
	cfg.URL = srv.URL + "/"
	c, err := New(cfg, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.endpoint != srv.URL+rpcPath {
		t.Fatalf("endpoint = %q, want the base plus %q with no doubled slash", c.endpoint, rpcPath)
	}
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("Version: %v", err)
	}
}

func TestTestConnectionSucceeds(t *testing.T) {
	_, srv := newFake(t)
	if err := TestConnection(context.Background(), config(srv)); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
}

func TestTestConnectionReportsAuthFailureWithoutTheCredential(t *testing.T) {
	f, srv := newFake(t)
	f.rejectAuth = true
	cfg := config(srv)
	cfg.Password = "wrong-password-sentinel"

	err := TestConnection(context.Background(), cfg)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), cfg.Password) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

func TestTestConnectionReportsAnUnreachableClient(t *testing.T) {
	_, srv := newFake(t)
	cfg := config(srv)
	srv.Close()

	err := TestConnection(context.Background(), cfg)
	if err == nil {
		t.Fatalf("TestConnection against a closed server succeeded")
	}
	if strings.Contains(err.Error(), testPass) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

// Something that answers a well-formed envelope with no version is not NZBGet.
func TestTestConnectionRejectsAnEmptyVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"1.1","result":""}`))
	}))
	t.Cleanup(srv.Close)

	if err := TestConnection(context.Background(), config(srv)); err == nil {
		t.Fatalf("TestConnection accepted a server with no version")
	}
}

func TestRegisterInstallsTheProbe(t *testing.T) {
	reg := clients.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !reg.Supported(core.DownloadClientNZBGet) {
		t.Fatalf("nzbget not supported after Register")
	}
	if err := Register(reg); err == nil {
		t.Fatalf("registering twice succeeded")
	}

	_, srv := newFake(t)
	if err := reg.TestConnection(context.Background(), config(srv)); err != nil {
		t.Fatalf("registry TestConnection: %v", err)
	}
}
