package dlna

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

func searchBody(containerID, criteria string, start, count int) string {
	return `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body><u:Search xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
<ContainerID>` + containerID + `</ContainerID>
<SearchCriteria>` + criteria + `</SearchCriteria>
<Filter>*</Filter>
<StartingIndex>` + itoa(start) + `</StartingIndex>
<RequestedCount>` + itoa(count) + `</RequestedCount>
<SortCriteria></SortCriteria>
</u:Search></s:Body></s:Envelope>`
}

// TestSearchListsEveryVideoItem is the Infuse case this feature exists for:
// one recursive Search from the root, filtered to video items, must surface
// the whole playable library.
func TestSearchListsEveryVideoItem(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	didl, err := svc.search(context.Background(), testURLs, rootID,
		`upnp:class derivedfrom "object.item.videoItem" and @refID exists false`)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(didl.Containers) != 0 {
		t.Errorf("containers = %+v, want none for an item-class search", didl.Containers)
	}
	// The seeded library: one movie file + three episode files (see
	// seedLibrary), every one of them playable.
	if len(didl.Items) != 4 {
		t.Fatalf("items = %v, want the 4 playable files", itemTitles(didl))
	}
	for _, item := range didl.Items {
		if item.Class != classVideoItem {
			t.Errorf("item %q class = %q, want %q", item.Title, item.Class, classVideoItem)
		}
		if item.Res.URL == "" {
			t.Errorf("item %q has no res URL", item.Title)
		}
	}
}

// TestSearchScopesToTheContainer: a Search under one container must not leak
// the rest of the library.
func TestSearchScopesToTheContainer(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	ctx := context.Background()

	movies, err := svc.search(ctx, testURLs, moviesID, `upnp:class derivedfrom "object.item.videoItem"`)
	if err != nil {
		t.Fatalf("search(movies): %v", err)
	}
	if len(movies.Items) != 1 {
		t.Errorf("movie-scoped items = %v, want just the movie", itemTitles(movies))
	}

	tv, err := svc.search(ctx, testURLs, tvID, `upnp:class derivedfrom "object.item.videoItem"`)
	if err != nil {
		t.Fatalf("search(tv): %v", err)
	}
	if len(tv.Items) != 3 {
		t.Errorf("tv-scoped items = %v, want the 3 episodes", itemTitles(tv))
	}
}

// TestSearchAnswersMusicWithNothing: a criteria naming classes this server
// does not have is a real question with an empty answer, not a fault and not
// the whole library.
func TestSearchAnswersMusicWithNothing(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	didl, err := svc.search(context.Background(), testURLs, rootID,
		`upnp:class derivedfrom "object.item.audioItem" or upnp:class derivedfrom "object.container.album.musicAlbum"`)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if didl.count() != 0 {
		t.Errorf("audio search returned %v", itemTitles(didl))
	}
}

func TestSearchFiltersByTitle(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	didl, err := svc.search(context.Background(), testURLs, rootID,
		`upnp:class derivedfrom "object.item.videoItem" and dc:title contains "islands"`)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	titles := itemTitles(didl)
	if len(titles) != 1 || !strings.Contains(titles[0], "Islands") {
		t.Errorf("title-filtered items = %v, want just Islands", titles)
	}
}

func TestSearchStarReturnsEverything(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	didl, err := svc.search(context.Background(), testURLs, rootID, "*")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(didl.Items) != 4 || len(didl.Containers) == 0 {
		t.Errorf("star search = %d containers, %d items; want every container and item",
			len(didl.Containers), len(didl.Items))
	}
}

func TestSearchUnknownContainerFaults(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	rec := soapPost(t, svc.Handler(), MountPath+"/control/cds", contentDirectoryType+"#Search",
		searchBody("s:9999:nope", "*", 0, 0))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "701") {
		t.Fatalf("unknown container = %d %q, want a 701 fault", rec.Code, rec.Body.String())
	}
}

// TestSearchOverSOAP exercises the wire shape: paging plus TotalMatches, the
// same contract as Browse.
func TestSearchOverSOAP(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	rec := soapPost(t, svc.Handler(), MountPath+"/control/cds", contentDirectoryType+"#Search",
		searchBody("0", `upnp:class derivedfrom "object.item.videoItem"`, 1, 2))
	if rec.Code != http.StatusOK {
		t.Fatalf("Search = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result         string `xml:"Body>SearchResponse>Result"`
		NumberReturned int    `xml:"Body>SearchResponse>NumberReturned"`
		TotalMatches   int    `xml:"Body>SearchResponse>TotalMatches"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode SOAP response: %v\n%s", err, rec.Body.String())
	}
	var didl didlProbe
	if err := xml.Unmarshal([]byte(resp.Result), &didl); err != nil {
		t.Fatalf("decode DIDL result: %v\n%s", err, resp.Result)
	}
	if resp.TotalMatches != 4 {
		t.Errorf("TotalMatches = %d, want 4", resp.TotalMatches)
	}
	if resp.NumberReturned != 2 || len(didl.Items) != 2 {
		t.Errorf("NumberReturned = %d with %d items, want the 2-item page", resp.NumberReturned, len(didl.Items))
	}
}

func TestGetSearchCapabilitiesNamesTheFilters(t *testing.T) {
	svc, _, _ := newTestService(t)

	rec := soapPost(t, svc.Handler(), MountPath+"/control/cds",
		contentDirectoryType+"#GetSearchCapabilities",
		`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetSearchCapabilities xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"/></s:Body></s:Envelope>`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), searchCaps) {
		t.Fatalf("GetSearchCapabilities = %d %q, want %q", rec.Code, rec.Body.String(), searchCaps)
	}
}

func TestParseSearchCriteria(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		class string
		want  bool
	}{
		{"videoItem derivedfrom matches", `upnp:class derivedfrom "object.item.videoItem"`, classVideoItem, true},
		{"ancestor derivedfrom matches", `upnp:class derivedfrom "object.item"`, classVideoItem, true},
		{"exact equality matches", `upnp:class = "object.item.videoItem"`, classVideoItem, true},
		{"equality on an ancestor does not", `upnp:class = "object.item"`, classVideoItem, false},
		{"audio does not match video", `upnp:class derivedfrom "object.item.audioItem"`, classVideoItem, false},
		{"or of classes matches either", `upnp:class = "object.item.audioItem" or upnp:class = "object.item.videoItem"`, classVideoItem, true},
		{"star matches everything", `*`, classContainer, true},
		{"gibberish matches nothing", `@id exists true`, classVideoItem, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseSearchCriteria(tt.raw).wantsClass(tt.class); got != tt.want {
				t.Errorf("wantsClass(%q, %q) = %v, want %v", tt.raw, tt.class, got, tt.want)
			}
		})
	}
}
