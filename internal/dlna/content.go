package dlna

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// Well-known object ids. "0" is the root, fixed by the ContentDirectory
// specification; "-1" is the parent every client expects the root to claim.
const (
	rootID       = "0"
	rootParentID = "-1"
	moviesID     = "movies"
	tvID         = "tv"
	// adultID is the Adult shelf (PLAN phase 9 task 6). It reaches the tree
	// through exactly the machinery the other two do — one row in `libraries`,
	// one dlna_visible flag — and is absent from the tree for the ordinary
	// reason that its flag is off, which is how the row is born.
	adultID = "adult"
)

// Object-id prefixes for the rows behind the tree. Ids are opaque strings to
// the client, so they encode exactly what it takes to answer a BrowseMetadata
// on them without a search: a movie item carries its file, an episode item
// carries both the episode it appears under and the file that plays it,
// because one file can appear under two episodes (S01E01E02, SPEC §7).
// The adult shelf carries prefixes of its own rather than reusing "s:" and
// "e:". A site is a series row, so one id space would make "s:12" ambiguous —
// and hidden() has to answer "may this object be served" from the id ALONE,
// before any row is read, or a client holding a cached id could keep browsing a
// shelf the owner took down. Separate prefixes keep that answer a pure function
// of the string. None of the four is a prefix of another, which is what lets
// the switches below match in any order.
const (
	movieItemPrefix   = "m:"
	seriesPrefix      = "s:"
	episodeItemPrefix = "e:"
	sitePrefix        = "as:"
	sceneItemPrefix   = "ae:"
)

// shelf is one top-level container holding series rows: TV and Adult. The two
// differ in which library owns them, which series kind they list, how their
// object ids are spelled, and how a season and an episode are named — and in
// nothing else, which is why they are one code path parameterised rather than
// two that would drift.
type shelf struct {
	containerID string
	title       string
	// libraryKind is the `libraries` row whose dlna_visible decides whether
	// this shelf is advertised at all. It is the whole of the phase-8
	// integration: there is no adult-specific visibility rule anywhere.
	libraryKind string
	seriesKind  string
	// seriesPrefix and episodePrefix spell this shelf's object ids.
	seriesPrefix  string
	episodePrefix string
	// season names a season container. A television season is "Season 3"; a
	// site's is its release year, because that is what the season number IS
	// (PLAN phase 9 task 3).
	season func(core.Season) string
	// episode names a playable item.
	episode func(*core.Series, core.Episode) string
}

var (
	tvShelf = shelf{
		containerID:   tvID,
		title:         "TV",
		libraryKind:   core.LibraryKindTV,
		seriesKind:    core.SeriesKindTV,
		seriesPrefix:  seriesPrefix,
		episodePrefix: episodeItemPrefix,
		season:        seasonTitle,
		episode:       episodeTitle,
	}
	adultShelf = shelf{
		containerID:   adultID,
		title:         "Adult",
		libraryKind:   core.LibraryKindAdult,
		seriesKind:    core.SeriesKindAdult,
		seriesPrefix:  sitePrefix,
		episodePrefix: sceneItemPrefix,
		season:        yearTitle,
		episode:       sceneTitle,
	}
	shelves = []shelf{tvShelf, adultShelf}
)

// shelfOf resolves the shelf an object id belongs to. It is a pure function of
// the string, deliberately — see the prefix constants above.
func shelfOf(objectID string) (shelf, bool) {
	for _, sh := range shelves {
		if objectID == sh.containerID ||
			strings.HasPrefix(objectID, sh.seriesPrefix) ||
			strings.HasPrefix(objectID, sh.episodePrefix) {
			return sh, true
		}
	}
	return shelf{}, false
}

func (sh shelf) seriesObjectID(seriesID int64) string {
	return sh.seriesPrefix + strconv.FormatInt(seriesID, 10)
}

func (sh shelf) seasonObjectID(seriesID int64, season int) string {
	return sh.seriesObjectID(seriesID) + ":" + strconv.Itoa(season)
}

