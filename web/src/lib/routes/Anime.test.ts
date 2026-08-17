/**
 * The one shelf that lists two item tables at once.
 *
 * What matters here is the merge and the links: an anime library owns films AND
 * series, so this screen has to ask both endpoints, keep only what the anime
 * shelves own, and send each card to the detail screen its own type already
 * has. There is no anime detail page, and a card that linked to one would be a
 * dead end.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Anime from './Anime.svelte';
import { navigate } from '../router.svelte';
import { session } from '../state/session.svelte';

function movie(id: number, title: string, libraryID: number) {
  return {
    id,
    tmdb_id: id,
    imdb_id: '',
    title,
    sort_title: title.toLowerCase(),
    year: 2021,
    overview: '',
    path: '',
    poster_path: '',
    poster_url: '',
    monitored: true,
    quality_profile_id: 0,
    library_id: libraryID,
    release_date: '',
    added_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    file: null,
  };
}

function series(id: number, title: string, libraryID: number) {
  return {
    id,
    tmdb_id: id,
    tvdb_id: 0,
    imdb_id: '',
    title,
    sort_title: title.toLowerCase(),
    year: 2021,
    overview: '',
    status: 'continuing',
    path: '',
    poster_path: '',
    poster_url: '',
    monitored: true,
    quality_profile_id: 0,
    library_id: libraryID,
    kind: 'anime',
    first_aired: '',
    added_at: '2026-02-01T00:00:00Z',
    updated_at: '2026-02-01T00:00:00Z',
    episode_count: 12,
    episode_file_count: 3,
  };
}

/** Library 3 is the anime shelf; 1 is the ordinary movie shelf beside it. */
const MOVIES = [movie(7, 'Akira', 3), movie(8, 'Dune', 1)];
const SERIES = [series(5, 'Frieren', 3)];

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let urls: string[];

beforeEach(() => {
  urls = [];
  window.scrollTo = () => {};
  session.user = {
    username: 'root',
    role: 'admin',
    open: false,
    adult: false,
    libraries: [
      { id: 1, kind: 'movie', name: 'Movies', icon: '' },
      { id: 3, kind: 'anime', name: 'Anime', icon: '' },
    ],
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      urls.push(url);
      const body = url.includes('/library/movies') ? { movies: MOVIES } : { series: SERIES };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  session.forget();
  vi.unstubAllGlobals();
  navigate('/', { replace: true });
});

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function open(url = '/anime') {
  window.history.replaceState({}, '', url);
  navigate(url, { replace: true });
  app = mount(Anime, { target: host, props: { onadd: () => {} } }) as Record<string, unknown>;
  await settle();
}

function cardHrefs(): string[] {
  return [...host.querySelectorAll<HTMLAnchorElement>('a[href^="/movies/"], a[href^="/series/"]')]
    .map((link) => link.getAttribute('href') ?? '');
}

describe('Anime shelf', () => {
  it('asks for anime series specifically, not the television list', async () => {
    await open();

    expect(urls.some((url) => url.includes('/library/series?kind=anime'))).toBe(true);
    expect(urls.some((url) => url.endsWith('/library/movies'))).toBe(true);
  });

  it('merges films and series into one grid, each linking to its own screen', async () => {
    await open();

    // Newest first: Frieren was added after Akira. Dune belongs to the movie
    // shelf, so it is not here at all.
    expect(cardHrefs()).toEqual(['/series/5', '/movies/7']);
    expect(host.textContent).toContain('Frieren');
    expect(host.textContent).toContain('Akira');
    expect(host.textContent).not.toContain('Dune');
  });

  it('narrows to the named library when the URL carries one', async () => {
    await open('/anime?library=3');
    expect(cardHrefs()).toEqual(['/series/5', '/movies/7']);

    unmount(app!);
    app = undefined;
    // A shelf that owns nothing shows nothing rather than everything — the same
    // answer /movies and /series give an id nobody owns.
    await open('/anime?library=99');
    expect(cardHrefs()).toEqual([]);
    expect(host.textContent).toContain('No anime yet');
  });

  it('filters by the libraryId the /l/:slug route supplies', async () => {
    window.history.replaceState({}, '', '/l/anime');
    navigate('/l/anime', { replace: true });
    app = mount(Anime, { target: host, props: { onadd: () => {}, libraryId: 3 } });
    await settle();
    expect(cardHrefs()).toEqual(['/series/5', '/movies/7']);
  });

  it('trusts an explicit library id over the kind, as the other two shelves do', async () => {
    // Nothing links here — the sidebar sends a movie shelf to /movies — but a
    // hand-typed id is a filter, and all three shelves read it the same way
    // rather than each second-guessing it differently.
    await open('/anime?library=1');
    expect(cardHrefs()).toEqual(['/movies/8']);
  });

  it('keeps a film out when no anime library in the session owns it', async () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      libraries: [{ id: 1, kind: 'movie', name: 'Movies', icon: '' }],
    };
    await open();

    // The series half is the server's answer and stands; the movie half needs
    // an anime shelf to belong to, and there is none.
    expect(cardHrefs()).toEqual(['/series/5']);
  });

  it('sorts a merged grid by title across both tables', async () => {
    await open('/anime?sort=title');
    expect(cardHrefs()).toEqual(['/movies/7', '/series/5']);
  });
});
