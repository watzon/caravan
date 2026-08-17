import { describe, expect, it } from 'vitest';
import type { Indexer, IndexerCategory, IndexerDefinition } from './api/types';
import {
  allCategoryIds,
  catalogContentValues,
  catalogLanguages,
  catalogPrivacyValues,
  categoryGroups,
  filterDefinitions,
  feedURLFromBase,
  formatCategories,
  isAdultCategory,
  indexerFormURL,
  parseCategories,
  searchCategoryOptions,
  selectionState,
  toggleCategory,
  toggleFilterValue,
  unknownCategoryIds,
  validateIndexer,
} from './indexer';

describe('parseCategories', () => {
  it('reads the comma-separated form indexers document', () => {
    expect(parseCategories('2000,2040,5000')).toEqual([2000, 2040, 5000]);
  });

  it('tolerates spaces and a trailing comma', () => {
    expect(parseCategories(' 2000 , 5000, ')).toEqual([2000, 5000]);
  });

  it('drops duplicates so an edit round-trips stably', () => {
    expect(parseCategories('2000,2000,5000')).toEqual([2000, 5000]);
  });

  it('drops anything that is not a category id', () => {
    expect(parseCategories('2000,abc,-5,1.5')).toEqual([2000]);
  });

  it('reads an empty field as no categories', () => {
    expect(parseCategories('')).toEqual([]);
    expect(parseCategories('   ')).toEqual([]);
  });
});

describe('formatCategories', () => {
  it('renders stored ids back into the editable form', () => {
    expect(formatCategories([2000, 5000])).toBe('2000, 5000');
  });

  it('renders missing categories as an empty field', () => {
    expect(formatCategories([])).toBe('');
    expect(formatCategories(null)).toBe('');
    expect(formatCategories(undefined)).toBe('');
  });

  it('round-trips through parseCategories', () => {
    expect(parseCategories(formatCategories([2000, 2040]))).toEqual([2000, 2040]);
  });
});

describe('validateIndexer', () => {
  it('accepts a complete config', () => {
    expect(validateIndexer({ name: 'Jackett', url: 'http://127.0.0.1:9117/api' })).toBeNull();
    expect(validateIndexer({ name: 'Jackett', url: 'https://example.test' })).toBeNull();
  });

  it('requires a name', () => {
    expect(validateIndexer({ name: '  ', url: 'https://example.test' })).toMatch(/name/i);
  });

  it('requires a URL', () => {
    expect(validateIndexer({ name: 'Jackett', url: '' })).toMatch(/URL/i);
  });

  it('rejects a URL without a scheme, which fetches as a relative path', () => {
    expect(validateIndexer({ name: 'Jackett', url: '127.0.0.1:9117' })).toMatch(/http/i);
  });

  it('requires an API key only when the definition says so', () => {
    expect(
      validateIndexer({ name: '1337x', url: 'https://1337x.to', requiresAPIKey: false }),
    ).toBeNull();
    expect(
      validateIndexer({ name: 'NZBgeek', url: 'https://api.nzbgeek.info', requiresAPIKey: true }),
    ).toMatch(/API key/i);
    expect(
      validateIndexer({
        name: 'NZBgeek',
        url: 'https://api.nzbgeek.info',
        requiresAPIKey: true,
        apiKey: 'secret',
      }),
    ).toBeNull();
    expect(
      validateIndexer({
        name: 'NZBgeek',
        url: 'https://api.nzbgeek.info',
        requiresAPIKey: true,
        hasStoredKey: true,
      }),
    ).toBeNull();
  });
});

