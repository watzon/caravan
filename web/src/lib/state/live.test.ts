import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { applyInvalidation, refreshSidebarStores, startLiveUpdates } from './live';

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/system/status')) {
        return new Response(
          JSON.stringify({
            version: '0',
            mode: 'server',
            storage_root: '/data',
            schema_version: 1,
            scanning: false,
            counts: { movies: 1, series: 0, media_files: 0, unmatched: 0 },
            disk_free_bytes: 0,
            disk_total_bytes: 0,
            engine_health: 'ok',
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }
      if (url.includes('/requests')) {
        return new Response(JSON.stringify({ requests: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/downloads')) {
        return new Response(JSON.stringify({ downloads: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/system/tasks')) {
        return new Response(JSON.stringify({ tasks: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/jobs')) {
        return new Response(JSON.stringify({ jobs: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('applyInvalidation', () => {
  it('refreshes the store that owns the resource', () => {
    applyInvalidation('library');
    applyInvalidation('requests');
    applyInvalidation('downloads');
    applyInvalidation('jobs');
    const urls = vi.mocked(fetch).mock.calls.map((call) => String(call[0]));
    expect(urls.some((url) => url.includes('/system/status'))).toBe(true);
    expect(urls.some((url) => url.includes('/requests'))).toBe(true);
    expect(urls.some((url) => url.includes('/downloads'))).toBe(true);
    expect(urls.some((url) => url.includes('/system/tasks'))).toBe(true);
    expect(urls.some((url) => url.includes('/jobs'))).toBe(true);
  });

  it('ignores an unknown resource', () => {
    applyInvalidation('nope');
    expect(fetch).not.toHaveBeenCalled();
  });
});

type Listener = (event: { data: string }) => void;

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  #listeners = new Map<string, Listener[]>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: Listener): void {
    const list = this.#listeners.get(type) ?? [];
    list.push(listener);
    this.#listeners.set(type, list);
  }

  close(): void {
    this.closed = true;
  }

  emit(type: string, data: string): void {
    for (const listener of this.#listeners.get(type) ?? []) {
      listener({ data });
    }
  }
}

describe('startLiveUpdates', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  it('does nothing when EventSource is missing', () => {
    vi.stubGlobal('EventSource', undefined);
    expect(startLiveUpdates()).toBeTypeOf('function');
    expect(FakeEventSource.instances).toHaveLength(0);
  });

  it('opens the stream and refreshes the matching store', () => {
    const stop = startLiveUpdates();
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.instances[0]?.url).toBe('/api/v1/events/stream');

    FakeEventSource.instances[0]?.emit('invalidate', JSON.stringify({ resource: 'requests' }));
    const urls = vi.mocked(fetch).mock.calls.map((call) => String(call[0]));
    expect(urls.some((url) => url.includes('/requests'))).toBe(true);

    stop();
    expect(FakeEventSource.instances[0]?.closed).toBe(true);
  });

  it('refreshes every sidebar store when the tab becomes visible', () => {
    const stop = startLiveUpdates();
    vi.mocked(fetch).mockClear();
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => false });
    document.dispatchEvent(new Event('visibilitychange'));
    const urls = vi.mocked(fetch).mock.calls.map((call) => String(call[0]));
    expect(urls.some((url) => url.includes('/system/status'))).toBe(true);
    expect(urls.some((url) => url.includes('/requests'))).toBe(true);
    expect(urls.some((url) => url.includes('/downloads'))).toBe(true);
    expect(urls.some((url) => url.includes('/jobs'))).toBe(true);
    stop();
  });

  it('refreshes every store after a reconnect', () => {
    const stop = startLiveUpdates();
    FakeEventSource.instances[0]?.onopen?.();
    vi.mocked(fetch).mockClear();
    FakeEventSource.instances[0]?.onopen?.();
    expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThan(0);
    stop();
  });
});

describe('refreshSidebarStores', () => {
  it('re-reads every sidebar snapshot', () => {
    refreshSidebarStores();
    const urls = vi.mocked(fetch).mock.calls.map((call) => String(call[0]));
    expect(urls.some((url) => url.includes('/system/status'))).toBe(true);
    expect(urls.some((url) => url.includes('/requests'))).toBe(true);
    expect(urls.some((url) => url.includes('/downloads'))).toBe(true);
    expect(urls.some((url) => url.includes('/jobs'))).toBe(true);
  });
});
