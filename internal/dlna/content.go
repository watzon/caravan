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
//
// moviesID, tvID and adultID name the DEFAULT library of their kind rather
// than the kind itself: an install may hold several libraries per kind (0022),
// and every one of them gets a container of its own. The default keeps the
// legacy id because a television caches object ids for as long as it likes,
// and an upgrade that renamed the three containers everybody already has would
// empty every cached library on the LAN.
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
	// libraryPrefix spells the container id of every OTHER library:
	// "lib:<libraries.id>". The row's id is the only stable name a library
	// has — its kind is shared and its name is editable.
	libraryPrefix = "lib:"
)

// legacyContainerKind maps the three inherited container ids onto the kind
// whose default library they name, and legacyContainerTitle onto the title
// that library keeps regardless of what the row is called. Both directions of
// the mapping live here so they cannot drift.
var (
	legacyContainerKind = map[string]string{
		moviesID: core.LibraryKindMovie,
		tvID:     core.LibraryKindTV,
		adultID:  core.LibraryKindAdult,
	}
	legacyContainerTitle = map[string]string{
		core.LibraryKindMovie: "Movies",
		core.LibraryKindTV:    "TV",
		core.LibraryKindAdult: "Adult",
	}
	legacyContainerID = map[string]string{
		core.LibraryKindMovie: moviesID,
		core.LibraryKindTV:    tvID,
		core.LibraryKindAdult: adultID,
	}
)

// libraryContainerID is the root container one library is browsed through.
func libraryContainerID(lib core.Library) string {
	if id, ok := legacyContainerID[lib.Kind]; ok && lib.IsDefault {
		return id
	}
	return libraryPrefix + strconv.FormatInt(lib.ID, 10)
}

// libraryTitle is what that container is called. The default library of each
// kind keeps the inherited title for the reason it keeps the inherited id: it
// is the shelf every existing client already has.
func libraryTitle(lib core.Library) string {
	if title, ok := legacyContainerTitle[lib.Kind]; ok && lib.IsDefault {
		return title
	}
	return lib.Name
}

// ownedBy reports whether an item row belongs to this library. A zero
// library_id is an item from before 0022, or one whose import never named a
// target: it belongs wherever a by-kind lookup would send it, which is the
// kind's default library (see core.Movie.LibraryID).
func ownedBy(libraryID int64, lib core.Library) bool {
	if libraryID == 0 {
		return lib.IsDefault
	}
	return libraryID == lib.ID
}

// Object-id prefixes for the rows behind the tree. Ids are opaque strings to
// the client, so they encode exactly what it takes to answer a BrowseMetadata
// on them without a search: a movie item carries its file, an episode item
// carries both the episode it appears under and the file that plays it,
// because one file can appear under two episodes (S01E01E02, SPEC §7).
//
// They deliberately do NOT carry a library. An install may hold several
// libraries of one kind, and an id that spelled out which one would break the
// moment an item was moved between them — the row already knows its owner. So
// the exposure promise is no longer "hidden() is a pure function of the
// string": it is that every id under a hidden library answers 701, resolved
// through the OWNING row (a movie item's movie, a series/season/episode
// object's series). hidden() reads that row before it answers, which is what
// keeps a client's cached id from outliving the owner's decision to stop
// sharing.
//
// The adult shelf still carries prefixes of its own rather than reusing "s:"
// and "e:". A site is a series row, so one id space would make "s:12"
// ambiguous, and the two id spaces are an exposure boundary in their own right
// (see insistShelfSeries). None of the five is a prefix of another, which is
// what lets the switches below match in any order.
const (
	movieItemPrefix   = "m:"
	seriesPrefix      = "s:"
	episodeItemPrefix = "e:"
	sitePrefix        = "as:"
	sceneItemPrefix   = "ae:"
)

