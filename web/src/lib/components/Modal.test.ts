/**
 * The body scroll lock across stacked modals: a confirm dialog opened from
 * inside an editor must not unlock page scroll while the editor remains open.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createRawSnippet, flushSync, mount, unmount } from 'svelte';
import Field from './Field.svelte';
import { FIELD_ACCESSIBILITY_CONTEXT } from './fieldContext';
import Modal from './Modal.svelte';
import TextInput from './TextInput.svelte';

let apps: Record<string, unknown>[] = [];
let hosts: HTMLElement[] = [];

interface MountedComponent {
  app: Record<string, unknown>;
  host: HTMLElement;
}

interface ModalOptions {
  onclose?: () => void;
  dirty?: boolean;
  childHTML?: string;
}

function rawSnippet(html: string) {
  return createRawSnippet(() => ({ render: () => html }));
}

async function settle() {
  await Promise.resolve();
  await Promise.resolve();
  flushSync();
}

function mountModal(title: string, options: ModalOptions = {}): MountedComponent {
  const host = document.createElement('div');
  document.body.appendChild(host);
  hosts.push(host);
  const app = mount(Modal, {
    target: host,
    props: {
      title,
      onclose: options.onclose ?? (() => {}),
      dirty: options.dirty ?? false,
      children: rawSnippet(options.childHTML ?? `<p>${title}</p>`),
    },
  }) as Record<string, unknown>;
  apps.push(app);
  flushSync();
  return { app, host };
}

function mountField(help?: string, error?: string): MountedComponent {
  const host = document.createElement('div');
  document.body.appendChild(host);
  hosts.push(host);
  const app = mount(Field, {
    target: host,
    props: {
      label: 'Name',
      for: 'field-control',
      help,
      error,
      children: rawSnippet('<input id="field-control">'),
    },
  }) as Record<string, unknown>;
  apps.push(app);
  flushSync();
  return { app, host };
}

function mountTextInput(context: { describedBy: string | undefined; invalid: boolean }): MountedComponent {
  const host = document.createElement('div');
  document.body.appendChild(host);
  hosts.push(host);
  const app = mount(TextInput, {
    target: host,
    context: new Map([[FIELD_ACCESSIBILITY_CONTEXT, context]]),
    props: { value: '' },
  }) as Record<string, unknown>;
  apps.push(app);
  flushSync();
  return { app, host };
}

function dialog(host: HTMLElement): HTMLElement {
  const element = host.querySelector<HTMLElement>('[role="dialog"]');
  if (!element) throw new Error('Expected a dialog');
  return element;
}

function button(host: HTMLElement, label: string): HTMLButtonElement {
  const element = Array.from(host.querySelectorAll('button')).find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!element) throw new Error(`Expected button "${label}"`);
  return element;
}

function unmountApp(app: Record<string, unknown>) {
  unmount(app);
  apps = apps.filter((candidate) => candidate !== app);
  flushSync();
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
    const { app } = mountModal('Editor');
    expect(document.body.style.overflow).toBe('hidden');
    unmountApp(app);
    expect(document.body.style.overflow).toBe('');
  });

  it('keeps the lock when a stacked confirm closes over a still-open editor', () => {
    mountModal('Editor');
    const confirm = mountModal('Remove?');
    expect(document.body.style.overflow).toBe('hidden');

    unmountApp(confirm.app);
    expect(document.body.style.overflow).toBe('hidden');
  });
});

describe('Modal close protection', () => {
  it.each([
    ['Escape', (host: HTMLElement) => dialog(host).dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Escape' }))],
    ['backdrop', (host: HTMLElement) => host.querySelector<HTMLElement>('[data-modal-backdrop]')?.click()],
    ['header close', (host: HTMLElement) => host.querySelector<HTMLButtonElement>('button[aria-label="Close"]')?.click()],
  ])('confirms dirty close attempts from %s', async (_reason, attemptClose) => {
    const onclose = vi.fn();
    const { host } = mountModal('Edit server', { dirty: true, onclose });

    attemptClose(host);
    await settle();

    expect(onclose).not.toHaveBeenCalled();
    expect(host.querySelector('[data-modal-discard-confirmation]')).not.toBeNull();
    expect(button(host, 'Keep editing')).toBe(document.activeElement);

    button(host, 'Discard changes').click();
    expect(onclose).toHaveBeenCalledOnce();
  });

  it('keeps a dirty draft open when editing continues', async () => {
    const onclose = vi.fn();
    const { host } = mountModal('Edit server', {
      dirty: true,
      onclose,
      childHTML: '<button type="button">Original control</button>',
    });

    host.querySelector<HTMLButtonElement>('button[aria-label="Close"]')?.click();
    await settle();
    button(host, 'Keep editing').click();
    await settle();

    expect(onclose).not.toHaveBeenCalled();
    expect(host.querySelector('[data-modal-discard-confirmation]')).toBeNull();
    expect(button(host, 'Original control')).toBeInstanceOf(HTMLButtonElement);
    expect(document.activeElement).toBe(dialog(host));
  });

  it('closes a clean modal immediately', () => {
    const onclose = vi.fn();
    const { host } = mountModal('Edit server', { onclose });

    host.querySelector<HTMLButtonElement>('button[aria-label="Close"]')?.click();

    expect(onclose).toHaveBeenCalledOnce();
    expect(host.querySelector('[data-modal-discard-confirmation]')).toBeNull();
  });
});

describe('Modal focus management', () => {
  it('keeps Tab and Shift+Tab within the modal at focusable boundaries and leaves the backdrop unfocusable', () => {
    const { host } = mountModal('Editor', {
      childHTML: `
        <div>
          <button type="button" disabled>Disabled</button>
          <button type="button" tabindex="-1">Skipped</button>
          <button type="button">First</button>
          <button type="button" hidden>Hidden</button>
          <button type="button">Last</button>
        </div>
      `,
    });
    const close = host.querySelector<HTMLButtonElement>('button[aria-label="Close"]');
    const first = button(host, 'First');
    const last = button(host, 'Last');
    const backdrop = host.querySelector<HTMLElement>('[data-modal-backdrop]');

    expect(close).not.toBeNull();
    expect(backdrop?.tagName).toBe('DIV');
    expect(backdrop?.tabIndex).toBe(-1);

    last.focus();
    const forwardBoundary = new KeyboardEvent('keydown', {
      bubbles: true,
      cancelable: true,
      key: 'Tab',
    });
    last.dispatchEvent(forwardBoundary);
    expect(forwardBoundary.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(close);

    const backwardBoundary = new KeyboardEvent('keydown', {
      bubbles: true,
      cancelable: true,
      key: 'Tab',
      shiftKey: true,
    });
    close?.dispatchEvent(backwardBoundary);
    expect(backwardBoundary.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(last);

    first.focus();
    const interiorTab = new KeyboardEvent('keydown', {
      bubbles: true,
      cancelable: true,
      key: 'Tab',
    });
    first.dispatchEvent(interiorTab);
    expect(interiorTab.defaultPrevented).toBe(false);
    expect(document.activeElement).toBe(first);
  });

  it('honors an initial autofocus target', () => {
    const { host } = mountModal('Editor', {
      childHTML: '<input autofocus aria-label="Editor name">',
    });

    expect(document.activeElement).toBe(host.querySelector('input'));
  });

  it('restores focus to the opener after closing', () => {
    const opener = document.createElement('button');
    opener.textContent = 'Open editor';
    document.body.appendChild(opener);
    opener.focus();

    const { app } = mountModal('Editor');
    expect(document.activeElement).toBe(dialog(hosts.at(-1)!));

    unmountApp(app);
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it('returns focus to an editor control after its nested delete confirmation closes', () => {
    const outer = mountModal('Edit server', {
      childHTML: '<button type="button">Delete server</button>',
    });
    const deleteButton = button(outer.host, 'Delete server');
    deleteButton.focus();

    const nested = mountModal('Delete server?');
    expect(document.activeElement).toBe(dialog(nested.host));

    unmountApp(nested.app);
    expect(document.activeElement).toBe(deleteButton);
  });
});

describe('Field text input accessibility', () => {
  it('uses a stable help id as the text input description', () => {
    const field = mountField('Shown to other users');
    const help = field.host.querySelector<HTMLParagraphElement>('p');
    if (!help) throw new Error('Expected help text');

    const input = mountTextInput({ describedBy: help.id, invalid: false })
      .host.querySelector<HTMLInputElement>('input');
    expect(help.id).not.toBe('');
    expect(input?.getAttribute('aria-describedby')).toBe(help.id);
    expect(input?.getAttribute('aria-invalid')).toBeNull();
  });

  it('uses a stable error id and marks the text input invalid', () => {
    const field = mountField(undefined, 'Name is required');
    const error = field.host.querySelector<HTMLParagraphElement>('p');
    if (!error) throw new Error('Expected error text');

    const input = mountTextInput({ describedBy: error.id, invalid: true })
      .host.querySelector<HTMLInputElement>('input');
    expect(error.id).not.toBe('');
    expect(input?.getAttribute('aria-describedby')).toBe(error.id);
    expect(input?.getAttribute('aria-invalid')).toBe('true');
  });
});