func (sh shelf) episodeObjectID(episodeID, fileID int64) string {
	return sh.episodePrefix + strconv.FormatInt(episodeID, 10) + ":" + strconv.FormatInt(fileID, 10)
}

// errNoObject is what an unknown object id resolves to; the SOAP layer turns it
// into ContentDirectory error 701.
var errNoObject = errors.New("dlna: no such object")

// urls builds the absolute URLs a DIDL document hands the client.
//
// The origin comes from the request's Host header rather than from
// configuration: the client reached us at some address that works for it — the
// LAN IP from SSDP, a hostname, a reverse proxy — and every URL we hand back
// has to be on that same address or the TV will fetch from somewhere it cannot
// reach.
type urls struct {
	origin string
}

// media is the playable URL for a library file. The extension is carried in the
// path because several renderers pick their demuxer from the URL before they
// look at Content-Type.
func (u urls) media(id int64, filePath string) string {
	return fmt.Sprintf("%s%s/media/%d%s", u.origin, MountPath, id, path.Ext(filePath))
}

// art points at the existing artwork endpoint rather than a DLNA-specific one:
// GET /api/v1/images already serves storage-root-relative posters with the same
// confinement rules, and a second copy of that would be a second place to get
// path safety wrong.
func (u urls) art(rel string) string {
	if rel == "" {
		return ""
	}
	segments := strings.Split(rel, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return u.origin + "/api/v1/images/" + strings.Join(segments, "/")
}

// libraryKindOf maps an object id onto the library kind whose subtree it lives
// in, so one lookup answers "may this object be served" for a container and for
// everything under it. The false return is for the root, which belongs to no
// single library.
func libraryKindOf(objectID string) (string, bool) {
	if objectID == moviesID || strings.HasPrefix(objectID, movieItemPrefix) {
		return core.LibraryKindMovie, true
	}
	if sh, ok := shelfOf(objectID); ok {
		return sh.libraryKind, true
	}
	return "", false
}

// visibleLibraries reports which library kinds the content tree may expose
// (PLAN phase 8 task 6). A library with dlna_visible off is not advertised at
// all: its container is absent from the root and every id beneath it resolves
// to "no such object", so a client cannot keep browsing a shelf the owner took
// down by holding on to a cached id.
//
// This is a visibility switch, not an access control: the media endpoint still
// serves any file by id, exactly as it did before libraries existed. What the
// user is choosing here is which shelves the television lists.
//
// The adult library carries a second condition: the module's own master switch.
// Disabling the module deletes nothing (store.SetAdultEnabled), so its
// dlna_visible survives an off — and a flag left on would keep the shelf on
// every television on the LAN while the API, the SPA, the calendar and the
// wanted list had all gone quiet. "Off" has to mean off on the one surface with
// no accounts, so the AND lives here rather than as a write to the row: the
// owner's sharing decision is remembered and simply does not apply while the
// module is off.
func (s *Service) visibleLibraries(ctx context.Context) (map[string]bool, error) {
	libraries, err := s.st.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	adultEnabled, err := s.st.AdultEnabled(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(libraries))
	for _, l := range libraries {
		visible := l.DLNAVisible
		if l.Kind == core.LibraryKindAdult && !adultEnabled {
			visible = false
		}
		out[l.Kind] = visible
	}
	return out, nil
}

// hidden reports whether objectID belongs to a library that is not advertised.
func (s *Service) hidden(ctx context.Context, objectID string) (bool, error) {
	kind, ok := libraryKindOf(objectID)
	if !ok {
		return false, nil
	}
	visible, err := s.visibleLibraries(ctx)
	if err != nil {
		return false, err
	}
	return !visible[kind], nil
}

// children returns the direct children of objectID as a DIDL document.
func (s *Service) children(ctx context.Context, u urls, objectID string) (*didlLite, error) {
	if hidden, err := s.hidden(ctx, objectID); err != nil {
		return nil, err
	} else if hidden {
		return nil, errNoObject
	}
	if objectID == rootID {
		return s.rootChildren(ctx, u)
	}
	if objectID == moviesID {
		return s.movieChildren(ctx, u)
	}
	if strings.HasPrefix(objectID, movieItemPrefix) {
		// Items have no children. An empty document is the correct answer, not
		// an error: clients probe leaves this way.
		return newDIDL(), nil
	}
	sh, ok := shelfOf(objectID)
	if !ok {
		return nil, errNoObject
	}
	switch {
	case objectID == sh.containerID:
		return s.seriesChildren(ctx, u, sh)
	case strings.HasPrefix(objectID, sh.seriesPrefix):
		seriesID, season, hasSeason, err := sh.parseSeriesID(objectID)
		if err != nil {
			return nil, err
		}
		if hasSeason {
			return s.episodeChildren(ctx, u, sh, seriesID, season)
		}
		return s.seasonChildren(ctx, u, sh, seriesID)
	default:
		return newDIDL(), nil
	}
}

// metadata returns objectID's own description as a one-object DIDL document.
func (s *Service) metadata(ctx context.Context, u urls, objectID string) (*didlLite, error) {
	if hidden, err := s.hidden(ctx, objectID); err != nil {
		return nil, err
	} else if hidden {
		return nil, errNoObject
	}
	out := newDIDL()
	switch {
	case objectID == rootID:
		kids, err := s.rootChildren(ctx, u)
		if err != nil {
			return nil, err
		}
		name, err := s.friendlyName(ctx)
		if err != nil {
			return nil, err
		}
		out.Containers = []didlContainer{container(rootID, rootParentID, name, kids.count(), "")}
	case objectID == moviesID:
		kids, err := s.children(ctx, u, objectID)
		if err != nil {
			return nil, err
		}
		out.Containers = []didlContainer{container(objectID, rootID, "Movies", kids.count(), "")}
	case strings.HasPrefix(objectID, movieItemPrefix):
		item, err := s.movieItemMetadata(ctx, u, objectID)
		if err != nil {
			return nil, err
		}
		out.Items = []didlItem{*item}
	default:
		sh, ok := shelfOf(objectID)
		if !ok {
			return nil, errNoObject
		}
		switch {
		case objectID == sh.containerID:
			kids, err := s.children(ctx, u, objectID)
			if err != nil {
				return nil, err
			}
			out.Containers = []didlContainer{container(objectID, rootID, sh.title, kids.count(), "")}
		case strings.HasPrefix(objectID, sh.seriesPrefix):
			c, err := s.seriesMetadata(ctx, u, sh, objectID)
			if err != nil {
				return nil, err
			}
			out.Containers = []didlContainer{*c}
		default:
			item, err := s.episodeItemMetadata(ctx, u, sh, objectID)
			if err != nil {
				return nil, err
			}
			out.Items = []didlItem{*item}
		}
	}
	return out, nil
}

// friendlyName is the root container's title: the device's own name, so a
// client that shows the root as a folder shows "Caravan" rather than "0".
func (s *Service) friendlyName(ctx context.Context) (string, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return "", err
	}
	return cfg.FriendlyName, nil
}

