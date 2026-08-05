import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { Settings } from '../api/types';
import EngineSettings from './EngineSettings.svelte';

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
});

const STORED: Settings = {
  engine_listen_port: '51413',
  engine_max_connections: '80',
  engine_max_down_kbps: '2048',
  engine_max_up_kbps: '256',
  engine_seed_ratio: '2.5',
  engine_seed_days: '14',
};

function mountCard(
  settings: Settings = STORED,
  onsave = vi.fn(async (_patch: Settings) => true),
) {
  app = mount(EngineSettings, { target: host, props: { settings, onsave } });
  flushSync();
  return onsave;
}

function input(id: string): HTMLInputElement {
  const el = host.querySelector(`#${id}`) as HTMLInputElement | null;
  expect(el, `#${id}`).not.toBeNull();
  return el!;
}

function clear(id: string) {
  const el = input(id);
  el.value = '';
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function save() {
  const btn = [...host.querySelectorAll('button')].find((b) => b.textContent?.includes('Save changes'));
  expect(btn, 'the save button').toBeDefined();
  btn!.click();
}

describe('EngineSettings', () => {
  it('loads the stored values', () => {
    mountCard();
    expect(input('engine-listen-port').value).toBe('51413');
    expect(input('engine-max-down-kbps').value).toBe('2048');
    expect(input('engine-seed-ratio').value).toBe('2.5');
  });

  /**
   * A cleared number input binds null, not '', so String(value) is the four
   * characters "null" — which the server rejects as an invalid setting. Every
   * one of these fields is "0 means off", so clearing one is a legitimate way
   * to say "no limit" and has to reach the server as 0.
   *
   * Before the fix this saved {"engine_max_down_kbps":"null", ...} and PUT
   * /settings answered 400, so blanking any engine field silently failed.
   */
  it('saves a cleared number field as 0 rather than the string "null"', async () => {
    const onsave = mountCard();

    clear('engine-max-down-kbps');
    clear('engine-max-up-kbps');
    clear('engine-seed-days');
    save();
    await Promise.resolve();

    expect(onsave).toHaveBeenCalledTimes(1);
    const patch = onsave.mock.calls[0]![0];
    for (const [key, value] of Object.entries(patch)) {
      expect(value, `${key} must be a number the server will accept`).toMatch(/^\d+(\.\d+)?$/);
    }
    expect(patch.engine_max_down_kbps).toBe('0');
    expect(patch.engine_max_up_kbps).toBe('0');
    expect(patch.engine_seed_days).toBe('0');
    // The fields the user did not touch keep their stored values.
    expect(patch.engine_listen_port).toBe('51413');
    expect(patch.engine_seed_ratio).toBe('2.5');
  });

  it('saves edited values', async () => {
    const onsave = mountCard();
    const down = input('engine-max-down-kbps');
    down.value = '4096';
    down.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    save();
    await Promise.resolve();
    expect(onsave.mock.calls[0]![0].engine_max_down_kbps).toBe('4096');
  });
});
