import { describe, expect, it } from 'vitest';
import {
  compareShelfTitle,
  filterByLibrary,
  inLibrary,
  readLibraryFilter,
  readShelfSort,
  sortShelf,
  type ShelfItem,
} from './shelf';

function row(over: Partial<ShelfItem> & { id: number }): ShelfItem {
  return {
    title: `Title ${over.id}`,
    sort_title: '',
    added_at: '2026-01-01T00:00:00Z',
    ...over,
  };
}

describe('readShelfSort', () => {
  it('reads the two non-default orders and nothing else', () => {
    expect(readShelfSort('title')).toBe('title');
    expect(readShelfSort('status')).toBe('status');
    expect(readShelfSort('added')).toBe('added');
    expect(readShelfSort('sideways')).toBe('added');
    expect(readShelfSort(null)).toBe('added');
  });
});

describe('readLibraryFilter', () => {
  it('reads a positive id', () => {
    expect(readLibraryFilter(new URLSearchParams('library=4'))).toBe(4);
  });

  it('reads no filter, and every unusable spelling of one, as 0', () => {
    expect(readLibraryFilter(new URLSearchParams(''))).toBe(0);
    expect(readLibraryFilter(new URLSearchParams('library='))).toBe(0);
    expect(readLibraryFilter(new URLSearchParams('library=0'))).toBe(0);
    expect(readLibraryFilter(new URLSearchParams('library=-3'))).toBe(0);
    expect(readLibraryFilter(new URLSearchParams('library=2.5'))).toBe(0);
    expect(readLibraryFilter(new URLSearchParams('library=all'))).toBe(0);
  });
});

describe('inLibrary and filterByLibrary', () => {
  it('matches strictly on library_id', () => {
    expect(inLibrary({ library_id: 4 }, 4)).toBe(true);
    expect(inLibrary({ library_id: 1 }, 4)).toBe(false);
    // A row naming no shelf belongs to none. The server stamps every row, so
    // this is a malformed payload rather than a legacy one — and it is how
    // store.CountLibraryItems counts too, so the badge and the grid agree.
    expect(inLibrary({ library_id: 0 }, 1)).toBe(false);
    expect(inLibrary({}, 1)).toBe(false);
  });

  it('narrows to one shelf, and passes everything through when unfiltered', () => {
    const items = [row({ id: 1, library_id: 1 }), row({ id: 2, library_id: 4 })];
    expect(filterByLibrary(items, 4).map((i) => i.id)).toEqual([2]);
    expect(filterByLibrary(items, 0).map((i) => i.id)).toEqual([1, 2]);
  });

  it('copies rather than aliases, so a caller cannot sort the source in place', () => {
    const items = [row({ id: 1 })];
    expect(filterByLibrary(items, 0)).not.toBe(items);
  });
});

describe('compareShelfTitle', () => {
  it('prefers the sort title, then the display title, then the id', () => {
    const the = row({ id: 1, title: 'The Matrix', sort_title: 'matrix, the' });
    const alpha = row({ id: 2, title: 'Nadir', sort_title: '' });
    expect(compareShelfTitle(the, alpha)).toBeLessThan(0);

    // The middle clause, and the only input that reaches it: one sort title,
    // two display titles. Without it the pair would fall through to the id and
    // put the later-added row first.
    const bravo = row({ id: 9, title: 'Bravo', sort_title: 'same' });
    const alphaSame = row({ id: 3, title: 'Alpha', sort_title: 'same' });
    expect(compareShelfTitle(bravo, alphaSame)).toBeGreaterThan(0);

    const first = row({ id: 3, title: 'Same', sort_title: '' });
    const second = row({ id: 9, title: 'Same', sort_title: '' });
    expect(compareShelfTitle(first, second)).toBeLessThan(0);
  });
});

describe('sortShelf', () => {
  const rows = [
    row({ id: 1, title: 'Bravo', added_at: '2026-01-01T00:00:00Z' }),
    row({ id: 2, title: 'Alpha', added_at: '2026-03-01T00:00:00Z' }),
    row({ id: 3, title: 'Charlie', added_at: '2026-02-01T00:00:00Z' }),
  ];
  const rank = (item: ShelfItem) => (item.id === 3 ? 0 : 1);

  it('orders newest first by default, with title breaking ties', () => {
    expect(sortShelf(rows, 'added', rank).map((r) => r.id)).toEqual([2, 3, 1]);
  });

  it('orders by title', () => {
    expect(sortShelf(rows, 'title', rank).map((r) => r.id)).toEqual([2, 1, 3]);
  });

  it("orders by the caller's ranking, then title", () => {
    expect(sortShelf(rows, 'status', rank).map((r) => r.id)).toEqual([3, 2, 1]);
  });

  it('never mutates the input', () => {
    const input = [...rows];
    sortShelf(input, 'title', rank);
    expect(input.map((r) => r.id)).toEqual([1, 2, 3]);
  });
});
