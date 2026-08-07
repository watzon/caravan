package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stashbox"
	"github.com/watzon/caravan/internal/store"
)

// The adult module's HTTP surface (PLAN phase 9 task 7).
//
// EVERY route in this file is registered on the adult mux in api.go and nowhere
// else. That is not a filing convention, it is the access control: requireAdult
// wraps the mux, so a handler here cannot be reached by a caller the gate would
// refuse, and it therefore does not repeat the check. A route moved out of that
// mux would keep compiling and silently lose its gate, which is why api.go says
// so at the registration site too.
//
// The one adult-shaped route that is NOT here is the master switch
// (POST /settings/adult, in settings.go): it has to be reachable while the
// module is off, because turning it on is its whole job.

// siteJSON is one site on the Adult grid.
//
// It is a DTO of its own rather than seriesJSON, even though a site IS a series
// row (PLAN phase 9 task 3). seriesJSON carries tmdb_id, tvdb_id, imdb_id,
// status and first_aired, and every one of those is permanently zero for a
// site: stash-box supplies none of them, and a site is a publisher rather than
// a production with a first-air year. A card rendered from fields that are
// always empty invites a client to display them, so they are not offered.
type siteJSON struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// StashID is the provider id, which is what identifies a site everywhere
	// the API accepts one — a library id is Caravan's own and means nothing to
	// the discover screen.
	StashID          string `json:"stash_id"`
	SortTitle        string `json:"sort_title"`
	Overview         string `json:"overview"`
	Path             string `json:"path"`
	PosterPath       string `json:"poster_path"`
	PosterURL        string `json:"poster_url"`
	Monitored        bool   `json:"monitored"`
	QualityProfileID int64  `json:"quality_profile_id"`
	LibraryID        int64  `json:"library_id"`
	AddedAt          string `json:"added_at"`
	UpdatedAt        string `json:"updated_at"`
	// SceneCount and SceneFileCount are the grid's "18 / 240" badge, carried on
	// the list so a poster grid costs one query rather than one per card. They
	// are the episode counts under a different name, because on this screen a
	// scene is what an episode is.
	SceneCount     int `json:"scene_count"`
	SceneFileCount int `json:"scene_file_count"`
}

// siteDetailJSON is one site's page: its release years, each holding its
// scenes. A year is a season row, so "years as seasons" is the storage fact and
// this is only its name on the wire.
type siteDetailJSON struct {
	siteJSON
	// ProviderURL is the site's page on the metadata endpoint's own website,
	// empty when there is no id to link or no endpoint configured.
	//
	// It is derived here rather than in the browser because the shape depends on
	// which endpoint is configured, and the endpoint setting is admin-only —
	// while this page is one a granted member reads. Deriving it in the SPA
	// would mean either handing the setting to every reader or showing the link
	// to admins alone, and a link off a record is not an admin fact.
	ProviderURL string `json:"provider_url"`
	// Cataloguing is true while a catalogue walk for this site is queued or
	// running (core.JobSyncSite).
	//
	// It exists because the walk is now something the reader can WATCH: the
	// scenes land a release year at a time while the job runs, so the page
	// polls itself until this goes false. Without it the page cannot tell a
	// site that is still being indexed from one the provider has nothing for,
	// and those two need opposite words on screen.
	Cataloguing bool           `json:"cataloguing"`
	Years       []siteYearJSON `json:"years"`
}

type siteYearJSON struct {
	Year      int         `json:"year"`
	Monitored bool        `json:"monitored"`
	Scenes    []sceneJSON `json:"scenes"`
}

