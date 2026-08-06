import { describe, expect, it } from 'vitest';
import type { Indexer, IndexerCategory } from './api/types';
import {
  allCategoryIds,
  formatCategories,
  isAdultCategory,
  parseCategories,
  searchCategoryOptions,
  selectionState,
  toggleCategory,
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
