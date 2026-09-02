package dlna

// ContentDirectory Search (service spec §2.7.5). Optional on paper, mandatory
// in the field: library-style clients, Infuse among them, enumerate a server
// with one recursive Search instead of walking Browse, and a server that faults
// the action renders as a device whose library is empty. This is the smallest
// Search that satisfies them: flatten the subtree under ContainerID, then keep
// what the criteria's class and title terms accept.
//
// The criteria language is scoped on purpose. Real clients send conjunctions of
// `upnp:class derivedfrom "…"` and `dc:title contains "…"`; those terms are
// honored and everything else in the expression is ignored, which errs toward
// showing content. A criteria whose class terms name only things this server
// does not have (music, images) matches nothing: that is the honest answer to
// "list your albums".

import (
	"context"
	"regexp"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// searchCaps is what GetSearchCapabilities reports: the properties Search
// actually filters on.
const searchCaps = "dc:title,upnp:class"

var (
	classTermRE = regexp.MustCompile(`(?i)upnp:class\s+(=|derivedfrom)\s+"([^"]+)"`)
	titleTermRE = regexp.MustCompile(`(?i)dc:title\s+contains\s+"([^"]+)"`)
)

// searchCriteria is the parsed, honored subset of a criteria expression.
type searchCriteria struct {
	// everything is the "*" criteria: no filtering at all.
	everything bool
	// classTerms are {op, literal} pairs from upnp:class terms, op "=" or
	// "derivedfrom". An object matching any of them passes (clients OR their
	// class alternatives).
	classTerms [][2]string
	// titleTerms are dc:title substrings; an object must carry every one.
	titleTerms []string
}

func parseSearchCriteria(raw string) searchCriteria {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return searchCriteria{everything: true}
	}
	var c searchCriteria
	for _, m := range classTermRE.FindAllStringSubmatch(raw, -1) {
		c.classTerms = append(c.classTerms, [2]string{strings.ToLower(m[1]), m[2]})
	}
	for _, m := range titleTermRE.FindAllStringSubmatch(raw, -1) {
		c.titleTerms = append(c.titleTerms, m[1])
	}
	return c
}

func classMatches(class, op, literal string) bool {
	if op == "=" {
		return class == literal
	}
	// derivedfrom: the literal is the class itself or an ancestor of it.
	return class == literal || strings.HasPrefix(class, literal+".")
}

func (c searchCriteria) wantsClass(class string) bool {
	if c.everything {
		return true
	}
	if len(c.classTerms) == 0 {
		// A bare title search applies to everything; a criteria with neither
		// recognizable term matches nothing rather than everything, so an
		// expression this server cannot evaluate does not dump the library.
		return len(c.titleTerms) > 0
	}
	for _, t := range c.classTerms {
		if classMatches(class, t[0], t[1]) {
			return true
		}
	}
	return false
}

func (c searchCriteria) wantsTitle(title string) bool {
	for _, q := range c.titleTerms {
		if !strings.Contains(strings.ToLower(title), strings.ToLower(q)) {
			return false
		}
	}
	return true
}

// search answers the Search action: the subtree under containerID, filtered.
func (s *Service) search(ctx context.Context, u urls, containerID, criteria string) (*didlLite, error) {
	scope, err := s.searchScope(ctx, u, containerID)
	if err != nil {
		return nil, err
	}
	c := parseSearchCriteria(criteria)

	out := newDIDL()
	for _, ct := range scope.Containers {
		if c.wantsClass(ct.Class) && c.wantsTitle(ct.Title) {
			out.Containers = append(out.Containers, ct)
		}
	}
	for _, it := range scope.Items {
		if c.wantsClass(it.Class) && c.wantsTitle(it.Title) {
			out.Items = append(out.Items, it)
		}
	}
	return out, nil
}