// sceneJSON is one scene row on a site's page. Number is the scene's sequence
// within its release year — the episode number — which is what the "#003"
// prefix on the row renders.
type sceneJSON struct {
	ID         int64    `json:"id"`
	SeriesID   int64    `json:"series_id"`
	Year       int      `json:"year"`
	Number     int      `json:"number"`
	StashID    string   `json:"stash_id"`
	Title      string   `json:"title"`
	Overview   string   `json:"overview"`
	Studio     string   `json:"studio"`
	Performers []string `json:"performers"`
	URL        string   `json:"url"`
	// ProviderURL is the scene's page on the metadata endpoint's own website —
	// the site detail page links titles there, not to the scene's own site,
	// because the provider page is the one that explains what Caravan thinks
	// this scene is. Derived server-side for the reason siteJSON.ProviderURL is.
	ProviderURL string `json:"provider_url"`
	// ReleaseDate is the episode's air date under the name this screen uses: a
	// scene is published, not broadcast.
	ReleaseDate string `json:"release_date"`
	Monitored   bool   `json:"monitored"`
	// File is the imported file, null when the scene is missing. It is the same
	// shape and the same status vocabulary the episode rows use, deliberately:
	// the Adult pages are the library's pages with different nouns.
	File *mediaFileJSON `json:"file"`
}

// siteMetaJSON is a provider search hit: a site that may or may not be in the
// library yet, so it carries a stash id and no library id.
type siteMetaJSON struct {
	StashID string `json:"stash_id"`
	Name    string `json:"name"`
	// Aliases are the other names the provider knows this site by. The picker
	// shows them because a release name carries whichever one the packager saw,
	// and picking the right site is easier when its aliases are visible.
	Aliases    []string `json:"aliases"`
	ParentName string   `json:"parent_name"`
	URL        string   `json:"url"`
	ImageURL   string   `json:"image_url"`
	// InLibrary and LibraryID say whether Caravan already tracks this site, so
	// the picker can say "already added" rather than offering it twice.
	InLibrary bool  `json:"in_library"`
	LibraryID int64 `json:"library_id"`
}

// sceneMetaJSON is a provider scene decorated with what Caravan knows about it:
// the discover screen's row. It mirrors discoverItemJSON's in_library/requested
// pair, because it answers the same two questions a title card does.
type sceneMetaJSON struct {
	MediaType   string   `json:"media_type"`
	StashID     string   `json:"stash_id"`
	SiteStashID string   `json:"site_stash_id"`
	SiteName    string   `json:"site_name"`
	Title       string   `json:"title"`
	Overview    string   `json:"overview"`
	Date        string   `json:"date"`
	Duration    int      `json:"duration"`
	Performers  []string `json:"performers"`
	URL         string   `json:"url"`
	ImageURL    string   `json:"image_url"`
	InLibrary   bool     `json:"in_library"`
	LibraryID   int64    `json:"library_id"`
	Requested   bool     `json:"requested"`
}

// handleListSites answers the Adult grid: every site in the library, with the
// scene counts its badge renders.
//
// It is reachable by a GRANTED member as well as an admin, which is more than
// GET /library/series offers one — that route is admin-only. The asymmetry is
// deliberate and comes from the phase spec: the Adult nav item exists for
// anyone the module is visible to, and a nav item that 403s is a worse answer
// than a shelf. The grant is what makes it safe, and the gate is what enforces
// the grant; write operations stay admin-only by staying out of memberAllowed.
func (s *server) handleListSites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sites, err := s.st.ListSeriesByKind(ctx, core.SeriesKindAdult)
	if err != nil {
		s.writeStoreError(w, "list sites", err)
		return
	}
	counts, err := s.st.EpisodeCountsBySeries(ctx)
	if err != nil {
		s.writeStoreError(w, "count scenes", err)
		return
	}

	out := make([]siteJSON, 0, len(sites))
	for _, sr := range sites {
		dto := siteDTO(sr)
		dto.SceneCount = counts[sr.ID].Total
		dto.SceneFileCount = counts[sr.ID].WithFile
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out})
}

