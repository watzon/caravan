/**
 * Settings → Adult content (PLAN phase 9 task 7a).
 *
 * This pane IS the module's control plane: without it there is no in-product
 * way to turn the module on or to hand a housemate a grant. So the tests are
 * about the three remaining cards existing and writing the right thing, and
 * about the module's own promise — nothing below the master switch is on
 * screen, or fetched, while the switch is off.
 *
 * The metadata source is no longer one of them (PLAN Part 2 phase 8): stash-box
 * endpoints are configured per instance on Settings → Metadata, so the
 * assertions about that card moved to StashboxSettings.test.ts and the negative
 * half stayed here.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AdultSettings from './AdultSettings.svelte';
import type { AdultUser, Library, Settings } from '../api/types';
import { session } from '../state/session.svelte';
import { clearToasts } from '../state/toast.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  if (status === 204) return new Response(null, { status });
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const ROSTER: AdultUser[] = [
  { id: 1, username: 'root', role: 'admin', granted: false, always_granted: true },
  { id: 2, username: 'ada', role: 'member', granted: false, always_granted: false },
];

const ADULT_LIBRARY: Library = {
  id: 3,
  kind: 'adult',
  name: 'Adult',
  root_path: 'library/Adult',
  provider: 'stashbox',
  providers: ['stashbox'],
  is_default: true,
  item_count: 0,
  dlna_visible: false,
  route_torrent: '',
  route_usenet: '',
  quality_profile_id: 0,
  indexers: [],
};

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let users: AdultUser[];
let libraries: Library[];
let calls: { url: string; method: string; body: unknown }[];

beforeEach(() => {
  users = ROSTER.map((u) => ({ ...u }));
  libraries = [ADULT_LIBRARY];
  calls = [];
  clearToasts();
  session.user = { username: 'root', role: 'admin', open: false, adult: true };
  host = document.createElement('div');
  document.body.appendChild(host);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = init?.body ? JSON.parse(String(init.body)) : null;
      calls.push({ url, method, body });

      if (url.endsWith('/auth/me')) return jsonResponse(session.user);
      if (url.endsWith('/settings/adult')) return jsonResponse({ enabled: body.enabled });
      if (url.endsWith('/adult/users')) return jsonResponse({ users });
      if (url.endsWith('/libraries')) return jsonResponse({ libraries });
      const access = /\/adult\/users\/(\d+)\/access$/.exec(url);
      if (access) {
        const id = Number(access[1]);
        users = users.map((u) => (u.id === id ? { ...u, granted: body.granted } : u));
        return jsonResponse(users.find((u) => u.id === id));
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.unstubAllGlobals();
});

async function settle() {
  // Each card's data is a fetch plus a json() plus the state write, so one
  // microtask turn is not enough to see the rendered result.
  for (let i = 0; i < 5; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function mountPane(settings: Settings) {
  app = mount(AdultSettings, { target: host, props: { settings } });
  await settle();
}

function switches(): HTMLButtonElement[] {
  return [...host.querySelectorAll('[role="switch"]')] as HTMLButtonElement[];
}

/** The nth switch on screen, asserted present so the tests read as assertions. */
function toggle(index: number): HTMLButtonElement {
  const found = switches()[index];
  expect(found, `switch #${index}`).toBeDefined();
  return found as HTMLButtonElement;
}

/**
 * A button inside the setup modal. Scoped, because "Enable adult content" is
 * also the master switch's own label — an unscoped lookup would find the
 * toggle behind the dialog and click that instead.
 */
