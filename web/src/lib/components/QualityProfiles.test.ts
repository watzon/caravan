import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import QualityProfiles from './QualityProfiles.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/quality-profiles') && (!init?.method || init.method === 'GET')) {
      return jsonResponse({ profiles: [] });
    }
    if (url.endsWith('/quality-profiles') && init?.method === 'POST') {
      return jsonResponse({ error: 'duplicate profile' }, 409);
    }
    throw new Error(`unexpected fetch: ${url}`);
  }));
  host = document.createElement('div');
  vi.useFakeTimers();
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((candidate) => candidate.textContent?.includes(label));
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

describe('QualityProfiles', () => {
  it('validates modal fields before posting and puts duplicate names on the name field', async () => {
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('New profile').click();
    flushSync();
    button('Create profile').click();
    flushSync();
    expect(host.textContent).toContain('Enter a profile name.');

    const onlyQuality = [...host.querySelectorAll('input[type="checkbox"]')].find(
      (input) => (input as HTMLInputElement).checked,
    ) as HTMLInputElement;
    onlyQuality.click();
    flushSync();
    button('Create profile').click();
    flushSync();
    expect(host.textContent).toContain('Select at least one quality.');

    onlyQuality.click();
    const name = host.querySelector('#quality-profile-name') as HTMLInputElement;
    name.value = 'Cinema';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button('Create profile').click();
    await settle();

    expect(host.textContent).toContain('a profile with that name already exists');
  });
});