// handleGetSite answers one site's page.
//
// A television series id is refused with the same 404 an unknown id gets: the
// two live in one table, and answering for a series here would turn the adult
// endpoint into a way to read the television library from an account that may
// not have GET /library/series.
func (s *server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	sr, err := s.st.GetSeries(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get site", err)
		return
	}
	if sr.Kind != core.SeriesKindAdult {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	years, count, withFile, err := s.siteYears(ctx, sr.ID)
	if err != nil {
		s.writeStoreError(w, "read scenes", err)
		return
	}
	dto := siteDTO(*sr)
	dto.SceneCount, dto.SceneFileCount = count, withFile
	writeJSON(w, http.StatusOK, siteDetailJSON{
		siteJSON:    dto,
		ProviderURL: s.siteProviderURL(ctx, sr.StashID),
		Cataloguing: s.siteCataloguing(ctx, sr.ID),
		Years:       years,
	})
}

// siteCataloguing reports whether a catalogue walk for this site is queued or
// running.
//
// The match is on the payload's series id rather than on the payload string,
// which is why this reads the open jobs instead of asking HasOpenJob: the
// sync_site payload carries SearchNow too, so the same site has two possible
// encodings and an exact-string match would miss one of them.
//
// A queue read that fails answers "not cataloguing" rather than failing the
// page, exactly as siteProviderURL does with its setting. The cost of being
// wrong is a page that stops polling a second early — against losing the whole
// site view to a transient database error, that is not a trade worth making.
func (s *server) siteCataloguing(ctx context.Context, seriesID int64) bool {
	jobs, err := s.st.OpenJobsByKind(ctx, core.JobSyncSite)
	if err != nil {
		s.log.Error("read open catalogue walks", "series_id", seriesID, "error", err)
		return false
	}
	for _, job := range jobs {
		var payload core.JobSyncSitePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			// A payload this process cannot read is a job it did not write.
			// It is not evidence about this site either way.
			continue
		}
		if payload.SeriesID == seriesID {
			return true
		}
	}
	return false
}

// siteProviderURL is the site's page on the configured endpoint's website.
//
// A settings read that fails is not worth failing the page for: the link is the
// least important thing on it, and a site with no link renders exactly as one
// whose provider has no page.
func (s *server) siteProviderURL(ctx context.Context, stashID string) string {
	endpoint, err := s.st.GetSetting(ctx, store.SettingStashboxEndpoint)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return ""
	}
	return stashbox.SiteWebURL(endpoint, stashID)
}

// siteYears assembles a site's release years and their scenes, and reports the
// totals so the page header does not need a second pass.
func (s *server) siteYears(ctx context.Context, seriesID int64) ([]siteYearJSON, int, int, error) {
	seasons, err := s.st.ListSeasons(ctx, seriesID)
	if err != nil {
		return nil, 0, 0, err
	}
	episodes, err := s.st.ListEpisodes(ctx, seriesID)
	if err != nil {
		return nil, 0, 0, err
	}
	filePairs, err := s.st.ListEpisodeMediaFilesForSeries(ctx, seriesID)
	if err != nil {
		return nil, 0, 0, err
	}
	filesByEpisode := make(map[int64][]core.MediaFile)
	for _, pair := range filePairs {
		filesByEpisode[pair.EpisodeID] = append(filesByEpisode[pair.EpisodeID], pair.File)
	}

	profile := s.activeTVProfile(ctx)
	// Read once for the whole page, tolerantly for the reason siteProviderURL
	// is: scenes with no provider link render fine.
	endpoint, err := s.st.GetSetting(ctx, store.SettingStashboxEndpoint)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		endpoint = ""
	}
	byYear := map[int][]sceneJSON{}
	total, withFile := 0, 0
	for _, e := range episodes {
		files := filesByEpisode[e.ID]
		total++
		if len(files) > 0 {
			withFile++
		}
		scene := sceneDTO(e, firstFileDTO(files, profile))
		scene.ProviderURL = stashbox.SceneWebURL(endpoint, e.StashID)
		byYear[e.SeasonNumber] = append(byYear[e.SeasonNumber], scene)
	}

	monitored := make(map[int]bool, len(seasons))
	haveRow := make(map[int]bool, len(seasons))
	years := make([]int, 0, len(seasons))
	for _, se := range seasons {
		monitored[se.Number] = se.Monitored
		haveRow[se.Number] = true
		years = append(years, se.Number)
	}
	// A year with scenes but no season row is a reconciliation gap, not
	// something to hide: showing it is how it gets noticed.
	for year := range byYear {
		if !haveRow[year] {
			years = append(years, year)
		}
	}
	// Newest first, years and scenes alike: an adult library is a publication
	// feed, and what somebody wants is almost always the latest release.
	sort.Sort(sort.Reverse(sort.IntSlice(years)))

	out := make([]siteYearJSON, 0, len(years))
	for _, year := range years {
		scenes := byYear[year]
		sort.Slice(scenes, func(i, j int) bool { return scenes[i].Number > scenes[j].Number })
		if scenes == nil {
			scenes = []sceneJSON{}
		}
		out = append(out, siteYearJSON{Year: year, Monitored: monitored[year], Scenes: scenes})
	}
	return out, total, withFile, nil
}

