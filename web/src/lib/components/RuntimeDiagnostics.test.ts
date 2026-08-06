import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mount, unmount } from 'svelte';
import RuntimeDiagnostics from './RuntimeDiagnostics.svelte';

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(RuntimeDiagnostics, {
    target: host,
    props: {
      diagnostics: {
        started_at: '2026-08-05T12:00:00Z',
        uptime_seconds: 93784,
        go_version: 'go1.26.0',
        os: 'darwin',
        arch: 'arm64',
        config_dir: '/etc/caravan',
        config_file: '/etc/caravan/caravan.yaml',
        database_path: '/var/lib/caravan/caravan.db',
        database_size_bytes: 1048576,
        log_level: 'info',
        listen_address: '127.0.0.1:8677',
        goroutines: 18,
        memory_alloc_bytes: 2097152,
      },
    },
  });
});

afterEach(() => {
  unmount(app);
  host.remove();
});

describe('Runtime diagnostics', () => {
  it('shows process, path, and resource evidence', () => {
    expect(host.textContent).toContain('Up 1d 2h 3m');
    expect(host.textContent).toContain('go1.26.0');
    expect(host.textContent).toContain('darwin/arm64');
    expect(host.textContent).toContain('/etc/caravan/caravan.yaml');
    expect(host.textContent).toContain('/var/lib/caravan/caravan.db');
    expect(host.textContent).toContain('18 goroutines');
    expect(host.textContent).toContain('2 MB allocated');
  });
});