// shelf is one library's top-level container, holding series rows: a TV
// library or an Adult one. Shelves differ in which library owns them, which
// series kind they list, how their object ids are spelled, and how a season
// and an episode are named — and in nothing else, which is why they are one
// code path parameterised rather than two that would drift.
type shelf struct {
	containerID string
	title       string
	// library is the `libraries` row this shelf IS: its dlna_visible decides
	// whether the shelf is advertised, and its id decides which series hang
	// under it. There is no adult-specific visibility rule anywhere.
	library    core.Library
	seriesKind string
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

// idSpaces are the two shelf templates: everything about a shelf that depends
// on the library's KIND rather than on which library it is. shelfFor stamps a
// library onto one of them; shelfSpaceOf recovers one from an object id, which
// is the only thing that can be read out of the string alone.
var (
	tvIDSpace = shelf{
		seriesKind:    core.SeriesKindTV,
		seriesPrefix:  seriesPrefix,
		episodePrefix: episodeItemPrefix,
		season:        seasonTitle,
		episode:       episodeTitle,
	}
	adultIDSpace = shelf{
		seriesKind:    core.SeriesKindAdult,
		seriesPrefix:  sitePrefix,
		episodePrefix: sceneItemPrefix,
		season:        yearTitle,
		episode:       sceneTitle,
	}
	idSpaces = []shelf{tvIDSpace, adultIDSpace}
)

// shelfFor builds the shelf a tv or adult library is browsed through. A movie
// library is not a shelf — it holds items directly, not series — so it is the
// false return.
func shelfFor(lib core.Library) (shelf, bool) {
	var sh shelf
	switch lib.Kind {
	case core.LibraryKindTV:
		sh = tvIDSpace
	case core.LibraryKindAdult:
		sh = adultIDSpace
	default:
		return shelf{}, false
	}
	sh.library = lib
	sh.containerID = libraryContainerID(lib)
	sh.title = libraryTitle(lib)
	return sh, true
}

// shelfSpaceOf resolves which id space an object id is spelled in. The result
// carries no library: it is the template, good for parsing the id and for the
// kind check that keeps the two spaces apart, and nothing else.
func shelfSpaceOf(objectID string) (shelf, bool) {
	for _, sh := range idSpaces {
		if strings.HasPrefix(objectID, sh.seriesPrefix) ||
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

// visibility is the resolved answer to "may this library's content be
// advertised", for every library at once (PLAN phase 8 task 6). A library with
// dlna_visible off is not advertised at all: its container is absent from the
// root and every id beneath it resolves to "no such object", so a client
// cannot keep browsing a shelf the owner took down by holding on to a cached
// id.
//
// The same policy gates direct media URLs, so a renderer cannot keep playing
// files from a shelf the owner has hidden by holding on to a cached URL.
type visibility struct {
	// libraries is every library in ListLibraries order, so the root advertises
	// its containers in a stable, seeded-first order.
	libraries []core.Library
	byID      map[int64]bool
	// defaults is each kind's default library id, which is what an item's zero
	// library_id resolves through.
	defaults map[string]int64
}

// library answers for one item's owner. libID 0 means the item names no
// library and belongs to its kind's default; a libID naming a row that is gone
// answers false, which is the same fail-closed direction ownedBy takes — such
// an item hangs under no container, so no id of it may be served either.
func (v visibility) library(libID int64, kind string) bool {
	if libID == 0 {
		libID = v.defaults[kind]
	}
	return v.byID[libID]
}

// visible lists the libraries the root advertises, in library order.
func (v visibility) visible() []core.Library {
	out := make([]core.Library, 0, len(v.libraries))
	for _, l := range v.libraries {
		if v.byID[l.ID] {
			out = append(out, l)
		}
	}
	return out
}

// visibility resolves every library's advertised state.
//
// The adult rule applies per adult library and carries a second condition: the
// module's own master switch. Disabling the module deletes nothing
// (store.SetAdultEnabled), so a dlna_visible survives an off — and a flag left
// on would keep the shelf on every television on the LAN while the API, the
// SPA, the calendar and the wanted list had all gone quiet. "Off" has to mean
// off on the one surface with no accounts, so the AND lives here rather than as
// a write to the row: the owner's sharing decision is remembered and simply
// does not apply while the module is off.
func (s *Service) visibility(ctx context.Context) (visibility, error) {
	libraries, err := s.st.ListLibraries(ctx)
	if err != nil {
		return visibility{}, err
	}
	adultEnabled, err := s.st.AdultEnabled(ctx)
	if err != nil {
		return visibility{}, err
	}
	v := visibility{
		libraries: libraries,
		byID:      make(map[int64]bool, len(libraries)),
		defaults:  make(map[string]int64, len(legacyContainerID)),
	}
	for _, l := range libraries {
		visible := l.DLNAVisible
		if l.Kind == core.LibraryKindAdult && !adultEnabled {
			visible = false
		}
		v.byID[l.ID] = visible
		if l.IsDefault {
			v.defaults[l.Kind] = l.ID
		}
	}
	return v, nil
}

// objectLibrary resolves the library that owns an object id by reading the row
// behind it: a movie item's movie, a series/season/episode object's series, a
// container id's own library row. The false return is for the root and for ids
// that name nothing, neither of which belongs to a single library.
func (s *Service) objectLibrary(ctx context.Context, objectID string) (libID int64, kind string, owned bool, err error) {
	if kind, ok := legacyContainerKind[objectID]; ok {
		// A legacy container names its kind's DEFAULT library, which is exactly
		// what a zero library id resolves to.
		return 0, kind, true, nil
	}
	if strings.HasPrefix(objectID, libraryPrefix) {
		id, err := strconv.ParseInt(strings.TrimPrefix(objectID, libraryPrefix), 10, 64)
		if err != nil {
			return 0, "", false, errNoObject
		}
		lib, err := s.st.GetLibrary(ctx, id)
		if err != nil {
			return 0, "", false, notFound(err)
		}
		return lib.ID, lib.Kind, true, nil
	}
	if strings.HasPrefix(objectID, movieItemPrefix) {
		m, _, err := s.movieOfItem(ctx, objectID)
		if err != nil {
			return 0, "", false, err
		}
		return m.LibraryID, core.LibraryKindMovie, true, nil
	}
	space, ok := shelfSpaceOf(objectID)
	if !ok {
		return 0, "", false, nil
	}
	seriesID, err := s.seriesOfObject(ctx, space, objectID)
	if err != nil {
		return 0, "", false, err
	}
	sr, err := s.st.GetSeries(ctx, seriesID)
	if err != nil {
		return 0, "", false, notFound(err)
	}
	return sr.LibraryID, core.LibraryKindForSeries(sr.Kind), true, nil
}

// seriesOfObject is the series id behind a series, season or episode object
// id. The episode form needs a row read, because an episode id names the
// episode and not the show it is under.
func (s *Service) seriesOfObject(ctx context.Context, space shelf, objectID string) (int64, error) {
	if strings.HasPrefix(objectID, space.seriesPrefix) {
		seriesID, _, _, err := space.parseSeriesID(objectID)
		return seriesID, err
	}
	episodeID, _, err := space.parseEpisodeID(objectID)
	if err != nil {
		return 0, err
	}
	e, err := s.st.GetEpisode(ctx, episodeID)
	if err != nil {
		return 0, notFound(err)
	}
	return e.SeriesID, nil
}

// hidden reports whether objectID belongs to a library that is not advertised.
func (s *Service) hidden(ctx context.Context, objectID string) (bool, error) {
	libID, kind, owned, err := s.objectLibrary(ctx, objectID)
	if err != nil || !owned {
		return false, err
	}
	v, err := s.visibility(ctx)
	if err != nil {
		return false, err
	}
	return !v.library(libID, kind), nil
}

// libraryOf resolves the library row an item names. A zero library_id is an
// item from before 0022 and resolves through the kind's default library, which
// is what the zero means everywhere else (see core.Movie.LibraryID). A named
// library that is gone is errNoObject, matching ownedBy: such an item hangs
// under no container, so there is no container id to give it.
func (s *Service) libraryOf(ctx context.Context, libraryID int64, kind string) (*core.Library, error) {
	if libraryID != 0 {
		lib, err := s.st.GetLibrary(ctx, libraryID)
		if err != nil {
			return nil, notFound(err)
		}
		return lib, nil
	}
	lib, err := s.st.GetDefaultLibrary(ctx, kind)
	if err != nil {
		return nil, notFound(err)
	}
	return lib, nil
}

// containerLibrary resolves a root container id to the library it stands for:
// a legacy per-kind id names that kind's DEFAULT library, "lib:<id>" names the
// row outright. The false return means objectID is not a root container at
// all, which is the caller's cue to try the series id spaces.
//
// "lib:<id>" keeps answering after that library is promoted to its kind's
// default, even though the tree then advertises it under the legacy id: the
// two ids name one library, and a promotion should not empty a client's cache.
func (s *Service) containerLibrary(ctx context.Context, objectID string) (*core.Library, bool, error) {
	if kind, ok := legacyContainerKind[objectID]; ok {
		lib, err := s.st.GetDefaultLibrary(ctx, kind)
		if err != nil {
			return nil, false, notFound(err)
		}
		return lib, true, nil
	}
	if !strings.HasPrefix(objectID, libraryPrefix) {
		return nil, false, nil
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(objectID, libraryPrefix), 10, 64)
	if err != nil {
		return nil, false, errNoObject
	}
	lib, err := s.st.GetLibrary(ctx, id)
	if err != nil {
		return nil, false, notFound(err)
	}
	return lib, true, nil
}

// shelfOfSeries resolves the shelf one series row is browsed through: its OWN
// library's. Computing it from the row rather than from the id is what keeps a
// series container's parent id, and its episodes' parent ids, pointing at the
// container the series actually hangs under.
func (s *Service) shelfOfSeries(ctx context.Context, space shelf, seriesID int64) (shelf, *core.Series, error) {
	sr, err := s.insistShelfSeries(ctx, space, seriesID)
	if err != nil {
		return shelf{}, nil, err
	}
	lib, err := s.libraryOf(ctx, sr.LibraryID, core.LibraryKindForSeries(sr.Kind))
	if err != nil {
		return shelf{}, nil, err
	}
	sh, ok := shelfFor(*lib)
	if !ok {
		return shelf{}, nil, errNoObject
	}
	return sh, sr, nil
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
	if strings.HasPrefix(objectID, movieItemPrefix) {
		// Items have no children. An empty document is the correct answer, not
		// an error: clients probe leaves this way.
		return newDIDL(), nil
	}
	lib, isContainer, err := s.containerLibrary(ctx, objectID)
	if err != nil {
		return nil, err
	}
	if isContainer {
		if lib.Kind == core.LibraryKindMovie {
			return s.movieChildren(ctx, u, *lib)
		}
		sh, ok := shelfFor(*lib)
		if !ok {
			return nil, errNoObject
		}
		return s.seriesChildren(ctx, u, sh)
	}
	space, ok := shelfSpaceOf(objectID)
	if !ok {
		return nil, errNoObject
	}
	if !strings.HasPrefix(objectID, space.seriesPrefix) {
		// An episode item, which has no children.
		return newDIDL(), nil
	}
	seriesID, season, hasSeason, err := space.parseSeriesID(objectID)
	if err != nil {
		return nil, err
	}
	sh, _, err := s.shelfOfSeries(ctx, space, seriesID)
	if err != nil {
		return nil, err
	}
	if hasSeason {
		return s.episodeChildren(ctx, u, sh, seriesID, season)
	}
	return s.seasonChildren(ctx, u, sh, seriesID)
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
	case strings.HasPrefix(objectID, movieItemPrefix):
		item, err := s.movieItemMetadata(ctx, u, objectID)
		if err != nil {
			return nil, err
		}
		out.Items = []didlItem{*item}
	default:
		lib, isContainer, err := s.containerLibrary(ctx, objectID)
		if err != nil {
			return nil, err
		}
		if isContainer {
			kids, err := s.children(ctx, u, objectID)
			if err != nil {
				return nil, err
			}
			out.Containers = []didlContainer{
				container(objectID, rootID, libraryTitle(*lib), kids.count(), ""),
			}
			break
		}
		space, ok := shelfSpaceOf(objectID)
		if !ok {
			return nil, errNoObject
		}
		if strings.HasPrefix(objectID, space.seriesPrefix) {
			c, err := s.seriesMetadata(ctx, u, space, objectID)
			if err != nil {
				return nil, err
			}
			out.Containers = []didlContainer{*c}
			break
		}
		item, err := s.episodeItemMetadata(ctx, u, space, objectID)
		if err != nil {
			return nil, err
		}
		out.Items = []didlItem{*item}
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

// rootChildren advertises one container per visible library.
//
// One loop, so every library is advertised by the same rule and by nothing
// else. There is no branch here that mentions the adult module: if a library
// row says dlna_visible, it is on the shelf; if it does not, it is absent —
// and the adult row is created with the flag off (store.SetAdultEnabled), so
// enabling the module changes nothing about what the LAN can see until the
// owner makes that second decision.
func (s *Service) rootChildren(ctx context.Context, u urls) (*didlLite, error) {
	v, err := s.visibility(ctx)
	if err != nil {
		return nil, err
	}
	out := newDIDL()
	for _, lib := range v.visible() {
		if lib.Kind == core.LibraryKindMovie {
			movies, err := s.movieChildren(ctx, u, lib)
			if err != nil {
				return nil, err
			}
			out.Containers = append(out.Containers,
				container(libraryContainerID(lib), rootID, libraryTitle(lib), movies.count(), ""))
			continue
		}
		sh, ok := shelfFor(lib)
		if !ok {
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

// movieChildren lists one item per movie file in ONE movie library.
//
// Movies with no file are absent: this is a playback surface, and a container
// entry that cannot be played is a dead end on a remote control. A movie that
// somehow has two files gets two entries, disambiguated by filename, rather
// than one entry that silently picks a winner.
//
// The ownership filter is an exposure boundary, not a tidy-up: a second movie
// library carries its own dlna_visible, and an unfiltered listing would hang
// its films under the default library's container — where that flag has no
// say, because it is not that library's container.
func (s *Service) movieChildren(ctx context.Context, u urls, lib core.Library) (*didlLite, error) {
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

	parentID := libraryContainerID(lib)
	out := newDIDL()
	for _, m := range movies {
		if !ownedBy(m.LibraryID, lib) {
			continue
		}
		owned := byMovie[m.ID]
		for _, f := range owned {
			out.Items = append(out.Items, movieItem(u, parentID, m, f, len(owned) > 1))
		}
	}
	return out, nil
}

func movieItem(u urls, parentID string, m core.Movie, f core.MediaFile, disambiguate bool) didlItem {
	title := titleWithYear(m.Title, m.Year)
	if disambiguate {
		title += " — " + path.Base(f.Path)
	}
	return item(movieItemPrefix+strconv.FormatInt(f.ID, 10), parentID, title, u.art(m.PosterPath), u, f)
}

// seriesChildren lists one shelf's series as containers of seasons.
//
// Unlike movies these are not filtered by file presence: a series is a shelf,
// and answering the count with a query per series would make browsing the TV
// folder cost a query per show. An empty season list is a visible, honest
// "nothing here yet".
//
// Both filters shelfSeries applies are exposure boundaries, not tidy-ups. A
// site is stored as a series (PLAN phase 9 task 3), so an unfiltered list would
// hang the adult library inside the TELEVISION container — where the adult
// library's own dlna_visible flag has no say, because it is not that library's
// container — and a second television library's shows would ride the default
// library's container under the default's flag for the same reason.
func (s *Service) seriesChildren(ctx context.Context, u urls, sh shelf) (*didlLite, error) {
	all, err := s.shelfSeries(ctx, sh)
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

// shelfSeries is the series one shelf holds: the right kind, owned by the
// right library. It is the single place both filters live, so browse and
// search cannot disagree about what is on a shelf.
func (s *Service) shelfSeries(ctx context.Context, sh shelf) ([]core.Series, error) {
	all, err := s.st.ListSeriesByKind(ctx, sh.seriesKind)
	if err != nil {
		return nil, err
	}
	out := make([]core.Series, 0, len(all))
	for _, sr := range all {
		if ownedBy(sr.LibraryID, sh.library) {
			out = append(out, sr)
		}
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

func (s *Service) seriesMetadata(ctx context.Context, u urls, space shelf, objectID string) (*didlContainer, error) {
	seriesID, season, hasSeason, err := space.parseSeriesID(objectID)
	if err != nil {
		return nil, err
	}
	sh, sr, err := s.shelfOfSeries(ctx, space, seriesID)
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

// movieOfItem reads the file and the movie behind a "m:<fileID>" object id. A
// file with no movie — an episode file reached through the movie prefix —
// fails on the GetMovie(0) lookup, which is the movie id space's integrity
// check, the counterpart of insistShelfSeries.
func (s *Service) movieOfItem(ctx context.Context, objectID string) (*core.Movie, *core.MediaFile, error) {
	fileID, err := strconv.ParseInt(strings.TrimPrefix(objectID, movieItemPrefix), 10, 64)
	if err != nil {
		return nil, nil, errNoObject
	}
	f, err := s.st.GetMediaFile(ctx, fileID)
	if err != nil {
		return nil, nil, notFound(err)
	}
	m, err := s.st.GetMovie(ctx, f.MovieID)
	if err != nil {
		return nil, nil, notFound(err)
	}
	return m, f, nil
}

func (s *Service) movieItemMetadata(ctx context.Context, u urls, objectID string) (*didlItem, error) {
	m, f, err := s.movieOfItem(ctx, objectID)
	if err != nil {
		return nil, err
	}
	// The parent is the movie's OWN library's container, so a BrowseMetadata
	// answers with the container the item was actually browsed under.
	lib, err := s.libraryOf(ctx, m.LibraryID, core.LibraryKindMovie)
	if err != nil {
		return nil, err
	}
	owned, err := s.st.ListMediaFilesForMovie(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	out := movieItem(u, libraryContainerID(*lib), *m, *f, len(owned) > 1)
	return &out, nil
}

func (s *Service) episodeItemMetadata(ctx context.Context, u urls, space shelf, objectID string) (*didlItem, error) {
	episodeID, fileID, err := space.parseEpisodeID(objectID)
	if err != nil {
		return nil, err
	}
	e, err := s.st.GetEpisode(ctx, episodeID)
	if err != nil {
		return nil, notFound(err)
	}
	f, err := s.st.GetMediaFile(ctx, fileID)
	if err != nil {
		return nil, notFound(err)
	}
	sh, sr, err := s.shelfOfSeries(ctx, space, e.SeriesID)
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

// parseEpisodeID splits "e:12:34" into the episode and the file that plays it
// (and "ae:12:34" on the adult shelf). Both halves are required: one file can
// appear under two episodes, so neither alone names the object.
func (sh shelf) parseEpisodeID(objectID string) (episodeID, fileID int64, err error) {
	rest := strings.TrimPrefix(objectID, sh.episodePrefix)
	epRaw, fileRaw, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, 0, errNoObject
	}
	episodeID, err = strconv.ParseInt(epRaw, 10, 64)
	if err != nil {
		return 0, 0, errNoObject
	}
	fileID, err = strconv.ParseInt(fileRaw, 10, 64)
	if err != nil {
		return 0, 0, errNoObject
	}
	return episodeID, fileID, nil
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