// addSiteRequest is the body of POST /adult/sites. There is no tmdb_id
// counterpart: a site is named by its stash-box id and nothing else.
type addSiteRequest struct {
	StashID string `json:"stash_id"`
	// Monitored is the dialog's "Add and monitor" checkbox, and reads exactly
	// as addRequest.Monitored does — absent means monitored.
	Monitored *bool `json:"monitored"`
	// SearchNow queues a search for every wanted scene once the catalogue is
	// filed. It rides on the sync job rather than happening here (see
	// core.JobSyncSitePayload): before the walk the site has no episode rows,
	// so a search queued now would queue nothing.
	SearchNow bool `json:"search_now"`
	// LibraryID reads exactly as addRequest.LibraryID: the adult library a
	// NEW site lands in, zero for the default.
	LibraryID int64 `json:"library_id"`
}

// handleAddSite adds a site to the library and queues the walk of its
// catalogue. Admin-only, by being absent from memberAllowed: a member may ask
// for a scene (POST /requests), which is the request queue's job, but adding to
// the library is a decision.
//
// It answers as soon as the site row exists, which is one provider round trip.
// The catalogue behind it can be tens of thousands of scenes across two hundred
// pages, and a request that waited for those was one people assumed had hung
// and clicked Add on again. Both halves of that are handled: the row upsert
// keys on the stash id, so a second POST is a refresh rather than a second
// site, and the job dedupes on its payload, so it is not a second walk either.
func (s *server) handleAddSite(w http.ResponseWriter, r *http.Request) {
	var body addSiteRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	stashID := strings.TrimSpace(body.StashID)
	if stashID == "" {
		writeError(w, http.StatusBadRequest, "stash_id is required")
		return
	}
	if !s.validAddLibraryID(w, r, body.LibraryID, core.LibraryKindAdult) {
		return
	}

	ctx := r.Context()
	sr, err := s.mgr.AddSite(ctx, stashID, body.Monitored, body.LibraryID)
	if err != nil {
		s.writeManagerError(w, "", "add site", err)
		return
	}
	if err := s.enqueueSiteSync(ctx, sr.ID, body.SearchNow); err != nil {
		// Unlike the search-on-add flags elsewhere, this one is not logged and
		// swallowed. The walk is what makes the site page anything other than
		// empty, so a site added with no job behind it is a site that stays
		// blank until somebody notices — better to report the failure and let
		// the identical, idempotent re-add fix it.
		s.writeStoreError(w, "queue site catalogue walk", err)
		return
	}
	writeJSON(w, http.StatusCreated, siteDTO(*sr))
}

// enqueueSiteSync queues the catalogue walk for one site unless an identical
// one is already pending or running.
//
// The dedupe is HasOpenJob on the encoded payload, the same one every search
// job uses, which is why the payload is marshalled from core.JobSyncSitePayload
// rather than assembled by hand: the encoded string IS the key.
func (s *server) enqueueSiteSync(ctx context.Context, seriesID int64, searchNow bool) error {
	payload, err := json.Marshal(core.JobSyncSitePayload{SeriesID: seriesID, SearchNow: searchNow})
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", core.JobSyncSite, err)
	}
	open, err := s.st.HasOpenJob(ctx, core.JobSyncSite, string(payload))
	if err != nil {
		return err
	}
	if open {
		return nil
	}
	return s.st.EnqueueJob(ctx, &core.Job{Kind: core.JobSyncSite, Payload: string(payload)})
}

