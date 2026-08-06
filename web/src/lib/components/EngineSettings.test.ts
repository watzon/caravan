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

describe('EngineSettings', () => {
  it('loads normalized stored values and does not save unchanged settings', () => {
    mountCard();

    expect(input('engine-listen-port').value).toBe('51413');
    expect(input('engine-max-down-kbps').value).toBe('2048');
    expect(input('engine-seed-ratio').value).toBe('2.5');
    expect(saveButton().disabled).toBe(true);
    expect(saveButton().textContent).toContain('No changes');
    expect(host.querySelectorAll('[data-settings-advanced]')).toHaveLength(3);
  });

  it('accepts the listen port boundaries and normalizes a changed patch', async () => {
    const onsave = mountCard();

    setInput('engine-listen-port', '65535');
    setInput('engine-max-down-kbps', '0004096');
    setInput('engine-seed-ratio', '02.50');
    expect(saveButton().disabled).toBe(false);

    saveButton().click();
    await settle();

    expect(onsave).toHaveBeenCalledWith({
      engine_listen_port: '65535',
      engine_max_connections: '80',
      engine_max_down_kbps: '4096',
      engine_max_up_kbps: '256',
      engine_seed_ratio: '2.5',
      engine_seed_days: '14',
    });
    expect(saveButton().disabled).toBe(true);
    expect(saveButton().textContent).toContain('No changes');
    expect(host.textContent).toContain('Port and connection changes apply after a restart.');
  });

  it('blocks invalid text and out-of-range ports before they reach the API', () => {
    const onsave = mountCard();

    setTextInput('engine-listen-port', 'not-a-port');

    expect(input('engine-listen-port').getAttribute('aria-invalid')).toBe('true');
    expect(host.textContent).toContain('Enter a whole number from 0 to 65,535.');
    expect(saveButton().disabled).toBe(true);
    saveButton().dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(onsave).not.toHaveBeenCalled();

    setInput('engine-listen-port', '65536');
    expect(input('engine-listen-port').getAttribute('aria-invalid')).toBe('true');
    expect(saveButton().disabled).toBe(true);
  });

  it('saves cleared settings as zero', async () => {
    const onsave = mountCard();

    setInput('engine-listen-port', '');
    setInput('engine-max-connections', '');
    setInput('engine-max-down-kbps', '');
    setInput('engine-max-up-kbps', '');
    setInput('engine-seed-ratio', '');
    setInput('engine-seed-days', '');
    expect(saveButton().disabled).toBe(false);

    saveButton().click();
    await Promise.resolve();
    flushSync();

    expect(onsave).toHaveBeenCalledWith({
      engine_listen_port: '0',
      engine_max_connections: '0',
      engine_max_down_kbps: '0',
      engine_max_up_kbps: '0',
      engine_seed_ratio: '0',
      engine_seed_days: '0',
    });
  });
});
