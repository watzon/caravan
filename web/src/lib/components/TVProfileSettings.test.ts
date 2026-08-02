import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import TVProfileSettings from './TVProfileSettings.svelte';
import type { Settings, TVProfile } from '../api/types';

const PROFILES: TVProfile[] = [
  {
    id: 'safe',
    name: 'Safe — H.264 8-bit + AAC in MP4, up to 1080p',
    description: 'The common denominator every current TV decodes without help.',
    video_codecs: ['h264'],
    max_bit_depth: 8,
    audio_codecs: ['AAC'],
    containers: ['mp4', 'm4v'],
    max_quality: '1080p',
    active: true,
  },
  {
    id: 'capable',
    name: 'Capable — HEVC Main10 / AV1 + AC3, up to 2160p',
    description: 'A modern set.',
    video_codecs: ['h264', 'hevc', 'av1'],
    max_bit_depth: 10,
    audio_codecs: ['AAC', 'AC3', 'EAC3'],
    containers: ['mp4', 'm4v', 'mkv'],
    max_quality: '2160p',
    active: false,
  },
];

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/tv-profiles')) return jsonResponse({ profiles: PROFILES });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
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

function radios(): HTMLInputElement[] {
  return [...host.querySelectorAll('input[type="radio"]')] as HTMLInputElement[];
}

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

describe('TVProfileSettings', () => {
  it("preselects the server's active profile when the setting is unset", async () => {
    app = mount(TVProfileSettings, {
      target: host,
      props: { settings: {} as Settings, onsave: async () => true },
    });
    await settle();

    const found = radios();
    expect(found).toHaveLength(2);
    expect(found[0]!.checked).toBe(true);
    expect(found[1]!.checked).toBe(false);
    expect(host.textContent).toContain('Capable');
    // The capability description is what makes the choice meaningful.
    expect(host.textContent).toContain('10-bit');
    expect(host.textContent).toContain('MP4/M4V/MKV');
  });

  it('saves the picked profile under the tv_profile key', async () => {
    let saved: Settings | null = null;
    app = mount(TVProfileSettings, {
      target: host,
      props: {
        settings: {} as Settings,
        onsave: async (patch: Settings) => {
          saved = patch;
          return true;
        },
      },
    });
    await settle();

    radios()[1]!.click();
    flushSync();
    button('Save').click();
    await settle();

    expect(saved).toEqual({ tv_profile: 'capable' });
  });

  it('honours a stored setting over the resolved default', async () => {
    app = mount(TVProfileSettings, {
      target: host,
      props: { settings: { tv_profile: 'capable' } as Settings, onsave: async () => true },
    });
    await settle();

    expect(radios()[1]!.checked).toBe(true);
  });
});