// handleSearchSites queries the provider for sites to add. It is the adult
// twin of GET /search, and admin-only for the reason handleAddSite is: its only
// use is choosing something to add.
//
// A blank q is a search, not a bad request — the difference from GET /search,
// where an empty TMDB query means nothing. A stash-box endpoint answers one with
// its own default list, which is what the add-a-site dialog opens on before
// anything is typed, exactly as GET /adult/discover opens on the newest scenes.
func (s *server) handleSearchSites(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	provider, ok := s.adultProvider(w)
	if !ok {
		return
	}

	ctx := r.Context()
	sites, err := provider.SearchSites(ctx, query)
	if err != nil {
		s.writeAdultProviderError(w, r, "site search", err)
		return
	}

	held, err := s.siteIDsByStashID(ctx, sites)
	if err != nil {
		s.writeStoreError(w, "read library state", err)
		return
	}
	out := make([]siteMetaJSON, 0, len(sites))
	for _, site := range sites {
		dto := siteMetaJSON{
			StashID:    site.StashID,
			Name:       site.Name,
			Aliases:    site.Aliases,
			ParentName: site.ParentName,
			URL:        site.URL,
			ImageURL:   site.ImageURL,
			LibraryID:  held[site.StashID],
		}
		if dto.Aliases == nil {
			dto.Aliases = []string{}
		}
		dto.InLibrary = dto.LibraryID != 0
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out})
}

// handleAdultDiscover is the discover screen for scenes: the provider's own
// results, decorated with what the library holds and what has been asked for.
//
// It is a route of its own rather than a branch inside GET /discover, and that
// is the point. /discover is TMDB-shaped down to its int64 ids and its curated
// network and studio shelves; merging scenes into it would mean the ungranted
// filter lived in a handler rather than at the router, which is exactly the
// arrangement requireAdult exists to avoid. A caller who may not see scenes
// does not get a filtered /discover — they get a 404 from a route they cannot
// reach, and their /discover is byte-for-byte the one they had before the
// module existed.
func (s *server) handleAdultDiscover(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.adultProvider(w)
	if !ok {
		return
	}
	query, err := parseSceneQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	result, err := provider.SearchScenes(ctx, query)
	if err != nil {
		// A filter this endpoint cannot express is the CALLER's problem, not
		// an upstream failure: the provider refused rather than answering the
		// wider question, and the rail has a control it must stop offering.
		// The filter is named; the value never is.
		var unsupported *core.SceneFilterUnsupportedError
		if errors.As(err, &unsupported) {
			writeError(w, http.StatusBadRequest,
				"the metadata endpoint cannot filter scenes by "+unsupported.Filter)
			return
		}
		s.writeAdultProviderError(w, r, "scene search", err)
		return
	}
	if result == nil {
		result = &core.ScenePage{Page: 1}
	}

	state, err := s.sceneState(ctx, result.Scenes)
	if err != nil {
		s.writeStoreError(w, "read library state", err)
		return
	}
	out := make([]sceneMetaJSON, 0, len(result.Scenes))
	for _, scene := range result.Scenes {
		out = append(out, state.decorate(scene))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page":     result.Page,
		"per_page": result.PerPage,
		"total":    result.Total,
		"scenes":   out,
	})
}

// sceneLibraryState is what Caravan knows about a page of provider scenes:
// which it already has an episode row for, and which have a pending request. It
// is libraryState's scene twin and is built in two queries per response rather
// than two per row.
type sceneLibraryState struct {
	episodes map[string]int64
	pending  map[string]bool
}

