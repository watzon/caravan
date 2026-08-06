import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import QualityProfiles from './QualityProfiles.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

let host: HTMLElement;
let app: Record<string, unknown>;
let profilesResponse: unknown[];
let defaultResponse: unknown;
let testResponse: unknown;
let createResponse: unknown | null;
let updateResponse: unknown | null;
let importResponse: unknown;

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/quality-profiles/2/default') && init?.method === 'PUT') {
      return jsonResponse(defaultResponse);
    }
    if (url.endsWith('/quality-profiles/2/test') && init?.method === 'POST') {
      return jsonResponse(testResponse);
    }
    if (url.endsWith('/quality-profiles/import') && init?.method === 'POST') {
      return jsonResponse(importResponse);
    }
    if (/\/quality-profiles\/\d+$/.test(url) && init?.method === 'DELETE') {
      return jsonResponse({ error: 'profile deletion failed' }, 500);
    }
    if (/\/quality-profiles\/\d+$/.test(url) && init?.method === 'PUT') {
      return jsonResponse(updateResponse);
    }
    if (url.endsWith('/quality-profiles') && (!init?.method || init.method === 'GET')) {
      return jsonResponse({ profiles: profilesResponse });
    }
    if (url.endsWith('/quality-profiles') && init?.method === 'POST') {
      if (createResponse !== null) return jsonResponse(createResponse);
      return jsonResponse({ error: 'duplicate profile' }, 409);
    }
    throw new Error(`unexpected fetch: ${url}`);
  }));
  profilesResponse = [];
  defaultResponse = {};
  testResponse = { results: [] };
  createResponse = null;
  updateResponse = null;
  importResponse = { profiles: 0 };
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
});

async function settle() {
  flushSync();
  await new Promise((resolve) => setTimeout(resolve, 20));
  flushSync();
}

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((candidate) => candidate.textContent?.includes(label));
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

function describedText(element: Element): string {
  const id = element.getAttribute('aria-describedby');
  expect(id, 'aria-describedby').toBeTruthy();
  const description = id ? host.querySelector(`#${id}`) : null;
  expect(description, `description ${id}`).not.toBeNull();
  return description?.textContent?.trim() ?? '';
}