function modalButton(label: string): HTMLButtonElement {
  const dialog = host.querySelector('[role="dialog"]');
  expect(dialog, 'setup modal').not.toBeNull();
  const found = [...dialog!.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === label,
  );
  expect(found, `modal button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

describe('AdultSettings', () => {
  // The disabled acceptance, on the one screen that must still render a control
  // while the module is off: the master switch, and nothing else.
  it('shows only the master switch while the module is off, and asks for nothing', async () => {
    await mountPane({});

    expect(switches()).toHaveLength(1);
    expect(host.textContent).toContain('Enable adult content');
    for (const gone of ['Metadata source', 'Member access', 'Where it reaches']) {
      expect(host.textContent).not.toContain(gone);
    }
    // No roster, no libraries, no provider — an off module makes no requests.
    expect(calls).toEqual([]);
  });

  // The switch no longer writes. Enabling states what it exposes first, so the
  // switch opens the setup modal and nothing is written until it finishes.
  it('opens the setup modal instead of enabling, and writes nothing yet', async () => {
    await mountPane({});

    toggle(0).click();
    await settle();

    expect(host.textContent).toContain('DLNA stays off');
    expect(calls).toEqual([]);
  });

  it('turns the module on once the setup modal reports it enabled, and refreshes the session', async () => {
    await mountPane({});

    toggle(0).click();
    await settle();
    modalButton('Enable adult content').click();
    await settle();

    // One atomic call. This install already has an instance — the stub answers
    // as the server does for a re-enable — so nothing about a stash-box travels
    // with it.
    expect(calls[0]).toMatchObject({
      method: 'POST',
      body: { enabled: true },
      url: expect.stringContaining('/settings/adult'),
    });
    // The sidebar, Discover and the request form all read session.adult, which
    // only /auth/me can answer.
    expect(calls.some((c) => c.url.endsWith('/auth/me'))).toBe(true);
    expect(host.textContent).toContain('Member access');
  });

  // The card moved to Settings → Metadata, where an install with several
  // stash-boxes has a row per box instead of one endpoint field.
  it('no longer configures the metadata source', async () => {
    await mountPane({ adult_enabled: 'true' });

    expect(host.textContent).not.toContain('Metadata source');
    expect(host.querySelector('#stashbox-endpoint')).toBeNull();
    expect(host.querySelector('#stashbox-api-key')).toBeNull();
  });

  // Disabling exposes nothing and deletes nothing, so it never prompts.
  it('disables straight through, with no modal', async () => {
    await mountPane({ adult_enabled: 'true' });

    toggle(0).click();
    await settle();

    expect(calls.some((c) => c.body && (c.body as { enabled: boolean }).enabled === false)).toBe(
      true,
    );
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it('grants a member and leaves admins without a toggle', async () => {
    await mountPane({ adult_enabled: 'true' });

    expect(host.textContent).toContain('Always has access');
    // One switch for the master toggle, one for the single member: the admin
    // row has no control at all, because a grant would mean nothing on it.
    expect(switches()).toHaveLength(2);
    const memberSwitch = toggle(1);
    expect(memberSwitch.getAttribute('aria-checked')).toBe('false');
    expect(memberSwitch.getAttribute('aria-label')).toBe('Adult content for ada');
    expect(memberSwitch.getAttribute('title')).toBe('Adult content for ada');
    expect(memberSwitch.parentElement?.classList.contains('w-full')).toBe(true);
    expect(host.querySelector('span[title="ada"]')?.textContent).toBe('ada');

    memberSwitch.click();
    await settle();

    const write = calls.find((c) => c.method === 'PUT');
    expect(write?.url).toContain('/adult/users/2/access');
    expect(write?.body).toEqual({ granted: true });
    expect(toggle(1).getAttribute('aria-checked')).toBe('true');
  });

  // "Where it reaches" is the card that states the two surfaces that leave the
  // browser. It reports the DLNA share rather than editing it — the toggle for
  // that lives on the Playback card — so the state has to come off the library.
  it('reports the DLNA share and the prepared-drive rule', async () => {
    await mountPane({ adult_enabled: 'true' });

    expect(host.textContent).toContain('Where it reaches');
    expect(host.textContent).toContain('Not shared');
    expect(host.textContent).toContain('--include-adult');
    expect(host.textContent).not.toContain('every device on this network can browse');
  });

  it('warns when the Adult library is already on the network', async () => {
    libraries = [{ ...ADULT_LIBRARY, dlna_visible: true }];
    await mountPane({ adult_enabled: 'true' });

    expect(host.textContent).toContain('Shared on this network');
    expect(host.textContent).toContain(
      'DLNA has no accounts — every device on this network can browse anything shared here.',
    );
  });
});
