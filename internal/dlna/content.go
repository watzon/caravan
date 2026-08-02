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
)

// Object-id prefixes for the rows behind the tree. Ids are opaque strings to
// the client, so they encode exactly what it takes to answer a BrowseMetadata
// on them without a search: a movie item carries its file, an episode item
// carries both the episode it appears under and the file that plays it,
// because one file can appear under two episodes (S01E01E02, SPEC §7).
const (
	movieItemPrefix   = "m:"
	seriesPrefix      = "s:"
	episodeItemPrefix = "e:"
)

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

// children returns the direct children of objectID as a DIDL document.
func (s *Service) children(ctx context.Context, u urls, objectID string) (*didlLite, error) {
	switch {
	case objectID == rootID:
		return s.rootChildren(ctx, u)
	case objectID == moviesID:
		return s.movieChildren(ctx, u)
	case objectID == tvID:
		return s.seriesChildren(ctx, u)
	case strings.HasPrefix(objectID, seriesPrefix):
		seriesID, season, hasSeason, err := parseSeriesID(objectID)
		if err != nil {
			return nil, err
		}
		if hasSeason {
			return s.episodeChildren(ctx, u, seriesID, season)
		}
		return s.seasonChildren(ctx, u, seriesID)
	case strings.HasPrefix(objectID, movieItemPrefix), strings.HasPrefix(objectID, episodeItemPrefix):
		// Items have no children. An empty document is the correct answer, not
		// an error: clients probe leaves this way.
		return newDIDL(), nil
	default:
		return nil, errNoObject
	}
}

// metadata returns objectID's own description as a one-object DIDL document.
func (s *Service) metadata(ctx context.Context, u urls, objectID string) (*didlLite, error) {
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
	case objectID == moviesID, objectID == tvID:
		kids, err := s.children(ctx, u, objectID)
		if err != nil {
			return nil, err
		}
		title := "Movies"
		if objectID == tvID {
			title = "TV"
		}
		out.Containers = []didlContainer{container(objectID, rootID, title, kids.count(), "")}
	case strings.HasPrefix(objectID, seriesPrefix):
		c, err := s.seriesMetadata(ctx, u, objectID)
		if err != nil {
			return nil, err
		}
		out.Containers = []didlContainer{*c}
	case strings.HasPrefix(objectID, movieItemPrefix):
		item, err := s.movieItemMetadata(ctx, u, objectID)
		if err != nil {
			return nil, err
		}
		out.Items = []didlItem{*item}
	case strings.HasPrefix(objectID, episodeItemPrefix):
		item, err := s.episodeItemMetadata(ctx, u, objectID)
		if err != nil {
			return nil, err
		}
		out.Items = []didlItem{*item}
	default:
		return nil, errNoObject
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
	movies, err := s.movieChildren(ctx, u)
	if err != nil {
		return nil, err
	}
	series, err := s.seriesChildren(ctx, u)
	if err != nil {
		return nil, err
	}
	out := newDIDL()
	out.Containers = []didlContainer{
		container(moviesID, rootID, "Movies", movies.count(), ""),
		container(tvID, rootID, "TV", series.count(), ""),
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

// seriesChildren lists every series as a container of seasons.
//
// Unlike movies these are not filtered by file presence: a series is a shelf,
// and answering the count with a query per series would make browsing the TV
// folder cost a query per show. An empty season list is a visible, honest
// "nothing here yet".
func (s *Service) seriesChildren(ctx context.Context, u urls) (*didlLite, error) {
	all, err := s.st.ListSeries(ctx)
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
			seriesObjectID(sr.ID), tvID, titleWithYear(sr.Title, sr.Year), len(seasons), u.art(sr.PosterPath)))
	}
	return out, nil
}

// seasonChildren lists a series' seasons, each counting the playable files
// beneath it rather than its episode rows: a season a client opens to find
// empty is worse than one that says it holds three of ten episodes.
func (s *Service) seasonChildren(ctx context.Context, u urls, seriesID int64) (*didlLite, error) {
	sr, err := s.st.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, notFound(err)
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
			seasonObjectID(seriesID, season.Number), seriesObjectID(seriesID),
			seasonTitle(season), counts[season.Number], u.art(art)))
	}
	return out, nil
}

// episodeChildren lists the playable files of one season.
func (s *Service) episodeChildren(ctx context.Context, u urls, seriesID int64, season int) (*didlLite, error) {
	sr, err := s.st.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, notFound(err)
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
		out.Items = append(out.Items, episodeItem(u, sr, byID[p.EpisodeID], p.File))
	}
	return out, nil
}

func episodeItem(u urls, sr *core.Series, e core.Episode, f core.MediaFile) didlItem {
	return item(episodeObjectID(e.ID, f.ID), seasonObjectID(sr.ID, e.SeasonNumber),
		episodeTitle(sr.Title, e), u.art(sr.PosterPath), u, f)
}

func (s *Service) seriesMetadata(ctx context.Context, u urls, objectID string) (*didlContainer, error) {
	seriesID, season, hasSeason, err := parseSeriesID(objectID)
	if err != nil {
		return nil, err
	}
	sr, err := s.st.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, notFound(err)
	}
	if !hasSeason {
		seasons, err := s.st.ListSeasons(ctx, seriesID)
		if err != nil {
			return nil, err
		}
		c := container(objectID, tvID, titleWithYear(sr.Title, sr.Year), len(seasons), u.art(sr.PosterPath))
		return &c, nil
	}

	kids, err := s.episodeChildren(ctx, u, seriesID, season)
	if err != nil {
		return nil, err
	}
	title := fmt.Sprintf("Season %d", season)
	if season == 0 {
		title = "Specials"
	}
	c := container(objectID, seriesObjectID(seriesID), title, kids.count(), u.art(sr.PosterPath))
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

func (s *Service) episodeItemMetadata(ctx context.Context, u urls, objectID string) (*didlItem, error) {
	rest := strings.TrimPrefix(objectID, episodeItemPrefix)
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
	sr, err := s.st.GetSeries(ctx, e.SeriesID)
	if err != nil {
		return nil, notFound(err)
	}
	out := episodeItem(u, sr, *e, *f)
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

func seriesObjectID(seriesID int64) string {
	return seriesPrefix + strconv.FormatInt(seriesID, 10)
}

func seasonObjectID(seriesID int64, season int) string {
	return seriesObjectID(seriesID) + ":" + strconv.Itoa(season)
}

func episodeObjectID(episodeID, fileID int64) string {
	return episodeItemPrefix + strconv.FormatInt(episodeID, 10) + ":" + strconv.FormatInt(fileID, 10)
}

// parseSeriesID splits "s:12" and "s:12:3" into their parts.
func parseSeriesID(objectID string) (seriesID int64, season int, hasSeason bool, err error) {
	rest := strings.TrimPrefix(objectID, seriesPrefix)
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

// episodeTitle is what shows on the remote: the code first, so a list sorts and
// reads correctly even on a renderer that truncates long titles.
func episodeTitle(seriesTitle string, e core.Episode) string {
	code := fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber)
	if e.Title != "" {
		return fmt.Sprintf("%s - %s", code, e.Title)
	}
	return fmt.Sprintf("%s - %s", code, seriesTitle)
}