describe('filterDefinitions', () => {
  const defs: IndexerDefinition[] = [
    {
      id: 'nzbgeek',
      name: 'NZBgeek',
      kind: 'usenet' as const,
      protocol: 'newznab' as const,
      privacy: 'private' as const,
      language: 'en-US',
      description: 'Private Usenet indexer.',
      info_url: 'https://nzbgeek.info',
      url: 'https://api.nzbgeek.info',
      urls: ['https://api.nzbgeek.info'],
      url_placeholder: '',
      requires_api_key: true,
      categories: [2000],
      content: ['movies', 'tv', 'adult'],
    },
    {
      id: '1337x',
      name: '1337x',
      kind: 'torrent' as const,
      protocol: 'torznab' as const,
      privacy: 'public' as const,
      language: 'en-US',
      description: 'Public torrent site',
      info_url: 'https://1337x.to/',
      url: 'https://1337x.to',
      urls: ['https://1337x.to'],
      url_placeholder: 'http://127.0.0.1:9117/api/v2.0/indexers/1337x/results/torznab',
      requires_api_key: true,
      categories: [],
      content: ['movies', 'tv', 'anime'],
    },
    {
      id: 'nyaasi',
      name: 'Nyaa.si',
      kind: 'torrent' as const,
      protocol: 'torznab' as const,
      privacy: 'public' as const,
      language: 'en-US',
      description: 'Public anime torrent site',
      info_url: 'https://nyaa.si/',
      url: 'https://nyaa.si',
      urls: ['https://nyaa.si'],
      url_placeholder: '',
      requires_api_key: true,
      categories: [],
      content: ['anime'],
    },
    {
      id: 'beyondhd',
      name: 'BeyondHD',
      kind: 'torrent' as const,
      protocol: 'torznab' as const,
      privacy: 'private' as const,
      language: 'en-US',
      description: 'Private HD tracker',
      info_url: '',
      url: 'https://beyond-hd.me',
      urls: ['https://beyond-hd.me'],
      url_placeholder: '',
      requires_api_key: true,
      categories: [],
      content: ['movies', 'tv'],
    },
    {
      id: 'yggtorrent',
      name: 'Yggtorrent',
      kind: 'torrent' as const,
      protocol: 'torznab' as const,
      privacy: 'private' as const,
      language: 'fr-FR',
      description: 'French private tracker',
      info_url: '',
      url: 'https://www.yggtorrent.top',
      urls: ['https://www.yggtorrent.top'],
      url_placeholder: '',
      requires_api_key: true,
      categories: [],
      content: ['movies', 'tv'],
    },
  ];

  it('returns the whole list for a blank query', () => {
    expect(filterDefinitions(defs, '  ')).toHaveLength(5);
  });

  it('matches name, id, and description', () => {
    expect(filterDefinitions(defs, 'geek').map((d) => d.id)).toEqual(['nzbgeek']);
    expect(filterDefinitions(defs, '1337').map((d) => d.id)).toEqual(['1337x']);
    expect(filterDefinitions(defs, 'french').map((d) => d.id)).toEqual(['yggtorrent']);
  });

  it('filters by privacy, language, and content, ANDed across facets', () => {
    expect(filterDefinitions(defs, '', { privacy: ['public'] }).map((d) => d.id)).toEqual([
      '1337x',
      'nyaasi',
    ]);
    expect(filterDefinitions(defs, '', { languages: ['fr-FR'] }).map((d) => d.id)).toEqual([
      'yggtorrent',
    ]);
    expect(filterDefinitions(defs, '', { content: ['anime'] }).map((d) => d.id)).toEqual([
      '1337x',
      'nyaasi',
    ]);
    expect(
      filterDefinitions(defs, '', { privacy: ['public'], content: ['anime'] }).map((d) => d.id),
    ).toEqual(['1337x', 'nyaasi']);
    expect(
      filterDefinitions(defs, 'nyaa', { privacy: ['public'], content: ['anime'] }).map((d) => d.id),
    ).toEqual(['nyaasi']);
    expect(filterDefinitions(defs, '', { privacy: ['semi-private'] })).toEqual([]);
  });

  it('lists the facet values present in the loaded catalog', () => {
    expect(catalogPrivacyValues(defs)).toEqual(['public', 'private']);
    expect(catalogLanguages(defs)).toEqual(['en-US', 'fr-FR']);
    expect(catalogContentValues(defs)).toEqual(['movies', 'tv', 'anime', 'adult']);
  });

  it('toggles a filter value on and off', () => {
    expect(toggleFilterValue(['public'], 'private')).toEqual(['public', 'private']);
    expect(toggleFilterValue(['public', 'private'], 'public')).toEqual(['private']);
  });
});

