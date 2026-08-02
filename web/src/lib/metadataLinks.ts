/**
 * External metadata-provider pages for library items (SPEC §6: TMDB is the
 * canonical provider; IMDb/TVDB ids ride along). Pure URL construction so the
 * detail routes only decide where to render the chips.
 */
import type { Movie, Series } from './api/types';

export interface MetadataLink {
  /** Short provider wordmark, e.g. "TMDB". */
  label: string;
  href: string;
}

/** Provider pages for a movie, in canonical-provider-first order. */
export function movieLinks(movie: Pick<Movie, 'tmdb_id' | 'imdb_id'>): MetadataLink[] {
  const out: MetadataLink[] = [];
  if (movie.tmdb_id > 0) {
    out.push({ label: 'TMDB', href: `https://www.themoviedb.org/movie/${movie.tmdb_id}` });
  }
  if (movie.imdb_id) {
    out.push({ label: 'IMDb', href: `https://www.imdb.com/title/${movie.imdb_id}/` });
  }
  return out;
}

/** Provider pages for a series, in canonical-provider-first order. */
export function seriesLinks(
  series: Pick<Series, 'tmdb_id' | 'tvdb_id' | 'imdb_id'>,
): MetadataLink[] {
  const out: MetadataLink[] = [];
  if (series.tmdb_id > 0) {
    out.push({ label: 'TMDB', href: `https://www.themoviedb.org/tv/${series.tmdb_id}` });
  }
  if (series.imdb_id) {
    out.push({ label: 'IMDb', href: `https://www.imdb.com/title/${series.imdb_id}/` });
  }
  if (series.tvdb_id > 0) {
    // The dereferrer resolves a bare TVDB id to the series' slug URL.
    out.push({ label: 'TVDB', href: `https://www.thetvdb.com/dereferrer/series/${series.tvdb_id}` });
  }
  return out;
}

/**
 * The TMDB page for one episode, or null when the series has no TMDB id.
 * TMDB routes episodes by season/episode number under the series, not by the
 * episode's own id.
 */
export function episodeLink(
  seriesTmdbID: number,
  season: number,
  episode: number,
): string | null {
  if (seriesTmdbID <= 0) return null;
  return `https://www.themoviedb.org/tv/${seriesTmdbID}/season/${season}/episode/${episode}`;
}
