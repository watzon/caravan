import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ItemQualityProfileSelect from './ItemQualityProfileSelect.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

const PROFILES = [
  {
    id: 1,
    name: 'Balanced',
    cutoff: '1080p',
    items: ['1080p'],
    upgrade_allowed: true,
    is_default: true,
    assignments: { libraries: 0, movies: 0, series: 0 },
    created_at: '',
    updated_at: '',
  },
  {
    id: 2,
    name: 'Archive',
    cutoff: '2160p',
    items: ['2160p', '1080p'],
    upgrade_allowed: true,
    is_default: false,
    assignments: { libraries: 0, movies: 0, series: 0 },
    created_at: '',
    updated_at: '',
  },
];

const LIBRARIES = [
  {
    id: 1,
    kind: 'movie',
    name: 'Cinema',
    root_path: 'movies',
    dlna_visible: true,
    route_torrent: '',
    route_usenet: '',
    quality_profile_id: 2,
    indexers: [],
  },
  {
    id: 2,
    kind: 'tv',
    name: 'Television',
    root_path: 'series',
    dlna_visible: true,
    route_torrent: '',
    route_usenet: '',
    quality_profile_id: 0,
    indexers: [],
  },
];

let host: HTMLElement;
let app: Record<string, unknown> | undefined;

function stubChoices() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const body = url.endsWith('/quality-profiles') ? { profiles: PROFILES } : { libraries: LIBRARIES };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

async function waitForProfileChoices() {
  await vi.waitFor(() => {
    if (!host.querySelector('select[aria-label="Quality profile"]')) {
      throw new Error('profile choices have not loaded');
    }
  });
  flushSync();
}

function profileSelect(): HTMLSelectElement {
  const select = host.querySelector<HTMLSelectElement>('select[aria-label="Quality profile"]');
  expect(select, 'quality profile select').not.toBeNull();
  return select!;
}

beforeEach(() => {
  clearToasts();
  host = document.createElement('div');
  document.body.appendChild(host);
  stubChoices();
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  clearToasts();
  vi.unstubAllGlobals();
});

describe('ItemQualityProfileSelect inheritance', () => {
  it('associates the visible label and assignment state with the select', async () => {
    app = mount(ItemQualityProfileSelect, {
      target: host,
      props: { profileID: 0, kind: 'movie', onassign: async () => {} },
    });
    await waitForProfileChoices();

    const select = profileSelect();
    const label = [...host.querySelectorAll<HTMLLabelElement>('label')].find(
      (candidate) => candidate.htmlFor === select.id,
    );
    expect(label?.textContent).toBe('Quality profile');

    const stateID = select.getAttribute('aria-describedby');
    expect(stateID).toBeTruthy();
    expect(host.querySelector(`#${stateID}`)?.textContent).toContain('Inherited from Cinema: Archive');
  });

  it('names the matching library profile for an inherited movie', async () => {
    app = mount(ItemQualityProfileSelect, {
      target: host,
      props: { profileID: 0, kind: 'movie', onassign: async () => {} },
    });
    await waitForProfileChoices();

    expect(host.textContent).toContain('Inherited from Cinema: Archive');
  });

  it('falls back to the system default when the matching library has no profile', async () => {
    app = mount(ItemQualityProfileSelect, {
      target: host,
      props: { profileID: 0, kind: 'tv', onassign: async () => {} },
    });
    await waitForProfileChoices();

    expect(host.textContent).toContain('Inherited from system default: Balanced');
  });

  it('distinguishes an item override from inheritance', async () => {
    app = mount(ItemQualityProfileSelect, {
      target: host,
      props: { profileID: 2, kind: 'movie', onassign: async () => {} },
    });
    await waitForProfileChoices();

    expect(host.textContent).toContain('Override: Archive');
  });
});

describe('ItemQualityProfileSelect assignment', () => {
  it('delegates a changed profile immediately and disables the select while it saves', async () => {
    const assignment = (
      Promise as PromiseConstructor & {
        withResolvers<T>(): {
          promise: Promise<T>;
          resolve: (value: T | PromiseLike<T>) => void;
          reject: (reason?: unknown) => void;
        };
      }
    ).withResolvers<void>();
    const onassign = vi.fn(() => assignment.promise);
    app = mount(ItemQualityProfileSelect, {
      target: host,
      props: { profileID: 0, kind: 'movie', onassign },
    });
    await waitForProfileChoices();

    const select = profileSelect();
    select.value = '1';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();

    expect(onassign).toHaveBeenCalledWith(1);
    expect(select.disabled).toBe(true);

    assignment.resolve();
    await vi.waitFor(() => {
      if (select.disabled) throw new Error('profile assignment is still saving');
    });
    expect(select.disabled).toBe(false);
  });

  it('restores the stored assignment and shows the failure toast', async () => {
    const onassign = vi.fn(async () => {
      throw new Error('Profile update failed');
    });
    app = mount(ItemQualityProfileSelect, {
      target: host,
      props: { profileID: 2, kind: 'movie', onassign },
    });
    await waitForProfileChoices();

    const select = profileSelect();
    select.value = '1';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    await vi.waitFor(() => {
      if (select.value !== '2' || toasts.items[0]?.message !== 'Profile update failed') {
        throw new Error('profile assignment failure has not rendered');
      }
    });

    expect(onassign).toHaveBeenCalledWith(1);
    expect(select.value).toBe('2');
    expect(toasts.items.map((toast) => toast.message)).toEqual(['Profile update failed']);
    expect(toasts.items[0]?.tone).toBe('danger');
  });
});
