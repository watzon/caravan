package searchql

import (
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// The seed builders write the query an item page's search box starts with.
//
// They exist so that there is exactly one definition of "search for this
// movie". A "Search" button on a movie page and the same expression typed by
// hand must reach the indexers with identical text, and the only way to
// guarantee that is for the button to go through the language too. The tests
// pin each builder's UpstreamQueries against the core.*Searches function it
// has to agree with.
//
// Text values are always quoted, even when they need not be. A seed is
// machine-written and lands in an editable box: the quotes show the user where
// the value begins and ends before they start changing it.

// MovieExpression seeds a movie search. A year of zero or less is omitted,
// which is the same "no year known" core.MovieSearches takes.
func MovieExpression(title string, year int) string {
	parts := []string{fieldTitle + ":" + quote(strings.TrimSpace(title))}
	if year > 0 {
		parts = append(parts, fieldYear+":"+strconv.Itoa(year))
	}
	return strings.Join(parts, " ")
}

// SeriesExpression seeds a series search. Pass season -1 and episode 0 for the
// whole series, matching core.SeriesSearches. An episode is only written when
// a season is, because an episode number alone names nothing an indexer can
// search for.
func SeriesExpression(title string, season, episode int) string {
	parts := []string{fieldTitle + ":" + quote(strings.TrimSpace(title))}
	if season >= 0 {
		parts = append(parts, fieldSeason+":"+strconv.Itoa(season))
		if episode > 0 {
			parts = append(parts, fieldEpisode+":"+strconv.Itoa(episode))
		}
	}
	return strings.Join(parts, " ")
}

// SceneExpression seeds an adult scene search with everything the page will
// actually run: the site-and-date form, and — when the scene has a usable
// title — the site-and-title form the search also falls back to. The seed is
// the whole truth of the fan-out; a seed that named only one variant would sit
// above a "Searched indexers for" line listing two.
func SceneExpression(site string, date time.Time, title string) string {
	siteTerm := fieldSite + ":" + quote(strings.TrimSpace(site))
	dated, titled := "", ""
	for _, search := range core.SceneSearches(site, date, title) {
		switch search.Variant {
		case core.SceneSearchByDate:
			dated = siteTerm + " " + fieldDate + ":" + date.UTC().Format(isoDateLayout)
		case core.SceneSearchByTitle:
			// The by-title query is site-then-title in one string. Quoting it
			// whole keeps that word order through the planner, which would
			// append a site: term after the keywords.
			titled = quote(search.Query)
		}
	}
	switch {
	case dated != "" && titled != "":
		return "(" + dated + ") OR " + titled
	case dated != "":
		return dated
	case titled != "":
		return titled
	default:
		// Neither a date nor a title: the page searches the site name alone.
		return siteTerm
	}
}