/* ----------------------------------------------------------------------------
 * Category-tree selection (CategoryPicker).
 * ------------------------------------------------------------------------- */

/** The shape a Torznab caps document advertises: parents with one subcat level. */
const MOVIES: IndexerCategory = {
  id: 2000,
  name: 'Movies',
  subcats: [
    { id: 2040, name: 'Movies/HD', subcats: [] },
    { id: 2045, name: 'Movies/UHD', subcats: [] },
  ],
};
const ANIME: IndexerCategory = { id: 5070, name: 'Anime', subcats: [] };
const TREE = [MOVIES, ANIME];

describe('allCategoryIds', () => {
  it('flattens parents and children in document order', () => {
    expect(allCategoryIds(TREE)).toEqual([2000, 2040, 2045, 5070]);
  });

  it('reads an empty tree as no ids', () => {
    expect(allCategoryIds([])).toEqual([]);
  });
});

describe('categoryGroups', () => {
  it('groups selected descendants under their top-level parent', () => {
    expect(categoryGroups([2000, 2040, 5070], TREE)).toEqual([
      {
        id: 2000,
        name: 'Movies',
        selected: true,
        children: [{ id: 2040, name: 'HD' }],
      },
      { id: 5070, name: 'Anime', selected: true, children: [] },
    ]);
  });

  it('keeps an unselected parent as context for selected descendants', () => {
    expect(categoryGroups([2045], TREE)).toEqual([
      {
        id: 2000,
        name: 'Movies',
        selected: false,
        children: [{ id: 2045, name: 'UHD' }],
      },
    ]);
  });

  it('keeps ids missing from the advertised tree in a separate group', () => {
    expect(categoryGroups([9999], TREE)).toEqual([
      {
        id: -1,
        name: 'Not advertised',
        selected: false,
        children: [{ id: 9999, name: 'Category 9999' }],
      },
    ]);
  });
});

describe('selectionState', () => {
  it('is none when nothing in the subtree is selected', () => {
    expect(selectionState(MOVIES, new Set([5070]))).toBe('none');
  });

  it('is some when only part of the subtree is selected', () => {
    expect(selectionState(MOVIES, new Set([2040]))).toBe('some');
    expect(selectionState(MOVIES, new Set([2000]))).toBe('some');
  });

  it('is all only when the node and every descendant are selected', () => {
    expect(selectionState(MOVIES, new Set([2000, 2040, 2045]))).toBe('all');
    expect(selectionState(ANIME, new Set([5070]))).toBe('all');
  });
});

describe('toggleCategory', () => {
  it('checking a parent selects the whole subtree explicitly', () => {
    // Explicit ids matter: indexers are not required to expand 2000 into its
    // children server-side (AnimeTosho does not).
    expect([...toggleCategory(MOVIES, new Set())].sort()).toEqual([2000, 2040, 2045]);
  });

  it('unchecking a fully selected parent clears the subtree', () => {
    expect([...toggleCategory(MOVIES, new Set([2000, 2040, 2045, 5070]))]).toEqual([5070]);
  });

  it('checking a partially selected parent completes the subtree, not inverts it', () => {
    expect([...toggleCategory(MOVIES, new Set([2040]))].sort()).toEqual([2000, 2040, 2045]);
  });

  it('toggles a leaf alone', () => {
    expect([...toggleCategory(ANIME, new Set())]).toEqual([5070]);
    expect([...toggleCategory(ANIME, new Set([5070]))]).toEqual([]);
  });

  it('never mutates the selection it was given', () => {
    const selected = new Set([2040]);
    toggleCategory(MOVIES, selected);
    expect([...selected]).toEqual([2040]);
  });
});

describe('unknownCategoryIds', () => {
  it('surfaces manually configured ids the indexer does not advertise', () => {
    expect(unknownCategoryIds(new Set([2000, 9090, 8010]), TREE)).toEqual([8010, 9090]);
  });

  it('is empty when every selected id is advertised', () => {
    expect(unknownCategoryIds(new Set([2000, 5070]), TREE)).toEqual([]);
  });
});

