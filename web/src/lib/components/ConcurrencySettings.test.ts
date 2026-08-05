import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { Settings } from '../api/types';
import ConcurrencySettings from './ConcurrencySettings.svelte';

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

function mountCard(settings: Settings, onsave = vi.fn(async () => true)) {
  app = mount(ConcurrencySettings, { target: host, props: { settings, onsave } });
  flushSync();
  return onsave;
}

function input(id: string): HTMLInputElement {
  const el = host.querySelector(`#${id}`) as HTMLInputElement | null;
  expect(el, `#${id}`).not.toBeNull();
  return el!;
}

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((b) => b.textContent?.includes(label));
  expect(found, `button ${label}`).toBeDefined();
  return found!;
}

describe('ConcurrencySettings', () => {
  it('shows the stored caps', () => {
    mountCard({
      max_concurrent_downloads: '3',
      embedded_torrent_max_concurrent: '2',
      embedded_usenet_max_concurrent: '1',
    });
    expect(input('max-concurrent-downloads').value).toBe('3');
    expect(input('embedded-torrent-max-concurrent').value).toBe('2');
    expect(input('embedded-usenet-max-concurrent').value).toBe('1');
  });

  // Unset is 0, and 0 is unlimited — which is what Caravan did before caps
  // existed, so an install that has never opened this screen must read that way.
  it('defaults every cap to unlimited', () => {
    mountCard({});
    expect(input('max-concurrent-downloads').value).toBe('0');
    expect(input('embedded-torrent-max-concurrent').value).toBe('0');
    expect(input('embedded-usenet-max-concurrent').value).toBe('0');
    expect(host.textContent).toContain('0 is unlimited');
  });

  // The Usenet advice is the whole reason that field is separate from the
  // torrent one: parallel NZBs share one connection pool.
  it('recommends a small usenet cap and says why', () => {
    mountCard({});
    expect(host.textContent).toContain('2 is a good default');
    expect(host.textContent).toContain('share one pool of connections');
  });

  it('saves every cap, blanks written as unlimited', async () => {
    const onsave = mountCard({});
    const global = input('max-concurrent-downloads');
    global.value = '4';
    global.dispatchEvent(new Event('input', { bubbles: true }));
    const usenet = input('embedded-usenet-max-concurrent');
    usenet.value = '';
    usenet.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    button('Save changes').click();
    await Promise.resolve();

    expect(onsave).toHaveBeenCalledWith({
      max_concurrent_downloads: '4',
      embedded_torrent_max_concurrent: '0',
      embedded_usenet_max_concurrent: '0',
    });
  });
});
