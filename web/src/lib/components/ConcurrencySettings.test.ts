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

const STORED: Settings = {
  max_concurrent_downloads: '3',
  embedded_torrent_max_concurrent: '2',
  embedded_usenet_max_concurrent: '1',
};

function mountCard(settings: Settings = STORED, onsave = vi.fn(async (_patch: Settings) => true)) {
  app = mount(ConcurrencySettings, { target: host, props: { settings, onsave } });
  flushSync();
  return onsave;
}

async function settle() {
  await Promise.resolve();
  await Promise.resolve();
  flushSync();
}

function input(id: string): HTMLInputElement {
  const el = host.querySelector<HTMLInputElement>(`#${id}`);
  expect(el, `#${id}`).not.toBeNull();
  return el!;
}

function saveButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.match(/Save changes|No changes|Fix errors/),
  );
  expect(button, 'the save button').toBeDefined();
  return button!;
}

function setInput(id: string, value: string) {
  const el = input(id);
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function setTextInput(id: string, value: string) {
  const el = input(id);
  el.type = 'text';
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

describe('ConcurrencySettings', () => {
  it('shows stored caps, keeps overall concurrency visible, and does not save unchanged settings', () => {
    mountCard();

    expect(input('max-concurrent-downloads').value).toBe('3');
    expect(input('embedded-torrent-max-concurrent').value).toBe('2');
    expect(input('embedded-usenet-max-concurrent').value).toBe('1');
    expect(input('max-concurrent-downloads').closest('[data-settings-advanced]')).toBeNull();
    expect(host.querySelectorAll('[data-settings-advanced]')).toHaveLength(1);
    expect(saveButton().disabled).toBe(true);
    expect(saveButton().textContent).toContain('No changes');
  });

  it('normalizes a changed count and saves zero as unlimited', async () => {
    const onsave = mountCard();

    setInput('max-concurrent-downloads', '0000');
    setInput('embedded-torrent-max-concurrent', '0004');
    expect(saveButton().disabled).toBe(false);

    saveButton().click();
    await settle();

    expect(onsave).toHaveBeenCalledWith({
      max_concurrent_downloads: '0',
      embedded_torrent_max_concurrent: '4',
      embedded_usenet_max_concurrent: '1',
    });
    expect(saveButton().disabled).toBe(true);
    expect(saveButton().textContent).toContain('No changes');
  });

  it('defaults every cap to the zero boundary', () => {
    mountCard({});

    expect(input('max-concurrent-downloads').value).toBe('0');
    expect(input('embedded-torrent-max-concurrent').value).toBe('0');
    expect(input('embedded-usenet-max-concurrent').value).toBe('0');
    expect(host.textContent).toContain('Zero starts every download right away.');
  });

  it('blocks invalid text and fractions before they reach the API', () => {
    const onsave = mountCard();

    setTextInput('embedded-usenet-max-concurrent', 'many');

    expect(input('embedded-usenet-max-concurrent').getAttribute('aria-invalid')).toBe('true');
    expect(host.textContent).toContain('Enter a non-negative whole number.');
    expect(saveButton().disabled).toBe(true);
    saveButton().dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(onsave).not.toHaveBeenCalled();

    setInput('embedded-usenet-max-concurrent', '2.5');
    expect(input('embedded-usenet-max-concurrent').getAttribute('aria-invalid')).toBe('true');
    expect(saveButton().disabled).toBe(true);
  });
});