func (s *Service) rootChildren(ctx context.Context, u urls) (*didlLite, error) {
	visible, err := s.visibleLibraries(ctx)
	if err != nil {
		return nil, err
	}
	out := newDIDL()
	if visible[core.LibraryKindMovie] {
		movies, err := s.movieChildren(ctx, u)
		if err != nil {
			return nil, err
		}
		out.Containers = append(out.Containers, container(moviesID, rootID, "Movies", movies.count(), ""))
	}
	// One loop over the shelves, so the Adult container is advertised by the
	// same rule the TV one is and by nothing else. There is no branch here that
	// mentions the adult module: if its library row says dlna_visible, it is on
	// the shelf; if it does not, it is absent — and the row is created with the
	// flag off (store.SetAdultEnabled), so enabling the module changes nothing
	// about what the LAN can see until the owner makes that second decision.
	for _, sh := range shelves {
		if !visible[sh.libraryKind] {
			continue
		}
		kids, err := s.seriesChildren(ctx, u, sh)
		if err != nil {
			return nil, err
		}
		out.Containers = append(out.Containers,
			container(sh.containerID, rootID, sh.title, kids.count(), ""))
	}
	return out, nil
}

// movieChildren lists one item per movie file.
//
// Movies with no file are absent: this is a playback surface, and a container
// entry that cannot be played is a dead end on a remote control. A movie that
// somehow has two files gets two entries, disambiguated by filename, rather
// than one entry that silently picks a winner.
func (s *Service) movieChildren(ctx context.Context, u urls) (*didlLite, error) {
	movies, err := s.st.ListMovies(ctx)
	if err != nil {
		return nil, err
	}
	files, err := s.st.ListMediaFiles(ctx)
	if err != nil {
		return nil, err
	}
	byMovie := map[int64][]core.MediaFile{}
	for _, f := range files {
		if f.MovieID != 0 {
			byMovie[f.MovieID] = append(byMovie[f.MovieID], f)
		}
	}

	out := newDIDL()
	for _, m := range movies {
		owned := byMovie[m.ID]
		for _, f := range owned {
			out.Items = append(out.Items, movieItem(u, m, f, len(owned) > 1))
		}
	}
	return out, nil
}

