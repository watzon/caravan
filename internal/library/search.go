package library

import (
	"context"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// maxHitsPerProvider bounds what ONE provider contributes to a merged answer.
// The cap is per provider rather than over the whole result because a chain
// whose head returns a hundred loose matches would otherwise push every other
// provider's answer off the end of the list — which is exactly the failure
// merging exists to prevent.
const maxHitsPerProvider = 20

// ProviderFailure is one provider's refusal to answer a search, named so the
// add dialog can say which one and why.
type ProviderFailure struct {
	Provider string
	Message  string
}

// SearchHits is a library search's merged answer.
type SearchHits struct {
	// Movies and Series hold the candidates, in chain order: the first
	// provider's hits, then the second's. Exactly one of the two is populated,
	// chosen by the media type asked for.
	Movies []core.MovieMeta
	Series []core.SeriesMeta
	// Providers are the chain ids that actually ran, including the ones that
	// failed. A provider skipped for not serving the kind did not run.
	Providers []string
	// Failures are the providers that ran and errored. They are part of a
	// SUCCESSFUL answer: one provider being down must not hide the hits the
	// others returned, and the screen shows the gap rather than pretending the
	// chain is shorter than it is.
	Failures []ProviderFailure
}

// SearchLibrary identifies a title through one library's whole provider chain.
//
// libraryID 0 means the kind's default library for mediaType — a search made
// before the user picked a shelf still has to go through somebody's chain, and
// the default is the shelf the add would land in anyway.
//
// The results are MERGED rather than taken from the first provider that
// answered anything, and that is the whole design:
//
//   - TMDB answers something for very nearly every anime query, so a
//     first-non-empty rule with TMDB at the head never reaches AniList at all;
//   - reversing the order does not fix it, it inverts it — the live-action show
//     that shares a name with an anime would then be the one nobody sees;
//   - so both are asked and both are shown, labelled by the provider that
//     offered them. Jellyfin does the same thing with its agents, for the same
//     reason: which of two true answers the user meant is the user's to say.
//
// There is deliberately no cross-provider de-duplication. Two providers' ids
// are written in different vocabularies and their titles are different strings
// for the same show; collapsing them would need an identity mapping that does
// not exist, and a wrong collapse silently deletes the candidate the user
// wanted.
//
// A provider that fails is a Failure, not an error: the chain's other answers
// still stand. Only a chain where EVERY provider failed returns an error, and
// it returns the first failure's, so a TMDB-headed chain with a rejected key
// still surfaces as the credential fault the add dialog knows how to explain
// (core.ErrMetadataUnauthorized survives the wrapping).
func (m *Manager) SearchLibrary(ctx context.Context, libraryID int64, mediaType, q string) (*SearchHits, error) {
	var kind string
	switch mediaType {
	case core.MediaTypeMovie:
		kind = core.LibraryKindMovie
	case core.MediaTypeSeries:
		kind = core.LibraryKindTV
	default:
		return nil, fmt.Errorf("library: unknown media type %q", mediaType)
	}

	lib, err := m.libraryByIDOrDefault(ctx, libraryID, kind)
	if err != nil {
		return nil, err
	}
	chain := m.metadataChain(ctx, lib)
	if len(chain) == 0 {
		// Nothing on this library's chain is configured. That is the nil
		// provider every caller already degrades on, said about a chain.
		return nil, core.ErrNoMetadataProvider
	}

	hits := &SearchHits{}
	var firstErr error
	for _, b := range chain {
		var err error
		switch mediaType {
		case core.MediaTypeMovie:
			var found []core.MovieMeta
			if found, err = b.P.SearchMovies(ctx, q); err == nil {
				hits.Movies = append(hits.Movies, capHits(found)...)
			}
		case core.MediaTypeSeries:
			var found []core.SeriesMeta
			if found, err = b.P.SearchSeries(ctx, q); err == nil {
				hits.Series = append(hits.Series, capHits(found)...)
			}
		}
		if errors.Is(err, core.ErrProviderKindUnsupported) {
			// Not a failure and not a run: this provider was never in a
			// position to answer about this kind.
			continue
		}
		hits.Providers = append(hits.Providers, b.ID)
		if err != nil {
			hits.Failures = append(hits.Failures, ProviderFailure{Provider: b.ID, Message: err.Error()})
			if firstErr == nil {
				firstErr = fmt.Errorf("library: search %q through %q: %w", q, b.ID, err)
			}
		}
	}
	if len(hits.Providers) > 0 && len(hits.Failures) == len(hits.Providers) {
		return nil, firstErr
	}
	return hits, nil
}

// capHits truncates one provider's contribution to maxHitsPerProvider,
// keeping the provider's own order — its best match is its first.
func capHits[T any](hits []T) []T {
	if len(hits) > maxHitsPerProvider {
		return hits[:maxHitsPerProvider]
	}
	return hits
}
