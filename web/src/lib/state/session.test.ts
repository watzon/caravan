/**
 * The session store: what GET /auth/me is turned into, and — the part worth
 * proving — what an *unanswered* /auth/me means.
 *
 * `isAdmin` is what every role-gated conditional in the SPA reads, so its
 * behaviour on a null user is a decision rather than an accident: unknown reads
 * as admin, because the alternative bounces a real admin off /settings on every
 * boot before the fetch lands.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { session } from './session.svelte';

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  session.forget();
  session.loading = true;
  vi.unstubAllGlobals();
});

describe('session', () => {
  it('reads as an admin before /auth/me has answered', () => {
    expect(session.user).toBeNull();
    expect(session.isAdmin).toBe(true);
    expect(session.open).toBe(false);
    expect(session.username).toBe('');
  });

  it('takes the role from /auth/me', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ username: 'ada', role: 'member', open: false })),
    );

    await session.refresh();

    expect(session.user).toEqual({ username: 'ada', role: 'member', open: false });
    expect(session.username).toBe('ada');
    expect(session.role).toBe('member');
    expect(session.isAdmin).toBe(false);
    expect(session.loading).toBe(false);
  });

  it('reports an open server as an anonymous admin', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ username: '', role: 'admin', open: true })));

    await session.refresh();

    expect(session.open).toBe(true);
    expect(session.isAdmin).toBe(true);
    // Nobody to sign out: the sidebar reads this to decide whether to offer it.
    expect(session.username).toBe('');
  });

  /**
   * A blip is not evidence that somebody's role changed. Downgrading the UI on
   * one failed fetch would yank screens out from under an admin mid-session.
   */
  it('keeps the last known identity when a refresh fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ username: 'ada', role: 'admin', open: false })));
    await session.refresh();

    vi.stubGlobal('fetch', vi.fn(async () => json({ error: 'boom' }, 500)));
    await session.refresh();

    expect(session.username).toBe('ada');
    expect(session.isAdmin).toBe(true);
    expect(session.error).not.toBeNull();
  });

  it('forgets who was signed in on logout', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ username: 'ada', role: 'member', open: false })));
    await session.refresh();

    session.forget();

    expect(session.user).toBeNull();
    expect(session.error).toBeNull();
  });
});
