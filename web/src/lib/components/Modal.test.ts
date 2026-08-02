/**
 * The body scroll lock across stacked modals: a confirm dialog opened from
 * inside an editor must not unlock page scroll when it closes while the
 * editor underneath is still open.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Modal from './Modal.svelte';
import { createRawSnippet } from 'svelte';

let apps: Record<string, unknown>[] = [];
let hosts: HTMLElement[] = [];

function mountModal(title: string): Record<string, unknown> {
  const host = document.createElement('div');
  document.body.appendChild(host);
  hosts.push(host);
  const app = mount(Modal, {
    target: host,
    props: {
      title,
      onclose: () => {},
      children: createRawSnippet(() => ({ render: () => `<p>${title}</p>` })),
    },
  }) as Record<string, unknown>;
  apps.push(app);
  flushSync();
  return app;
}

afterEach(() => {
  for (const app of apps) unmount(app);
  for (const host of hosts) host.remove();
  apps = [];
  hosts = [];
  document.body.style.overflow = '';
});

describe('Modal scroll lock', () => {
  it('locks page scroll while open and unlocks on close', () => {
    const app = mountModal('Editor');
    expect(document.body.style.overflow).toBe('hidden');
    unmount(app);
    apps = apps.filter((a) => a !== app);
    flushSync();
    expect(document.body.style.overflow).toBe('');
  });

  it('keeps the lock when a stacked confirm closes over a still-open editor', () => {
    mountModal('Editor');
    const confirm = mountModal('Remove?');
    expect(document.body.style.overflow).toBe('hidden');

    unmount(confirm);
    apps = apps.filter((a) => a !== confirm);
    flushSync();
    // The editor underneath is still open; scroll stays locked.
    expect(document.body.style.overflow).toBe('hidden');
  });
});