// searchScope flattens everything beneath containerID, containers and items,
// the container itself excluded, in browse order.
func (s *Service) searchScope(ctx context.Context, u urls, containerID string) (*didlLite, error) {
	// Search is a second way to reach the same tree, so it answers the same
	// question about visibility Browse does: otherwise a library the owner
	// stopped advertising would still be enumerable by the clients that prefer
	// Search over walking Browse, which is most of them.
	if hidden, err := s.hidden(ctx, containerID); err != nil {
		return nil, err
	} else if hidden {
		return nil, errNoObject
	}

	out := newDIDL()
	switch {
	case containerID == rootID:
		root, err := s.rootChildren(ctx, u)
		if err != nil {
			return nil, err
		}
		out.Containers = append(out.Containers, root.Containers...)
		v, err := s.visibility(ctx)
		if err != nil {
			return nil, err
		}
		// One loop over the same libraries rootChildren advertises, so a
		// library is searchable exactly when it is browsable. A root search
		// that walked a library the root does not advertise would be the
		// classic way an invisible library leaks: most clients prefer Search
		// over walking Browse.
		for _, lib := range v.visible() {
			if err := s.libraryScope(ctx, u, lib, out); err != nil {
				return nil, err
			}
		}
	case strings.HasPrefix(containerID, movieItemPrefix):
		// Searching under an item is a well-formed question with an empty
		// answer, exactly like browsing its children.
	default:
		lib, isContainer, err := s.containerLibrary(ctx, containerID)
		if err != nil {
			return nil, err
		}
		if isContainer {
			if err := s.libraryScope(ctx, u, *lib, out); err != nil {
				return nil, err
			}
			break
		}
		space, ok := shelfSpaceOf(containerID)
		if !ok {
			return nil, errNoObject
		}
		if !strings.HasPrefix(containerID, space.seriesPrefix) {
			break
		}
		seriesID, season, hasSeason, err := space.parseSeriesID(containerID)
		if err != nil {
			return nil, err
		}
		sh, _, err := s.shelfOfSeries(ctx, space, seriesID)
		if err != nil {
			return nil, err
		}
		if hasSeason {
			episodes, err := s.episodeChildren(ctx, u, sh, seriesID, season)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, episodes.Items...)
		} else if err := s.seriesScope(ctx, u, sh, seriesID, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// libraryScope appends everything one library holds, whichever kind it is.
func (s *Service) libraryScope(ctx context.Context, u urls, lib core.Library, out *didlLite) error {
	if lib.Kind == core.LibraryKindMovie {
		movies, err := s.movieChildren(ctx, u, lib)
		if err != nil {
			return err
		}
		out.Items = append(out.Items, movies.Items...)
		return nil
	}
	sh, ok := shelfFor(lib)
	if !ok {
		return nil
	}
	if err := s.shelfScope(ctx, u, sh, out); err != nil {
		return err
	}
	// An anime library holds films beside its series, so a search scoped to it
	// has to walk both halves or it would answer about half the shelf.
	if lib.Kind != core.LibraryKindAnime {
		return nil
	}
	films, err := s.movieChildren(ctx, u, lib)
	if err != nil {
		return err
	}
	out.Items = append(out.Items, films.Items...)
	return nil
}

// shelfScope appends one shelf's series containers, season containers, and
// episode items.
func (s *Service) shelfScope(ctx context.Context, u urls, sh shelf, out *didlLite) error {
	series, err := s.seriesChildren(ctx, u, sh)
	if err != nil {
		return err
	}
	out.Containers = append(out.Containers, series.Containers...)
	// The same list seriesChildren walked, for the reason it gives: a site is a
	// series row, and a search that walked every kind, or every library of one
	// kind, would answer this shelf with somebody else's rows.
	all, err := s.shelfSeries(ctx, sh)
	if err != nil {
		return err
	}
	for _, sr := range all {
		if err := s.seriesScope(ctx, u, sh, sr.ID, out); err != nil {
			return err
		}
	}
	return nil
}

// seriesScope appends one series' season containers and every episode item it
// has a file for, across all seasons.
func (s *Service) seriesScope(ctx context.Context, u urls, sh shelf, seriesID int64, out *didlLite) error {
	seasons, err := s.seasonChildren(ctx, u, sh, seriesID)
	if err != nil {
		return err
	}
	out.Containers = append(out.Containers, seasons.Containers...)

	sr, err := s.insistShelfSeries(ctx, sh, seriesID)
	if err != nil {
		return err
	}
	episodes, err := s.st.ListEpisodes(ctx, seriesID)
	if err != nil {
		return err
	}
	byID := make(map[int64]core.Episode, len(episodes))
	for _, e := range episodes {
		byID[e.ID] = e
	}
	pairs, err := s.st.ListEpisodeMediaFilesForSeries(ctx, seriesID)
	if err != nil {
		return err
	}
	for _, p := range pairs {
		out.Items = append(out.Items, episodeItem(u, sh, sr, byID[p.EpisodeID], p.File))
	}
	return nil
}
