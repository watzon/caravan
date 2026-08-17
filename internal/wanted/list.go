package wanted

import (
	"context"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// Wanted reasons: the vocabulary the wanted list and the calendar both use to
// say why an item is still being looked for.
const (
	ReasonMissing     = "missing"
	ReasonBelowCutoff = "below_cutoff"
)

// Movie is one movie Caravan is still looking for.
type Movie struct {
	core.Movie
	// Reason is one of the Reason* constants.
	Reason string
	// FileQuality is the best file quality the library holds, empty when
	// Reason is ReasonMissing.
	FileQuality string
}

// Episode is one episode Caravan is still looking for.
type Episode struct {
	core.Episode
	SeriesTitle string
	// The series' artwork, since episodes have none of their own.
	SeriesPosterPath string
	SeriesPosterURL  string
	// SeriesKind is the series' core.SeriesKind* value. Everything that acts
	// on a wanted episode has to know it: the indexer fan-out, the quality
	// profile, and whether the item may be shown to a given caller at all.
	SeriesKind string
	// SeriesLibraryID is the series' own library, for SeriesKind's reason —
	// two libraries of one kind may search different indexers and default to
	// different profiles. Zero resolves through the kind's default library.
	SeriesLibraryID int64
	// Reason is one of the Reason* constants. An unaired episode is never
	// wanted: there is nothing to find yet.
	Reason string
	// FileQuality is the best file quality the library holds, empty when
	// Reason is ReasonMissing.
	FileQuality string
}

// Lists is the wanted list: everything monitored that is missing or below
// its profile's cutoff (PLAN phase 3, task 2).
type Lists struct {
	Movies   []Movie
	Episodes []Episode
}

// Compute builds the wanted list from the store. It lives here rather than in
// the store package because "wanted" is a decision, and decisions are this
// package's job: the store reports file states, Compute applies profiles.
func Compute(ctx context.Context, st *store.Store) (*Lists, error) {
	movieStates, err := st.MovieFileStates(ctx)
	if err != nil {
		return nil, err
	}
	episodeStates, err := st.EpisodeFileStates(ctx)
	if err != nil {
		return nil, err
	}
	// An item under an inactive library is not wanted: it must not reach the
	// backlog sweep, the RSS matcher or the wanted screen. This is where that is
	// enforced rather than at each of those three, because the wanted list is
	// what all three read — and an item that never enters it cannot leak out of
	// any of them.
	libraries, err := st.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	owners := core.NewLibrarySet(libraries)

	// Profiles are resolved once and cached: a library shares a handful of
	// profiles across thousands of items. The LIBRARY is part of the key
	// because an item naming no profile of its own resolves to its own
	// library's default, and two libraries — even of one kind — may have
	// picked different ones.
	type profileKey struct {
		libraryID int64
		kind      string
		id        int64
	}
	profiles := map[profileKey]*core.QualityProfile{}
	resolve := func(libraryID int64, kind string, id int64) (*core.QualityProfile, error) {
		key := profileKey{libraryID: libraryID, kind: kind, id: id}
		if p, ok := profiles[key]; ok {
			return p, nil
		}
		p, err := st.ResolveItemQualityProfileByLibrary(ctx, libraryID, kind, id)
		if err != nil {
			return nil, err
		}
		profiles[key] = p
		return p, nil
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)

	out := &Lists{Movies: []Movie{}, Episodes: []Episode{}}
	for _, ms := range movieStates {
		if !owners.Active(ms.Movie.LibraryID) {
			continue
		}
		// A movie that has not reached its minimum availability is never
		// wanted, the way an unaired episode never is: there is nothing real
		// to find yet. A file on disk overrides the calendar — whatever
		// exists is graded against the profile, not against release dates.
		if !ms.HasFile && !Available(ms.Movie, today) {
			continue
		}
		reason, err := movieReason(ctx, ms, resolve)
		if err != nil {
			return nil, err
		}
		if reason == "" {
			continue
		}
		out.Movies = append(out.Movies, Movie{Movie: ms.Movie, Reason: reason, FileQuality: ms.FileQuality})
	}
	for _, es := range episodeStates {
		if !owners.Active(es.SeriesLibraryID) {
			continue
		}
		// An episode with no known air date is treated as aired: providers do
		// not always publish one, and "we don't know" must not hide a hole in
		// the library.
		aired := es.Episode.AirDate.IsZero() || !es.Episode.AirDate.After(today)
		if !aired {
			continue
		}
		// The item's own library, not television's: a site's default quality
		// profile is the adult library's, and grading a scene against the TV
		// library's cutoff would search for releases nobody asked for.
		p, err := resolve(es.SeriesLibraryID, core.LibraryKindForSeries(es.SeriesKind), es.SeriesProfileID)
		if err != nil {
			return nil, err
		}
		reason := fileReason(es.HasFile, es.FileQuality, p)
		if reason == "" {
			continue
		}
		out.Episodes = append(out.Episodes, Episode{
			Episode: es.Episode, SeriesTitle: es.SeriesTitle,
			SeriesPosterPath: es.SeriesPosterPath, SeriesPosterURL: es.SeriesPosterURL,
			SeriesKind: es.SeriesKind, SeriesLibraryID: es.SeriesLibraryID,
			Reason: reason, FileQuality: es.FileQuality,
		})
	}
	return out, nil
}

// cinemaWindow is how long after the theatrical date a movie with no known
// home-release date is assumed to have reached one. It is Radarr's number: by
// three months out, a movie that is going to get a home release has had it.
const cinemaWindow = 90 * 24 * time.Hour

// Available reports whether a movie has reached its minimum availability —
// whether a file for it can plausibly exist yet. The rules are Radarr's
// (Movie.IsAvailable), so a library migrated from one behaves identically:
//
//   - announced: available immediately.
//   - in_cinemas: available once the theatrical date passes. With no
//     theatrical date on record it degrades to the released rule rather than
//     to "always": a missing date must not mean "search now".
//   - released: available at the earlier of the digital and physical release
//     dates; with neither on record, cinemaWindow after the theatrical date.
//
// One deliberate deviation: Radarr holds a movie with no dates at all forever,
// which silently never searches for obscure titles the provider has no dates
// for. Caravan treats it as available, the same reading Compute gives an
// episode with no air date: "we don't know" must not hide a hole in the
// library.
//
// An empty MinAvailability reads as released, the store default.
func Available(m core.Movie, today time.Time) bool {
	switch m.MinAvailability {
	case core.AvailabilityAnnounced:
		return true
	case core.AvailabilityInCinemas:
		if !m.ReleaseDate.IsZero() {
			return !m.ReleaseDate.After(today)
		}
	}

	when := homeRelease(m)
	if when.IsZero() {
		return true
	}
	return !when.After(today)
}

// homeRelease is the date a movie's home release is (or is expected to be)
// out, zero when there is nothing to expect one from.
func homeRelease(m core.Movie) time.Time {
	when := m.DigitalRelease
	if when.IsZero() || (!m.PhysicalRelease.IsZero() && m.PhysicalRelease.Before(when)) {
		when = m.PhysicalRelease
	}
	if !when.IsZero() {
		return when
	}
	if !m.ReleaseDate.IsZero() {
		return m.ReleaseDate.Add(cinemaWindow)
	}
	return time.Time{}
}

// movieReason applies the wanted rules to one movie file state.
func movieReason(ctx context.Context, ms store.MovieFileState, resolve func(int64, string, int64) (*core.QualityProfile, error)) (string, error) {
	if !ms.HasFile {
		return ReasonMissing, nil
	}
	p, err := resolve(ms.Movie.LibraryID, core.LibraryKindMovie, ms.Movie.QualityProfileID)
	if err != nil {
		return "", err
	}
	return fileReason(true, ms.FileQuality, p), nil
}

// fileReason is the shared file half of the wanted rules: a file below the
// profile's cutoff keeps the item wanted only when the profile allows
// upgrades at all (SPEC §9).
func fileReason(hasFile bool, fileQuality string, p *core.QualityProfile) string {
	if !hasFile {
		return ReasonMissing
	}
	if p.UpgradeAllowed && BelowCutoff(fileQuality, p) {
		return ReasonBelowCutoff
	}
	return ""
}
