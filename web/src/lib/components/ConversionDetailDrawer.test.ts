import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ConversionDetailDrawer from './ConversionDetailDrawer.svelte';
import type { Conversion } from '../api/types';

const RUNNING: Conversion = {
  id: 7,
  media_file_id: 42,
  source_path: 'library/Movies/Contact (1997)/Contact (1997).mkv',
  output_path: 'library/Movies/Contact (1997)/.Contact (1997).caravan-convert.mp4',
  strategy: 'transcode',
  profile_id: 'safe',
  status: 'running',
  error: '',
  created_at: '2026-08-05T11:59:30Z',
  updated_at: '2026-08-05T12:00:00Z',
  stage: 'converting',
  started_at: '2026-08-05T12:00:00Z',
  progress: 0.5,
  processed_seconds: 60,
  duration_seconds: 120,
  speed: 1.5,
  eta_seconds: 40,
};

let host: HTMLElement;
let app: Record<string, unknown> | undefined;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-08-05T12:02:00Z'));
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  host.remove();
  document.querySelector('[data-conversion-focus-main]')?.remove();
  vi.useRealTimers();
});

function render(
  conversion: Conversion,
  props: Record<string, unknown> = {},
) {
  app = mount(ConversionDetailDrawer, {
    target: host,
    props: { conversion, onclose: vi.fn(), ...props },
  });
  flushSync();
}

function buttonWith(text: string): HTMLButtonElement | undefined {
  return [...host.querySelectorAll('button')].find((button) =>
    button.textContent?.includes(text),
  );
}

describe('ConversionDetailDrawer', () => {
  it('shows live ffmpeg progress, timing, and process details', () => {
    render(RUNNING);

    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    expect(host.textContent).toContain('Contact (1997).mkv');
    expect(host.textContent).toContain('Encoding media');
    expect(host.textContent).toContain('Transcode (re-encode)');
    expect(host.textContent).toContain('50%');
    expect(host.textContent).toContain('1m / 2m');
    expect(host.textContent).toContain('1.5x');
    expect(host.textContent).toContain('Elapsed');
    expect(host.textContent).toContain('2m');
    expect(host.textContent).toContain('Remaining');
    expect(host.textContent).toContain('40s');

    const dialog = host.querySelector<HTMLElement>('[role="dialog"]')!;
    const headingID = dialog.getAttribute('aria-labelledby');
    const heading = headingID ? host.querySelector<HTMLElement>(`#${headingID}`) : null;
    const sourcePath = host.querySelector<HTMLElement>(`p[title="${RUNNING.source_path}"]`);
    const progress = host.querySelector<HTMLElement>('[role="progressbar"]');
    const iconClose = host.querySelector<HTMLButtonElement>('button[title="Close conversion details"]');

    expect(heading?.textContent?.trim()).toBe('Contact (1997).mkv');
    expect(heading?.title).toBe('Contact (1997).mkv');
    expect(sourcePath?.title).toBe(RUNNING.source_path);
    expect(progress?.getAttribute('aria-label')).toBe('Contact (1997).mkv conversion progress');
    expect(progress?.getAttribute('aria-valuetext')).toBe('50%');
    expect(iconClose?.textContent?.trim()).toBe('Close');
  });

  it('presents stream-copy conversion without internal vocabulary', () => {
    render({ ...RUNNING, strategy: 'remux' });

    expect(host.textContent).toContain('Convert (stream copy)');
    expect(host.textContent).not.toMatch(/remux/i);
  });

  it('keeps queued cancellation in the drawer', () => {
    const oncancel = vi.fn();
    const onclose = vi.fn();
    render({
      ...RUNNING,
      status: 'queued',
      strategy: '',
      stage: undefined,
      started_at: undefined,
      progress: undefined,
      processed_seconds: undefined,
      duration_seconds: undefined,
      speed: undefined,
      eta_seconds: undefined,
    }, { oncancel, onclose });

    buttonWith('Cancel')!.click();
    expect(oncancel).toHaveBeenCalledOnce();

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(onclose).toHaveBeenCalledOnce();
  });

  it('contains sequential keyboard focus', () => {
    render(RUNNING);
    const dialog = host.querySelector<HTMLElement>('[role="dialog"]')!;
    const buttons = [...dialog.querySelectorAll<HTMLButtonElement>('button:not([disabled])')];
    const backdrop = host.querySelector<HTMLButtonElement>(
      'button[aria-label="Close conversion details"]',
    )!;

    expect(document.activeElement).toBe(dialog);
    expect(backdrop.tabIndex).toBe(-1);

    window.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Tab',
      shiftKey: true,
    }));
    expect(document.activeElement).toBe(buttons.at(-1));

    buttons.at(-1)!.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab' }));
    expect(document.activeElement).toBe(buttons[0]);
  });

  it('returns focus to the selected PageTabs button when the opener is gone', async () => {
    const main = document.createElement('main');
    main.dataset.conversionFocusMain = '';
    main.innerHTML = `
      <button type="button">Unrelated action</button>
      <div role="group" aria-label="Conversion work">
        <button type="button" aria-pressed="false">Needs conversion</button>
        <button type="button" aria-pressed="true">Active</button>
      </div>
    `;
    document.body.appendChild(main);
    const selected = main.querySelector<HTMLButtonElement>('button[aria-pressed="true"]')!;
    const opener = document.createElement('button');
    document.body.appendChild(opener);
    opener.focus();

    render(RUNNING);
    opener.remove();
    const mounted = app;
    if (!mounted) throw new Error('Expected mounted drawer');
    app = undefined;
    await unmount(mounted);
    await Promise.resolve();

    expect(document.activeElement).toBe(selected);
  });

  it('does not invent zero progress before ffmpeg reports media time', () => {
    render({
      ...RUNNING,
      progress: undefined,
      processed_seconds: undefined,
      speed: undefined,
      eta_seconds: undefined,
    });

    expect(host.textContent).toContain('Encoding media');
    expect(host.textContent).toContain('Elapsed');
    expect(host.querySelector('[role="progressbar"]')).toBeNull();
    expect(host.textContent).not.toContain('0%');
  });

  it('shows terminal errors and retry in context', () => {
    const onretry = vi.fn();
    render({
      ...RUNNING,
      status: 'failed',
      stage: undefined,
      started_at: undefined,
      progress: undefined,
      processed_seconds: undefined,
      duration_seconds: undefined,
      speed: undefined,
      eta_seconds: undefined,
      error: 'ffmpeg: Invalid data found when processing input',
    }, { onretry });

    expect(host.textContent).toContain('Failed');
    expect(host.textContent).toContain('ffmpeg: Invalid data found when processing input');
    expect(host.querySelector('[role="progressbar"]')).toBeNull();
    buttonWith('Retry')!.click();
    expect(onretry).toHaveBeenCalledOnce();
  });
});
