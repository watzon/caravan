/**
 * The system store's credential readers.
 *
 * The part worth proving is that the per-provider map and the three flat TMDB
 * fields are never two different answers. `metadataCredential` is read by every
 * surface that predates the map, so it has to give the same verdict whether the
 * server sent a map or not — and `credential('tmdb')` has to agree with it,
 * because they are two names for one fact.
 */
import { afterEach, describe, expect, it } from 'vitest';
import type { SystemStatus } from '../api/types';
import { system } from './system.svelte';

/** A status carrying only the credential fields under test. */
function seed(fields: Partial<SystemStatus>): void {
  system.status = fields as SystemStatus;
}

afterEach(() => {
  system.status = null;
});

describe('system credentials', () => {
  it('reads TMDB the same way with a map and without one', () => {
    seed({
      metadata_credential: 'invalid',
      metadata_credential_reason: 'tmdb: http 401: Invalid API key',
      metadata_credential_checked_at: '2026-08-06T00:00:00Z',
    });
    const flat = { ...system.credential('tmdb') };
    expect(system.metadataCredential).toBe('invalid');
    expect(system.metadataCredentialReason).toBe('tmdb: http 401: Invalid API key');
    expect(flat.state).toBe(system.metadataCredential);
    expect(flat.reason).toBe(system.metadataCredentialReason);

    // The server lifts the flat fields out of the map, so a payload carrying
    // both must read identically through either door.
    seed({
      metadata_credential: 'invalid',
      metadata_credential_reason: 'tmdb: http 401: Invalid API key',
      metadata_credential_checked_at: '2026-08-06T00:00:00Z',
      metadata_credentials: {
        tmdb: {
          state: 'invalid',
          reason: 'tmdb: http 401: Invalid API key',
          checked_at: '2026-08-06T00:00:00Z',
        },
      },
    });
    expect(system.credential('tmdb')).toEqual(flat);
    expect(system.metadataCredential).toBe('invalid');
    expect(system.metadataCredentialReason).toBe('tmdb: http 401: Invalid API key');
  });

  // A member never fetches the status at all, and warning about a credential
  // nobody has looked at would be noise.
  it('reads an unfetched status as ok with nothing to say', () => {
    expect(system.metadataCredential).toBe('ok');
    expect(system.metadataCredentialReason).toBe('');
    expect(system.credential('thetvdb')).toEqual({ state: 'ok', reason: '', checkedAt: '' });
    expect(system.unhealthyCredentials).toEqual([]);
  });

  it('lists every credential that needs attention, in a stable order', () => {
    seed({
      metadata_credential: 'invalid',
      metadata_credential_reason: 'tmdb: http 401',
      metadata_credentials: {
        tmdb: { state: 'invalid', reason: 'tmdb: http 401' },
        thetvdb: { state: 'absent' },
        tvmaze: { state: 'ok' },
      },
    });

    expect(system.unhealthyCredentials).toEqual([
      { id: 'thetvdb', state: 'absent', reason: '', checkedAt: '' },
      { id: 'tmdb', state: 'invalid', reason: 'tmdb: http 401', checkedAt: '' },
    ]);
  });

  it('says nothing while every credential works', () => {
    seed({ metadata_credentials: { tmdb: { state: 'ok' }, thetvdb: { state: 'ok' } } });
    expect(system.unhealthyCredentials).toEqual([]);
  });

  // An older server sends no map, and TMDB's flat verdict is the only one it
  // has an opinion about.
  it('warns about TMDB from the flat fields when there is no map', () => {
    seed({ metadata_credential: 'absent' });
    expect(system.unhealthyCredentials).toEqual([
      { id: 'tmdb', state: 'absent', reason: '', checkedAt: '' },
    ]);
  });
});