func (s *server) sceneState(ctx context.Context, scenes []core.SceneMeta) (*sceneLibraryState, error) {
	ids := make([]string, 0, len(scenes))
	for _, scene := range scenes {
		ids = append(ids, scene.StashID)
	}
	episodes, err := s.st.EpisodeIDsByStashID(ctx, ids)
	if err != nil {
		return nil, err
	}
	requests, err := s.st.ListPendingRequestsForStashIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	pending := make(map[string]bool, len(requests))
	for _, req := range requests {
		pending[req.StashID] = true
	}
	return &sceneLibraryState{episodes: episodes, pending: pending}, nil
}

func (st *sceneLibraryState) decorate(scene core.SceneMeta) sceneMetaJSON {
	performers := make([]string, 0, len(scene.Performers))
	for _, p := range scene.Performers {
		name := p.As
		if name == "" {
			name = p.Name
		}
		if name != "" {
			performers = append(performers, name)
		}
	}
	libraryID := st.episodes[scene.StashID]
	return sceneMetaJSON{
		MediaType:   MediaTypeScene,
		StashID:     scene.StashID,
		SiteStashID: scene.SiteStashID,
		SiteName:    scene.SiteName,
		Title:       scene.Title,
		Overview:    scene.Overview,
		Date:        jsonDate(scene.Date),
		Duration:    scene.Duration,
		Performers:  performers,
		URL:         scene.URL,
		ImageURL:    scene.ImageURL,
		InLibrary:   libraryID != 0,
		LibraryID:   libraryID,
		Requested:   st.pending[scene.StashID],
	}
}

// siteIDsByStashID reports which of the provider's hits are already in the
// library. It reads the adult series once rather than querying per hit; a
// household's site list is short, and the alternative is a query per row.
func (s *server) siteIDsByStashID(ctx context.Context, sites []core.SiteMeta) (map[string]int64, error) {
	out := map[string]int64{}
	if len(sites) == 0 {
		return out, nil
	}
	held, err := s.st.ListSeriesByKind(ctx, core.SeriesKindAdult)
	if err != nil {
		return nil, err
	}
	byStash := make(map[string]int64, len(held))
	for _, sr := range held {
		if sr.StashID != "" {
			byStash[sr.StashID] = sr.ID
		}
	}
	for _, site := range sites {
		if id := byStash[site.StashID]; id != 0 {
			out[site.StashID] = id
		}
	}
	return out, nil
}

// adultUserJSON is one account and its grant, for the member-access card.
//
// It is a DTO of its own rather than a field added to userJSON so that
// GET /users — which is reachable whether or not the module exists — carries no
// adult field at all. A `"adult_access": false` on every row of an install that
// has never turned the module on is precisely the trace this phase promises not
// to leave.
type adultUserJSON struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	// Granted is the account's own adult_access flag. It is false on an admin
	// row and meaningless there: AlwaysGranted is what the card renders.
	Granted bool `json:"granted"`
	// AlwaysGranted says the account reaches the module through its role rather
	// than through a grant, which is true of every admin (core.AdultVisible).
	// The client shows "Always has access" instead of a toggle.
	AlwaysGranted bool `json:"always_granted"`
}

func adultUserDTO(u core.User) adultUserJSON {
	return adultUserJSON{
		ID:            u.ID,
		Username:      u.Username,
		Role:          u.Role,
		Granted:       u.AdultAccess,
		AlwaysGranted: u.Role == core.RoleAdmin,
	}
}

