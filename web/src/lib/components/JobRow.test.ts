/**
 * The Jobs feed row: what the summary line says, and what the details panel
 * shows for each job state.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { Job } from '../api/types';
import JobRow from './JobRow.svelte';

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function job(over: Partial<Job> = {}): Job {
  return {
    id: 42,
    kind: 'search_movie',
    payload: '{}',
    state: 'done',
    attempts: 0,
    run_after: '',
    lease_expires_at: '',
    last_error: '',
    created_at: '2026-08-01T10:00:00Z',
    updated_at: '2026-08-01T10:01:00Z',
    ...over,
  };
}

function render(props: { job: Job; expanded?: boolean; ontoggle?: () => void }) {
  host = document.createElement('ul');
  document.body.appendChild(host);
  app = mount(JobRow, {
    target: host,
    props: { expanded: false, ontoggle: () => {}, ...props },
  }) as Record<string, unknown>;
  flushSync();
  return host;
}

function panel(root: HTMLElement): HTMLElement | null {
  const button = root.querySelector<HTMLButtonElement>('button[aria-expanded]');
  const id = button?.getAttribute('aria-controls');
  return id ? document.getElementById(id) : null;
}

afterEach(() => {
  if (app) unmount(app);
  app = null;
  host?.remove();
  host = null;
});

describe('JobRow', () => {
  it('keeps tries out of the summary until one has failed, and always lists them in the panel', () => {
    const clean = render({ job: job({ attempts: 0 }), expanded: true });
    expect(clean.querySelector('button')?.textContent).not.toContain('tries failed');
    expect(panel(clean)?.textContent).toContain('Failed tries');
    expect(panel(clean)?.textContent).toContain('0 of 5');
    if (app) unmount(app);
    app = null;
    clean.remove();

    const retried = render({ job: job({ state: 'pending', attempts: 2, run_after: '2026-08-01T10:30:00Z' }) });
    expect(retried.querySelector('button')?.textContent).toContain('2 of 5 tries failed');
  });

  it('shows the next try for a pending job and the lease for a running one', () => {
    const pending = render({
      job: job({ state: 'pending', attempts: 1, run_after: '2026-08-01T10:30:00Z' }),
      expanded: true,
    });
    expect(panel(pending)?.textContent).toContain('Next try');
    expect(panel(pending)?.textContent).not.toContain('Lease expires');
    if (app) unmount(app);
    app = null;
    pending.remove();

    const running = render({
      job: job({ state: 'running', lease_expires_at: '2026-08-01T10:05:00Z' }),
      expanded: true,
    });
    expect(panel(running)?.textContent).toContain('Lease expires');
    expect(panel(running)?.textContent).not.toContain('Next try');
  });

  it('names the subject in the summary and links it in the panel', () => {
    const root = render({
      job: job({ subject: 'Arrival', subject_kind: 'movie', subject_id: 7, payload: '{"movie_id":7}' }),
      expanded: true,
    });
    expect(root.querySelector('button')?.textContent).toContain('Arrival');
    const link = panel(root)?.querySelector('a');
    expect(link?.getAttribute('href')).toBe('/movies/7');
    expect(link?.textContent).toBe('Arrival');
    expect(panel(root)?.textContent).toContain('movie id:');
  });

  it('shows the failure inline when collapsed and in the panel when open', () => {
    const collapsed = render({ job: job({ state: 'failed', attempts: 5, last_error: 'Indexer timed out' }) });
    expect(collapsed.textContent).toContain('Indexer timed out');
    expect(panel(collapsed)).toBeNull();
    if (app) unmount(app);
    app = null;
    collapsed.remove();

    const open = render({ job: job({ state: 'failed', attempts: 5, last_error: 'Indexer timed out' }), expanded: true });
    expect(panel(open)?.textContent).toContain('Last error');
    expect(panel(open)?.textContent).toContain('Indexer timed out');
    expect(open.querySelectorAll('p').length).toBe(0);
  });

  it('renders nothing for arguments that are not a flat object', () => {
    const root = render({ job: job({ payload: 'not json' }), expanded: true });
    expect(panel(root)?.textContent).not.toContain('Arguments');
  });

  it('asks the parent to toggle when the summary is clicked', () => {
    const ontoggle = vi.fn();
    const root = render({ job: job(), ontoggle });
    root.querySelector<HTMLButtonElement>('button[aria-expanded]')?.click();
    expect(ontoggle).toHaveBeenCalledTimes(1);
  });
});
