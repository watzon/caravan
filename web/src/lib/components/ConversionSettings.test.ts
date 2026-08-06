import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { Settings } from '../api/types';
import ConversionSettings from './ConversionSettings.svelte';

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
  app = mount(ConversionSettings, { target: host, props: { settings, onsave } });
  flushSync();
  return onsave;
}

function input(id: string): HTMLInputElement {
  const found = host.querySelector<HTMLInputElement>(`#${id}`);
  expect(found, `#${id}`).not.toBeNull();
  return found!;
}

function saveButton(): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((button) =>
    button.textContent?.includes('Save changes'),
  );
  expect(found, 'Save changes button').toBeDefined();
  return found!;
}

describe('ConversionSettings', () => {
  it('shows the established ffmpeg defaults and scope', () => {
    mountCard({});

    expect(host.querySelector<HTMLSelectElement>('#convert-video-preset')?.value).toBe('veryfast');
    expect(input('convert-video-crf').value).toBe('20');
    expect(input('convert-audio-bitrate-kbps').value).toBe('192');
    expect(host.textContent).toContain('Container-only conversions copy streams');
    expect(host.textContent).toContain('running conversion keeps the settings it started with');
  });

  it('validates and saves one normalized settings patch', async () => {
    const onsave = mountCard({
      convert_video_preset: 'fast',
      convert_video_crf: '19',
      convert_audio_bitrate_kbps: '224',
    });
    const crf = input('convert-video-crf');
    crf.value = '52';
    crf.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    expect(saveButton().disabled).toBe(true);
    expect(host.textContent).toContain('Enter a whole number from 0 to 51.');

    const preset = host.querySelector<HTMLSelectElement>('#convert-video-preset')!;
    preset.value = 'slow';
    preset.dispatchEvent(new Event('change', { bubbles: true }));
    crf.value = '18';
    crf.dispatchEvent(new Event('input', { bubbles: true }));
    const audio = input('convert-audio-bitrate-kbps');
    audio.value = '256';
    audio.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    saveButton().click();
    await Promise.resolve();

    expect(onsave).toHaveBeenCalledWith({
      convert_video_preset: 'slow',
      convert_video_crf: '18',
      convert_audio_bitrate_kbps: '256',
    });
  });
});
