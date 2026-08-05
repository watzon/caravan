/**
 * The category picker's load/reload flow, mounted for real against a stubbed
 * /api/v1. Field report to reproduce: edit an indexer, the caps tree loads (or
 * a reload is clicked) — the tree must actually appear, not only after
 * switching forms and coming back.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import IndexerSettings from './IndexerSettings.svelte';
import type { Indexer, IndexerCategory } from '../api/types';

const GEEK: Indexer = {
  id: 1,
  name: 'NZBGeek',
  url: 'https://api.nzbgeek.info',
  has_api_key: true,
  type: 'newznab',
  categories: [2000],
  enabled: true,
};

const FULL_TREE: IndexerCategory[] = [
  {
    id: 2000,
    name: 'Movies',
    subcats: [
      { id: 2040, name: 'Movies/HD', subcats: [] },
      { id: 2045, name: 'Movies/UHD', subcats: [] },
    ],
  },
  { id: 5000, name: 'TV', subcats: [{ id: 5040, name: 'TV/HD', subcats: [] }] },
];

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
/** Every answer the categories endpoint will give, consumed in order. */
let categoriesAnswers: Array<() => Response>;
let categoriesCalls: number;
let indexerWrites: Array<{ method: string; body: unknown }>;

beforeEach(() => {
  categoriesAnswers = [];
  categoriesCalls = 0;
  indexerWrites = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/indexers/categories')) {
        categoriesCalls += 1;
        const answer = categoriesAnswers.shift();
        if (!answer) throw new Error('unexpected categories fetch');
        return answer();
      }
      if (url.endsWith('/indexers/1') && init?.method === 'PUT') {
        indexerWrites.push({
          method: init.method,
          body: typeof init.body === 'string' ? JSON.parse(init.body) : null,
        });
        return jsonResponse(GEEK);
      }
      if (url.endsWith('/indexers')) return jsonResponse({ indexers: [GEEK] });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
});

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function clickButton(label: string) {
  const button = [...host.querySelectorAll('button')].find((b) =>
    (b.textContent ?? '').includes(label),
  );
  expect(button, `a button labelled "${label}"`).toBeDefined();
  button!.click();
  flushSync();
}

describe('IndexerSettings category picker', () => {
  it('loads the caps tree when the edit form opens', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: FULL_TREE })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();

    expect(categoriesCalls).toBe(1);
    // The tree rendered: subcategory labels exist nowhere but in the caps
    // response, so their presence proves the picker replaced the text input.
    expect(host.textContent).toContain('Movies/HD');
    expect(host.textContent).toContain('TV/HD');
    expect(host.querySelector('#indexer-categories')).toBeNull();
    // The stored selection (2000) is partial: Movies renders indeterminate.
    const movies = [...host.querySelectorAll('[role="checkbox"]')].find((b) =>
      (b.textContent ?? '').includes('Movies'),
    );
    expect(movies?.getAttribute('aria-checked')).toBe('mixed');
  });

  it('recovers via Reload after a failed first load', async () => {
    categoriesAnswers = [
      () => new Response(JSON.stringify({ error: 'rate limited' }), { status: 429 }),
      () => jsonResponse({ categories: FULL_TREE }),
    ];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();

    // Failed load: the text input stays, and the failure is said out loud.
    expect(host.querySelector('#indexer-categories')).not.toBeNull();
    expect(host.textContent).toContain('rate limited');

    clickButton('Load from indexer');
    await settle();

    expect(categoriesCalls).toBe(2);
    expect(host.textContent).toContain('Movies/HD');
    expect(host.querySelector('#indexer-categories')).toBeNull();
  });

  it('shows a fresh tree after Reload replaces a loaded one', async () => {
    categoriesAnswers = [
      () => jsonResponse({ categories: FULL_TREE.slice(0, 1) }),
      () => jsonResponse({ categories: FULL_TREE }),
    ];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    expect(host.textContent).toContain('Movies/HD');
    expect(host.textContent).not.toContain('TV/HD');

    clickButton('Reload from indexer');
    await settle();
    expect(host.textContent).toContain('TV/HD');
  });

  it('starts edits with a blank write-only key and stored hint', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();

    expect((host.querySelector('#indexer-key') as HTMLInputElement).value).toBe('');
    expect(host.textContent).toContain('A key is stored. Leave blank to keep it.');
  });

  it('omits a blank key on ordinary save and sends empty only on Clear', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] }), () => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    clickButton('Save');
    await settle();
    expect(indexerWrites[0]?.body).not.toHaveProperty('api_key');

    clickButton('Edit');
    await settle();
    clickButton('Clear API key');
    clickButton('Save');
    await settle();
    expect(indexerWrites[1]?.body).toHaveProperty('api_key', '');
  });
});