func movieItem(u urls, m core.Movie, f core.MediaFile, disambiguate bool) didlItem {
	title := titleWithYear(m.Title, m.Year)
	if disambiguate {
		title += " — " + path.Base(f.Path)
	}
	return item(movieItemPrefix+strconv.FormatInt(f.ID, 10), moviesID, title, u.art(m.PosterPath), u, f)
}

// seriesChildren lists one shelf's series as containers of seasons.
//
// Unlike movies these are not filtered by file presence: a series is a shelf,
// and answering the count with a query per series would make browsing the TV
// folder cost a query per show. An empty season list is a visible, honest
// "nothing here yet".
//
// The kind filter is an exposure boundary, not a tidy-up. A site is stored as a
// series (PLAN phase 9 task 3), so an unfiltered list would hang the adult
// library inside the TELEVISION container — where the adult library's own
// dlna_visible flag has no say, because it is not that library's container.
func (s *Service) seriesChildren(ctx context.Context, u urls, sh shelf) (*didlLite, error) {
	all, err := s.st.ListSeriesByKind(ctx, sh.seriesKind)
	if err != nil {
		return nil, err
	}
	out := newDIDL()
	for _, sr := range all {
		seasons, err := s.st.ListSeasons(ctx, sr.ID)
		if err != nil {
			return nil, err
		}
		out.Containers = append(out.Containers, container(
			sh.seriesObjectID(sr.ID), sh.containerID,
			titleWithYear(sr.Title, sr.Year), len(seasons), u.art(sr.PosterPath)))
	}
	return out, nil
}