describe('QualityProfiles', () => {

  it('disables invalid profile saves while retaining validation and duplicate feedback', async () => {
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('New profile').click();
    flushSync();
    const unchanged = button('No changes') as HTMLButtonElement;
    expect(unchanged.disabled).toBe(true);

    const onlyQuality = [...host.querySelectorAll('input[type="checkbox"]')].find(
      (input) => (input as HTMLInputElement).checked,
    ) as HTMLInputElement;
    onlyQuality.click();
    flushSync();
    const invalid = button('Create profile') as HTMLButtonElement;
    expect(invalid.disabled).toBe(true);
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    flushSync();
    expect(host.textContent).toContain('Enter a profile name.');
    expect(host.textContent).toContain('Select at least one quality.');

    const qualityGroup = host.querySelector('[role="group"][aria-label="Allowed qualities"]')!;
    expect(host.querySelector('#quality-profile-items-error[role="alert"]')).not.toBeNull();
    expect(describedText(qualityGroup)).toBe('Select at least one quality.');

    const cutoff = host.querySelector('#quality-profile-cutoff')!;
    expect(cutoff.getAttribute('aria-invalid')).toBe('true');
    expect(describedText(cutoff)).toBe('Choose a cutoff from the selected qualities.');

    onlyQuality.click();
    const name = host.querySelector('#quality-profile-name') as HTMLInputElement;
    name.value = 'Cinema';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    const create = button('Create profile') as HTMLButtonElement;
    expect(create.disabled).toBe(false);
    create.click();
    await settle();

    expect(host.textContent).toContain('A profile with that name already exists.');
  });

  it('explains acceptance, ranking, penalties, compatibility, and test score contributions', async () => {
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('New profile').click();
    flushSync();
    button('Add custom format').click();
    flushSync();

    expect(host.textContent).toContain('Allowed qualities plus this cutoff define which releases are accepted.');
    expect(host.textContent).toContain('Preferred sources affect ranking.');
    expect(host.textContent).toContain('Negative scores penalize matching releases.');
    expect(host.textContent).toContain('Prefer adds a compatibility bonus.');
    expect(host.textContent).toContain('Require rejects releases that do not match the selected TV target.');
    expect(host.textContent).toContain('shows each score contribution below.');

    expect(describedText(host.querySelector('#quality-profile-cutoff')!)).toContain(
      'define which releases are accepted',
    );
    expect(describedText(host.querySelector('[aria-label="Preferred source order"]')!)).toContain(
      'Preferred sources affect ranking',
    );
    expect(
      describedText(host.querySelector('[aria-label="Proper and Repack preference"]')!),
    ).toContain('Neutral ignores proper and repack tags');
    expect(describedText(host.querySelector('#quality-profile-min-seeders')!)).toContain(
      'Applies to torrent releases only',
    );
    expect(
      describedText(host.querySelector('[aria-label="TV compatibility policy"]')!),
    ).toContain('Require rejects releases');
    expect(describedText(host.querySelector('#quality-format-score-0')!)).toContain(
      'Negative scores penalize matching releases',
    );
    expect(button('Cancel').closest('.flex-wrap')).not.toBeNull();
  });

  it('creates with the persisted policy defaults', async () => {
    createResponse = {
      id: 3,
      name: 'Cinema',
      cutoff: '1080p',
      items: ['1080p'],
      upgrade_allowed: true,
      preferred_sources: [],
      proper_repack_preference: 'prefer',
      min_seeders: 0,
      min_size_mb: 0,
      max_size_mb: 0,
      custom_formats: [],
      tv_profile: 'safe',
      tv_compatibility_policy: 'ignore',
      is_default: false,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('New profile').click();
    flushSync();
    const name = host.querySelector('#quality-profile-name') as HTMLInputElement;
    name.value = 'Cinema';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button('Create profile').click();
    await settle();

    const createCall = vi.mocked(fetch).mock.calls.find(
      ([url, init]) => String(url).endsWith('/quality-profiles') && init?.method === 'POST',
    );
    expect(createCall).toBeDefined();
    expect(JSON.parse((createCall![1] as RequestInit).body as string)).toEqual({
      name: 'Cinema',
      items: ['1080p'],
      cutoff: '1080p',
      upgrade_allowed: true,
      preferred_sources: [],
      proper_repack_preference: 'prefer',
      min_seeders: 0,
      min_size_mb: 0,
      max_size_mb: 0,
      custom_formats: [],
      tv_profile: 'safe',
      tv_compatibility_policy: 'ignore',
    });
  });

  it('protects changed profile drafts and enables save only after a valid change', async () => {
    profilesResponse = [{
      id: 1,
      name: 'HD',
      cutoff: '1080p',
      items: ['1080p'],
      upgrade_allowed: true,
      is_default: true,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }];
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('Edit').click();
    flushSync();
    const unchanged = button('No changes') as HTMLButtonElement;
    expect(unchanged.disabled).toBe(true);

    const name = host.querySelector('#quality-profile-name') as HTMLInputElement;
    name.value = 'Ultra HD';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    const save = button('Save changes') as HTMLButtonElement;
    expect(save.disabled).toBe(false);

    const close = host.querySelector<HTMLButtonElement>('[role="dialog"] button[aria-label="Close"]');
    expect(close, 'the profile editor close button').not.toBeNull();
    close!.click();
    await settle();
    expect(host.textContent).toContain('Discard changes');
    button('Keep editing').click();
    await settle();
    expect((host.querySelector('#quality-profile-name') as HTMLInputElement).value).toBe('Ultra HD');

    const secondClose = host.querySelector<HTMLButtonElement>('[role="dialog"] button[aria-label="Close"]');
    expect(secondClose, 'the restored profile editor close button').not.toBeNull();
    secondClose!.click();
    await settle();
    button('Discard changes').click();
    await settle();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it('explains the active policy, assignments, default, and protected deletion', async () => {
    profilesResponse = [{
      id: 1,
      name: '4K',
      cutoff: '1080p',
      items: ['2160p', '1080p'],
      upgrade_allowed: true,
      is_default: true,
      assignments: { libraries: 1, movies: 2, series: 1 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }, {
      id: 2,
      name: 'HD',
      cutoff: '720p',
      items: ['1080p', '720p'],
      upgrade_allowed: false,
      is_default: false,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }];
    app = mount(QualityProfiles, { target: host });
    await settle();

    expect(host.textContent).toContain('Accepts 2160p and 1080p. Upgrades toward the 1080p cutoff.');
    expect(host.textContent).toContain('Direct assignments:');
    expect(host.textContent).toContain('1 library · 3 titles');
    expect(host.textContent).toContain('(2 movies, 1 series)');
    expect(host.textContent).toContain('System default');
    expect(host.textContent).toContain('Deletion unavailable: It is the system default. It is assigned to 1 library · 3 titles.');
    expect(host.textContent).toContain('Deletion available.');

    const deleteButtons = [...host.querySelectorAll<HTMLButtonElement>('button')].filter(
      (candidate) => candidate.textContent === 'Delete',
    );
    expect(deleteButtons).toHaveLength(2);
    expect(deleteButtons[0]?.disabled).toBe(true);
    expect(deleteButtons[1]?.disabled).toBe(false);

    deleteButtons[1]!.click();
    flushSync();
    const deleteDialog = host.querySelector<HTMLElement>('[role="dialog"]');
    const confirmDelete = [...deleteDialog!.querySelectorAll<HTMLButtonElement>('button')].find(
      (candidate) => candidate.textContent?.trim() === 'Delete',
    );
    expect(confirmDelete).toBeDefined();
    confirmDelete!.click();
    await settle();
    expect(deleteDialog!.querySelector('[role="alert"]')?.textContent).toBe(
      'profile deletion failed',
    );
  });

  it('clones a distinct policy draft and can set the clone source as the default', async () => {
    profilesResponse = [{
      id: 1,
      name: '4K',
      cutoff: '1080p',
      items: ['2160p', '1080p'],
      upgrade_allowed: true,
      is_default: true,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }, {
      id: 2,
      name: 'HD',
      cutoff: '720p',
      items: ['1080p', '720p'],
      upgrade_allowed: false,
      is_default: false,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }];
    defaultResponse = {
      id: 2,
      name: 'HD',
      cutoff: '720p',
      items: ['1080p', '720p'],
      upgrade_allowed: false,
      is_default: true,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('Set as default').click();
    await settle();
    expect(vi.mocked(fetch)).toHaveBeenCalledWith(
      '/api/v1/quality-profiles/2/default',
      expect.objectContaining({ method: 'PUT' }),
    );

    button('Clone').click();
    flushSync();
    expect((host.querySelector('#quality-profile-name') as HTMLInputElement).value).toBe('4K copy');
    expect((button('Create profile') as HTMLButtonElement).disabled).toBe(false);
    expect(host.textContent).toContain('Accepts 2160p and 1080p. Upgrades toward the 1080p cutoff.');
  });

  it('renders server-owned profile test decisions with parsed tags and score contributions', async () => {
    profilesResponse = [{
      id: 2,
      name: 'HD',
      cutoff: '1080p',
      items: ['1080p'],
      upgrade_allowed: true,
      is_default: false,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }];
    testResponse = {
      results: [{
        title: 'Film.2026.1080p.WEB-DL',
        parsed: { quality: '1080p', source: 'WEB-DL' },
        decision: {
          accepted: true,
          profile_id: 2,
          profile_name: 'HD',
          score: 42,
          reason: 'Accepted at the cutoff.',
          contributions: { quality: 20, source: 10, proper: 0, repack: 0, seeders: 12 },
        },
      }, {
        title: 'Film.2026.480p.CAM',
        parsed: { quality: '480p', source: 'CAM' },
        decision: {
          accepted: false,
          profile_id: 2,
          profile_name: 'HD',
          score: 0,
          reason: 'Rejected: 480p is not an accepted quality.',
          contributions: { quality: 0, source: 0, proper: 0, repack: 0, seeders: 0 },
        },
      }],
    };
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('Edit').click();
    flushSync();
    const titles = host.querySelector('#quality-profile-test-titles') as HTMLTextAreaElement;
    titles.value = 'Film.2026.1080p.WEB-DL\nFilm.2026.480p.CAM';
    titles.dispatchEvent(new Event('input', { bubbles: true }));
    button('Test releases').click();
    await settle();

    expect(host.textContent).toContain('Accepted');
    expect(host.textContent).toContain('Rejected');
    expect(host.textContent).toContain('WEB-DL');
    expect(host.textContent).toContain('Score 42');
    expect(host.textContent).toContain('Rejected: 480p is not an accepted quality.');
    expect(host.textContent).toContain('Seeders');
  });
  it('saves every policy field and preserves source reordering', async () => {
    const profile = {
      id: 7,
      name: 'Cinema',
      cutoff: '1080p',
      items: ['2160p', '1080p'],
      upgrade_allowed: true,
      preferred_sources: ['bluray', 'webdl', 'webrip'],
      proper_repack_preference: 'prefer',
      min_seeders: 4,
      min_size_mb: 500,
      max_size_mb: 9000,
      custom_formats: [{ name: 'HDR', include_terms: ['hdr', 'dv'], exclude_terms: ['sdr'], score: 12 }],
      tv_profile: 'capable',
      tv_compatibility_policy: 'prefer',
      is_default: false,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
    profilesResponse = [profile];
    updateResponse = { ...profile, name: 'Cinema Plus' };
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('Edit').click();
    flushSync();
    const name = host.querySelector('#quality-profile-name') as HTMLInputElement;
    name.value = 'Cinema Plus';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    button('Move webdl down').click();
    (host.querySelector('input[name="proper-repack-preference"][value="neutral"]') as HTMLInputElement).click();
    const seeders = host.querySelector('#quality-profile-min-seeders') as HTMLInputElement;
    seeders.value = '6';
    seeders.dispatchEvent(new Event('input', { bubbles: true }));
    const minSize = host.querySelector('#quality-profile-min-size') as HTMLInputElement;
    minSize.value = '750';
    minSize.dispatchEvent(new Event('input', { bubbles: true }));
    const maxSize = host.querySelector('#quality-profile-max-size') as HTMLInputElement;
    maxSize.value = '9500';
    maxSize.dispatchEvent(new Event('input', { bubbles: true }));
    (host.querySelector('input[name="tv-compatibility-policy"][value="require"]') as HTMLInputElement).click();
    flushSync();
    button('Save changes').click();
    await settle();

    const updateCall = vi.mocked(fetch).mock.calls.find(
      ([url, init]) => String(url).endsWith('/quality-profiles/7') && init?.method === 'PUT',
    );
    expect(updateCall).toBeDefined();
    expect(JSON.parse((updateCall![1] as RequestInit).body as string)).toEqual({
      name: 'Cinema Plus',
      items: ['2160p', '1080p'],
      cutoff: '1080p',
      upgrade_allowed: true,
      preferred_sources: ['bluray', 'webrip', 'webdl'],
      proper_repack_preference: 'neutral',
      min_seeders: 6,
      min_size_mb: 750,
      max_size_mb: 9500,
      custom_formats: [{ name: 'HDR', include_terms: ['hdr', 'dv'], exclude_terms: ['sdr'], score: 12 }],
      tv_profile: 'capable',
      tv_compatibility_policy: 'require',
    });
  });

  it('blocks invalid size bounds and incomplete custom formats with field errors', async () => {
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('New profile').click();
    flushSync();
    const name = host.querySelector('#quality-profile-name') as HTMLInputElement;
    name.value = 'Cinema';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    const minSize = host.querySelector('#quality-profile-min-size') as HTMLInputElement;
    minSize.value = '900';
    minSize.dispatchEvent(new Event('input', { bubbles: true }));
    const maxSize = host.querySelector('#quality-profile-max-size') as HTMLInputElement;
    maxSize.value = '800';
    maxSize.dispatchEvent(new Event('input', { bubbles: true }));
    button('Add custom format').click();
    flushSync();
    const score = host.querySelector('#quality-format-score-0') as HTMLInputElement;
    score.value = '1.5';
    score.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    expect((button('Create profile') as HTMLButtonElement).disabled).toBe(true);
    expect(host.textContent).toContain('Maximum size must be at least the minimum size');
    expect(host.textContent).toContain('Enter a rule name.');
    expect(host.textContent).toContain('Enter one or more include terms');
    expect(host.textContent).toContain('Score must be a whole number.');
  });

  it('clones all persisted policy values', async () => {
    const profile = {
      id: 8,
      name: 'Cinema',
      cutoff: '1080p',
      items: ['2160p', '1080p'],
      upgrade_allowed: false,
      preferred_sources: ['webdl', 'bluray'],
      proper_repack_preference: 'neutral',
      min_seeders: 3,
      min_size_mb: 400,
      max_size_mb: 8000,
      custom_formats: [{ name: 'Atmos', include_terms: ['atmos'], exclude_terms: ['dubbed'], score: 25 }],
      tv_profile: 'capable',
      tv_compatibility_policy: 'require',
      is_default: false,
      assignments: { libraries: 0, movies: 0, series: 0 },
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
    profilesResponse = [profile];
    createResponse = { ...profile, id: 9, name: 'Cinema copy' };
    app = mount(QualityProfiles, { target: host });
    await settle();

    button('Clone').click();
    flushSync();
    button('Create profile').click();
    await settle();

    const createCall = vi.mocked(fetch).mock.calls.find(
      ([url, init]) => String(url).endsWith('/quality-profiles') && init?.method === 'POST',
    );
    expect(JSON.parse((createCall![1] as RequestInit).body as string)).toMatchObject({
      name: 'Cinema copy',
      preferred_sources: ['webdl', 'bluray'],
      proper_repack_preference: 'neutral',
      min_seeders: 3,
      min_size_mb: 400,
      max_size_mb: 8000,
      custom_formats: [{ name: 'Atmos', include_terms: ['atmos'], exclude_terms: ['dubbed'], score: 25 }],
      tv_profile: 'capable',
      tv_compatibility_policy: 'require',
    });
  });

  it('waits for import confirmation, sends the selected JSON, and reloads profiles', async () => {
    const payload = {
      version: 1 as const,
      default_profile: 'Cinema',
      profiles: [{
        name: 'Cinema',
        cutoff: '1080p',
        items: ['1080p'],
        upgrade_allowed: true,
        preferred_sources: [],
        proper_repack_preference: 'prefer' as const,
        min_seeders: 0,
        min_size_mb: 0,
        max_size_mb: 0,
        custom_formats: [],
        tv_profile: 'safe' as const,
        tv_compatibility_policy: 'ignore' as const,
      }],
    };
    importResponse = { profiles: 1 };
    app = mount(QualityProfiles, { target: host });
    await settle();
    const exportLink = [...host.querySelectorAll('a')].find((candidate) =>
      candidate.textContent?.includes('Export JSON'),
    );
    expect(exportLink?.getAttribute('href')).toBe('/api/v1/quality-profiles/export');

    const file = new File([JSON.stringify(payload)], 'quality-profiles.json', { type: 'application/json' });
    Object.defineProperty(file, 'text', { value: async () => JSON.stringify(payload) });
    const input = host.querySelector('input[type="file"]') as HTMLInputElement;
    Object.defineProperty(input, 'files', { value: [file] });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    expect(host.textContent).toContain('Import quality profiles');
    expect(vi.mocked(fetch).mock.calls.some(
      ([url, init]) => String(url).endsWith('/quality-profiles/import') && init?.method === 'POST',
    )).toBe(false);

    button('Import profiles').click();
    await settle();

    const importCall = vi.mocked(fetch).mock.calls.find(
      ([url, init]) => String(url).endsWith('/quality-profiles/import') && init?.method === 'POST',
    );
    expect(importCall).toBeDefined();
    expect(JSON.parse((importCall![1] as RequestInit).body as string)).toEqual(payload);
    expect(vi.mocked(fetch).mock.calls.filter(
      ([url, init]) => String(url).endsWith('/quality-profiles') && (!init?.method || init.method === 'GET'),
    )).toHaveLength(2);
  });

});