describe('searchCategoryOptions', () => {
  function indexer(id: number, categories: number[], enabled = true): Indexer {
    return {
      id,
      name: `Indexer ${id}`,
      url: 'http://localhost',
      has_api_key: true,
      type: 'torznab',
      categories,
      priority: 0,
      enabled,
    };
  }

  it('offers the standard blocks named by id and label', () => {
    const ids = searchCategoryOptions([], true).map((o) => o.id);
    expect(ids).toEqual([2000, 3000, 4000, 5000, 6000, 7000, 8000]);
    expect(searchCategoryOptions([], true)[0]).toEqual({ id: 2000, name: '2000 Movies' });
  });

  it('hides the adult block when the module is not visible', () => {
    const ids = searchCategoryOptions([], false).map((o) => o.id);
    expect(ids).not.toContain(6000);
  });

  it('adds ids configured on an indexer that the blocks do not cover', () => {
    const options = searchCategoryOptions([indexer(1, [2000, 100042])], true);
    expect(options.map((o) => o.id)).toContain(100042);
    // A private tracker's id has no standard name; the number is what it documents.
    expect(options.find((o) => o.id === 100042)?.name).toBe('100042');
  });

  it('never leaks a configured adult id to a caller the module is absent to', () => {
    const options = searchCategoryOptions([indexer(1, [6010, 6060])], false);
    expect(options.map((o) => o.id)).toEqual([2000, 3000, 4000, 5000, 7000, 8000]);
  });

  it('keeps a configured id that duplicates a block under the block name', () => {
    const options = searchCategoryOptions([indexer(1, [2000])], true);
    expect(options.filter((o) => o.id === 2000)).toEqual([{ id: 2000, name: '2000 Movies' }]);
  });
});

describe('isAdultCategory', () => {
  it('matches the whole 6000 block and nothing else', () => {
    expect(isAdultCategory(6000)).toBe(true);
    expect(isAdultCategory(6999)).toBe(true);
    expect(isAdultCategory(5999)).toBe(false);
    expect(isAdultCategory(7000)).toBe(false);
  });
});

describe('indexerFormURL', () => {
  it('builds the feed URL from the first published site', () => {
    expect(
      indexerFormURL({
        kind: 'torrent',
        url: '',
        urls: ['https://1337x.st/', 'https://x1337x.ws/'],
        url_placeholder: 'http://127.0.0.1:9117/api/v2.0/indexers/1337x/results/torznab',
      }),
    ).toBe('https://1337x.st/api');
  });

  it('appends /api to a native torrent host', () => {
    expect(
      indexerFormURL({
        kind: 'torrent',
        url: 'https://feed.animetosho.org',
        url_placeholder: 'http://127.0.0.1:9117/api/v2.0/indexers/animetosho/results/torznab',
      }),
    ).toBe('https://feed.animetosho.org/api');
  });

  it('preserves an explicit nonstandard Torznab endpoint', () => {
    expect(feedURLFromBase('https://tntracker.org/api/torznab')).toBe(
      'https://tntracker.org/api/torznab',
    );
  });

  it('appends /api to a Usenet API host', () => {
    expect(
      indexerFormURL({
        kind: 'usenet',
        url: 'https://api.nzbgeek.info',
        url_placeholder: '',
      }),
    ).toBe('https://api.nzbgeek.info/api');
  });

  it('keeps a Jackett placeholder when there is no site address', () => {
    expect(
      indexerFormURL({
        kind: 'generic',
        url: '',
        urls: [],
        url_placeholder: 'http://127.0.0.1:9117/api/v2.0/indexers/INDEXER/results/torznab',
      }),
    ).toBe('http://127.0.0.1:9117/api/v2.0/indexers/INDEXER/results/torznab');
  });

  it('keeps a local definition on its tracker base', () => {
    expect(
      indexerFormURL({
        kind: 'torrent',
        definition_id: 'thepiratebay',
        url: 'https://thepiratebay.org',
        urls: ['https://thepiratebay.org/'],
        url_placeholder: '',
      }),
    ).toBe('https://thepiratebay.org');
  });
});