// seasonChildren lists a series' seasons, each counting the playable files
// beneath it rather than its episode rows: a season a client opens to find
// empty is worse than one that says it holds three of ten episodes.
func (s *Service) seasonChildren(ctx context.Context, u urls, sh shelf, seriesID int64) (*didlLite, error) {
	sr, err := s.insistShelfSeries(ctx, sh, seriesID)
	if err != nil {
		return nil, err
	}
	seasons, err := s.st.ListSeasons(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	pairs, err := s.st.ListEpisodeMediaFilesForSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	counts := map[int]int{}
	for _, p := range pairs {
		counts[p.SeasonNumber]++
	}

	out := newDIDL()
	for _, season := range seasons {
		art := season.PosterPath
		if art == "" {
			art = sr.PosterPath
		}
		out.Containers = append(out.Containers, container(
			sh.seasonObjectID(seriesID, season.Number), sh.seriesObjectID(seriesID),
			sh.season(season), counts[season.Number], u.art(art)))
	}
	return out, nil
}

// episodeChildren lists the playable files of one season.
func (s *Service) episodeChildren(ctx context.Context, u urls, sh shelf, seriesID int64, season int) (*didlLite, error) {
	sr, err := s.insistShelfSeries(ctx, sh, seriesID)
	if err != nil {
		return nil, err
	}
	episodes, err := s.st.ListEpisodes(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	byID := map[int64]core.Episode{}
	for _, e := range episodes {
		byID[e.ID] = e
	}
	pairs, err := s.st.ListEpisodeMediaFilesForSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}

	out := newDIDL()
	for _, p := range pairs {
		if p.SeasonNumber != season {
			continue
		}
		out.Items = append(out.Items, episodeItem(u, sh, sr, byID[p.EpisodeID], p.File))
	}
	return out, nil
}

func episodeItem(u urls, sh shelf, sr *core.Series, e core.Episode, f core.MediaFile) didlItem {
	return item(sh.episodeObjectID(e.ID, f.ID), sh.seasonObjectID(sr.ID, e.SeasonNumber),
		sh.episode(sr, e), u.art(sr.PosterPath), u, f)
}

// insistShelfSeries reads a series and refuses one that belongs to a different
// shelf.
//
// It is the id space's integrity check, and it is an exposure boundary rather
// than tidiness: "s:12" and "as:12" address the SAME row, so without this a
// client could reach a site through the television shelf's prefix — whose
// dlna_visible flag says nothing about the adult library — and the visibility
// decision the owner made would be bypassed by a two-character edit to a URL.
func (s *Service) insistShelfSeries(ctx context.Context, sh shelf, seriesID int64) (*core.Series, error) {
	sr, err := s.st.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, notFound(err)
	}
	if sr.Kind != sh.seriesKind {
		return nil, errNoObject
	}
	return sr, nil
}

func (s *Service) seriesMetadata(ctx context.Context, u urls, sh shelf, objectID string) (*didlContainer, error) {
	seriesID, season, hasSeason, err := sh.parseSeriesID(objectID)
	if err != nil {
		return nil, err
	}
	sr, err := s.insistShelfSeries(ctx, sh, seriesID)
	if err != nil {
		return nil, err
	}
	if !hasSeason {
		seasons, err := s.st.ListSeasons(ctx, seriesID)
		if err != nil {
			return nil, err
		}
		c := container(objectID, sh.containerID,
			titleWithYear(sr.Title, sr.Year), len(seasons), u.art(sr.PosterPath))
		return &c, nil
	}

	kids, err := s.episodeChildren(ctx, u, sh, seriesID, season)
	if err != nil {
		return nil, err
	}
	c := container(objectID, sh.seriesObjectID(seriesID),
		sh.season(core.Season{Number: season}), kids.count(), u.art(sr.PosterPath))
	return &c, nil
}

func (s *Service) movieItemMetadata(ctx context.Context, u urls, objectID string) (*didlItem, error) {
	fileID, err := strconv.ParseInt(strings.TrimPrefix(objectID, movieItemPrefix), 10, 64)
	if err != nil {
		return nil, errNoObject
	}
	f, err := s.st.GetMediaFile(ctx, fileID)
	if err != nil {
		return nil, notFound(err)
	}
	m, err := s.st.GetMovie(ctx, f.MovieID)
	if err != nil {
		return nil, notFound(err)
	}
	owned, err := s.st.ListMediaFilesForMovie(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	out := movieItem(u, *m, *f, len(owned) > 1)
	return &out, nil
}

func (s *Service) episodeItemMetadata(ctx context.Context, u urls, sh shelf, objectID string) (*didlItem, error) {
	rest := strings.TrimPrefix(objectID, sh.episodePrefix)
	epRaw, fileRaw, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, errNoObject
	}
	episodeID, err := strconv.ParseInt(epRaw, 10, 64)
	if err != nil {
		return nil, errNoObject
	}
	fileID, err := strconv.ParseInt(fileRaw, 10, 64)
	if err != nil {
		return nil, errNoObject
	}

	e, err := s.st.GetEpisode(ctx, episodeID)
	if err != nil {
		return nil, notFound(err)
	}
	f, err := s.st.GetMediaFile(ctx, fileID)
	if err != nil {
		return nil, notFound(err)
	}
	sr, err := s.insistShelfSeries(ctx, sh, e.SeriesID)
	if err != nil {
		return nil, err
	}
	out := episodeItem(u, sh, sr, *e, *f)
	return &out, nil
}

