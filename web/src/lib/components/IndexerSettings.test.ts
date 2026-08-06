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
  priority: 25,
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

function editor(): HTMLElement {
  const el = host.querySelector<HTMLElement>('[role="dialog"]');
  expect(el, 'the indexer editor').not.toBeNull();
  return el!;
}

function type(id: string, value: string) {
  const el = host.querySelector<HTMLInputElement>(`#${id}`);
  expect(el, `an input #${id}`).not.toBeNull();
  el!.value = value;
  el!.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

describe('IndexerSettings category picker', () => {
  it('names compact row controls and exposes the full truncated values', async () => {
    app = mount(IndexerSettings, { target: host });
    await settle();

    expect(host.querySelector('span[title="NZBGeek"]')?.textContent).toBe('NZBGeek');
    expect(host.textContent).toContain('Enabled');
    expect(host.querySelector('span.bg-success')?.getAttribute('aria-hidden')).toBe('true');
    expect(host.querySelector('p[title="https://api.nzbgeek.info"]')?.textContent?.trim()).toBe(
      'https://api.nzbgeek.info',
    );

    const remove = [...host.querySelectorAll<HTMLButtonElement>('button')].find((candidate) =>
      candidate.textContent?.includes('Remove NZBGeek'),
    );
    expect(remove).toBeDefined();
    expect(remove?.textContent?.trim()).toBe('Remove NZBGeek');
    expect(remove?.title).toBe('Remove NZBGeek');
    expect(remove?.parentElement?.classList.contains('w-full')).toBe(true);
  });

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
    expect(host.querySelector('#indexer-categories')?.closest('[data-settings-advanced]')).not.toBeNull();
    expect((host.querySelector('#indexer-priority') as HTMLInputElement).value).toBe('25');
    expect(host.textContent).toContain('Priority 25');
  });

  it('disables unchanged Save and writes only changed drafts', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] }), () => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    const unchangedSave = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'No changes',
    );
    expect(unchangedSave).toBeDefined();
    expect(unchangedSave!.disabled).toBe(true);
    expect(indexerWrites).toHaveLength(0);

    type('indexer-name', 'NZBGeek renamed');
    clickButton('Save');
    await settle();
    expect(indexerWrites[0]?.body).toMatchObject({ name: 'NZBGeek renamed' });
    expect(indexerWrites[0]?.body).not.toHaveProperty('api_key');

    clickButton('Edit');
    await settle();
    clickButton('Clear API key');
    clickButton('Save');
    await settle();
    expect(indexerWrites[1]?.body).toHaveProperty('api_key', '');
  });

  it('validates and saves search priority', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    type('indexer-priority', '-1');
    expect(host.textContent).toContain('Priority must be a whole number of zero or greater.');
    const invalidSave = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'Fix errors',
    );
    expect(invalidSave?.disabled).toBe(true);
    expect(indexerWrites).toHaveLength(0);

    type('indexer-priority', '7');
    clickButton('Save');
    await settle();
    expect(indexerWrites[0]?.body).toMatchObject({ priority: 7 });
  });

  it('keeps a dirty draft open until Modal discards it', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    type('indexer-name', 'Unsaved');

    const dialog = editor();
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(host.textContent).toContain('Discard changes');
    expect(host.querySelector('[role="dialog"]')).toBe(dialog);
    clickButton('Keep editing');

    const backdrop = host.querySelector<HTMLElement>('[data-modal-backdrop]');
    expect(backdrop).not.toBeNull();
    backdrop!.click();
    await settle();
    expect(host.textContent).toContain('Discard changes');
    clickButton('Keep editing');

    const close = dialog.querySelector<HTMLButtonElement>('button[aria-label="Close"]');
    expect(close).not.toBeNull();
    close!.click();
    await settle();
    expect(host.textContent).toContain('Discard changes');
    clickButton('Discard changes');
    await settle();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });
});