// handleListAdultUsers answers the member-access card. Admin-only by absence
// from memberAllowed — a member who could read this would learn the household's
// account roster, which is the one thing a failed login goes out of its way not
// to confirm.
func (s *server) handleListAdultUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		s.writeStoreError(w, "list users", err)
		return
	}
	out := make([]adultUserJSON, 0, len(users))
	for _, u := range users {
		out = append(out, adultUserDTO(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// adultAccessRequest is the body of PUT /adult/users/{id}/access.
type adultAccessRequest struct {
	// Granted is a pointer so an absent field is a client bug rather than a
	// silent revoke, exactly as monitorRequest treats Monitored.
	Granted *bool `json:"granted"`
}

// handleSetAdultAccess grants or revokes one account's access.
//
// Revoking takes effect on the account's very next request without a logout:
// requireAuth reads the row every time (see requestUser.AdultAccess), so there
// is no session to invalidate and no window in which a revoked grant is still
// good. That is why this does not touch the session store, unlike a password
// reset — the grant is not a credential.
func (s *server) handleSetAdultAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body adultAccessRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Granted == nil {
		writeError(w, http.StatusBadRequest, "granted is required")
		return
	}

	ctx := r.Context()
	if err := s.st.SetUserAdultAccess(ctx, id, *body.Granted); err != nil {
		s.writeStoreError(w, "set adult access", err)
		return
	}
	user, err := s.st.GetUser(ctx, id)
	if err != nil {
		s.writeStoreError(w, "read user", err)
		return
	}
	writeJSON(w, http.StatusOK, adultUserDTO(*user))
}

// adultProvider returns the configured stash-box provider, writing the 503
// itself when there is none. A missing credential is configuration, not a
// retry, so it reads the same way a missing TMDB key does on GET /search.
//
// The code matters as much as the status: the SPA reads an UNCODED 503 as a
// missing TMDB key, so naming the adult credential here is what keeps a
// stash-box failure from being reported as a TMDB one.
func (s *server) adultProvider(w http.ResponseWriter) (core.AdultMetadataProvider, bool) {
	provider := s.mgr.AdultMetadata()
	if provider == nil {
		writeCodedError(w, http.StatusServiceUnavailable, CodeAdultCredentialAbsent,
			"no adult metadata provider configured")
		return nil, false
	}
	return provider, true
}

// writeAdultProviderError reports an upstream failure as a bad gateway, the way
// GET /search and the discover endpoints do. The provider's own message is
// logged and not returned: a stash-box error can quote the query, and the query
// is the one string on this surface nobody wants echoed into a shared log or a
// browser's error console.
func (s *server) writeAdultProviderError(w http.ResponseWriter, r *http.Request, what string, err error) {
	// A canceled search is the typeahead working — every keystroke aborts the
	// previous request — so it logs as debug and closes out as 499, not as an
	// upstream failure.
	if clientGone(r) {
		s.log.Debug("adult provider request abandoned by the caller", "what", what, "error", err)
		writeError(w, statusClientClosedRequest, "client closed request")
		return
	}
	s.log.Error("adult provider request failed", "what", what, "error", err)
	writeError(w, http.StatusBadGateway, "adult provider request failed")
}

func siteDTO(sr core.Series) siteJSON {
	return siteJSON{
		ID:               sr.ID,
		Title:            sr.Title,
		StashID:          sr.StashID,
		SortTitle:        sr.SortTitle,
		Overview:         sr.Overview,
		Path:             sr.Path,
		PosterPath:       sr.PosterPath,
		PosterURL:        sr.PosterURL,
		Monitored:        sr.Monitored,
		QualityProfileID: sr.QualityProfileID,
		LibraryID:        sr.LibraryID,
		AddedAt:          jsonTime(sr.AddedAt),
		UpdatedAt:        jsonTime(sr.UpdatedAt),
	}
}

func sceneDTO(e core.Episode, file *mediaFileJSON) sceneJSON {
	out := sceneJSON{
		ID:          e.ID,
		SeriesID:    e.SeriesID,
		Year:        e.SeasonNumber,
		Number:      e.EpisodeNumber,
		StashID:     e.StashID,
		Title:       e.Title,
		Overview:    e.Overview,
		ReleaseDate: jsonDate(e.AirDate),
		Monitored:   e.Monitored,
		Performers:  []string{},
		File:        file,
	}
	if e.Scene != nil {
		out.Studio = e.Scene.Studio
		out.URL = e.Scene.URL
		if e.Scene.Performers != nil {
			out.Performers = e.Scene.Performers
		}
	}
	return out
}