// container and item are the two DIDL constructors, so restricted and the
// class strings are set in exactly one place each.
func container(id, parentID, title string, childCount int, art string) didlContainer {
	return didlContainer{
		ID: id, ParentID: parentID, Restricted: 1, ChildCount: childCount,
		Title: title, Class: classContainer, AlbumArtURI: art,
	}
}

func item(id, parentID, title, art string, u urls, f core.MediaFile) didlItem {
	return didlItem{
		ID: id, ParentID: parentID, Restricted: 1,
		Title: title, Class: classVideoItem, AlbumArtURI: art,
		Res: didlRes{
			ProtocolInfo: protocolInfo(f.Path),
			Size:         f.Size,
			URL:          u.media(f.ID, f.Path),
		},
	}
}

// parseSeriesID splits "s:12" and "s:12:3" into their parts (and "as:12",
// "as:12:2022" on the adult shelf).
func (sh shelf) parseSeriesID(objectID string) (seriesID int64, season int, hasSeason bool, err error) {
	rest := strings.TrimPrefix(objectID, sh.seriesPrefix)
	idRaw, seasonRaw, hasSeason := strings.Cut(rest, ":")
	seriesID, err = strconv.ParseInt(idRaw, 10, 64)
	if err != nil {
		return 0, 0, false, errNoObject
	}
	if hasSeason {
		season, err = strconv.Atoi(seasonRaw)
		if err != nil {
			return 0, 0, false, errNoObject
		}
	}
	return seriesID, season, hasSeason, nil
}

// notFound maps a missing row onto errNoObject so a stale object id a client
// cached becomes ContentDirectory's "no such object" instead of a 500.
func notFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errNoObject
	}
	return err
}

func titleWithYear(title string, year int) string {
	if year == 0 {
		return title
	}
	return fmt.Sprintf("%s (%d)", title, year)
}

func seasonTitle(season core.Season) string {
	if season.Number == 0 {
		return "Specials"
	}
	return fmt.Sprintf("Season %d", season.Number)
}

// episodeTitle names an episode item the way the library names its file:
// series first, then the code, then the episode's own title. The series name
// is not decoration — clients that fetch their own metadata (Infuse's folder
// browsing, for one) parse the item title for it, and a bare "S01E01 - …" is
// unmatchable. Within a season folder everything shares the prefix, so lists
// still sort by the code.
func episodeTitle(sr *core.Series, e core.Episode) string {
	label := fmt.Sprintf("%s - S%02dE%02d", titleWithYear(sr.Title, sr.Year), e.SeasonNumber, e.EpisodeNumber)
	if e.Title != "" {
		label += " - " + e.Title
	}
	return label
}

// yearTitle names a site's season container. A site's season number IS its
// release year (PLAN phase 9 task 3), so "Season 2022" would be nonsense and
// "Specials" — the number-zero case television has — cannot arise: a scene with
// no release date is never filed at all.
func yearTitle(season core.Season) string {
	return strconv.Itoa(season.Number)
}

// sceneTitle names a scene item after its release date rather than an SxxEyy
// code, matching the filename the organizer writes (internal/library: a
// "S2022E01" tag is unreadable by the release parser, whose season is one or
// two digits). What a television shows and what is on disk therefore agree.
func sceneTitle(sr *core.Series, e core.Episode) string {
	label := sr.Title
	if !e.AirDate.IsZero() {
		label += " - " + e.AirDate.UTC().Format("2006-01-02")
	}
	if e.Title != "" {
		label += " - " + e.Title
	}
	return label
}
