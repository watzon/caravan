/**
 * The credential model (PLAN phase 10 tasks 2-5).
 *
 * These are the rules every guarded screen branches on, so they are tested
 * once here rather than re-asserted per component: which failures are a
 * credential problem, which are not, and what each one is called.
 */
import { describe, expect, it } from 'vitest';
import { ApiError } from './api/client';
import {
  adultFault,
  metadataCopy,
  metadataFault,
  metadataStateLabel,
  metadataStateOf,
  metadataToast,
} from './credentials';

/** An error shaped exactly as the API layer builds one from a JSON envelope. */
function apiError(status: number, body: unknown): ApiError {
  const message =
    body && typeof body === 'object' && typeof (body as { error?: unknown }).error === 'string'
      ? (body as { error: string }).error
      : 'failed';
  return new ApiError(message, status, body);
}

describe('metadataFault', () => {
  it('reads the absent code', () => {
    expect(
      metadataFault(
        apiError(503, { error: 'no metadata provider configured', code: 'metadata_credential_absent' }),
      ),
    ).toBe('absent');
  });

  it('reads the invalid code', () => {
    expect(
      metadataFault(
        apiError(503, { error: 'the TMDB API key was rejected', code: 'metadata_credential_invalid' }),
      ),
    ).toBe('invalid');
  });

  // The whole point of the code: an unreachable provider is not a wrong key,
  // and telling the user to go fix their key would be a lie.
  it('is silent about a provider that is simply unhappy', () => {
    expect(metadataFault(apiError(502, { error: 'tmdb: http 500' }))).toBeNull();
  });

  // The adult module's 503 must not be read as a TMDB problem: it has its own
  // credential, its own screen and its own fix.
  it('does not claim another module’s credential code', () => {
    expect(
      metadataFault(apiError(503, { error: 'no adult provider', code: 'adult_credential_absent' })),
    ).toBeNull();
  });

  // Back-compat: every metadata route answered a bare 503 before the code
  // existed, and it always meant "no key".
  it('reads an uncoded 503 as a missing key', () => {
    expect(metadataFault(apiError(503, { error: 'no metadata provider configured' }))).toBe(
      'absent',
    );
  });

  it('ignores things that are not API errors at all', () => {
    expect(metadataFault(new Error('boom'))).toBeNull();
    expect(metadataFault(null)).toBeNull();
  });
});

describe('adultFault', () => {
  it('tells a missing credential from a refused one', () => {
    expect(adultFault(apiError(400, { error: 'x', code: 'adult_credential_absent' }))).toBe(
      'absent',
    );
    expect(adultFault(apiError(502, { error: 'x', code: 'adult_credential_invalid' }))).toBe(
      'invalid',
    );
  });

  // A store write that failed mid-enable is not a credential problem, and the
  // modal must not send the user back to re-type a key that was fine.
  it('is silent about anything else', () => {
    expect(adultFault(apiError(500, { error: 'write settings' }))).toBeNull();
  });
});

describe('metadataStateOf', () => {
  it('reads the two states that need attention', () => {
    expect(metadataStateOf('absent')).toBe('absent');
    expect(metadataStateOf('invalid')).toBe('invalid');
  });

  // A server that does not report the field, or a fixture that predates it,
  // must read as fine rather than raising a warning nobody can act on.
  it('treats anything else — including nothing — as ok', () => {
    expect(metadataStateOf('ok')).toBe('ok');
    expect(metadataStateOf(undefined)).toBe('ok');
    expect(metadataStateOf('')).toBe('ok');
  });
});

describe('metadataStateLabel', () => {
  it('stays quiet while the key works', () => {
    expect(metadataStateLabel('ok')).toBeNull();
  });

  it('names each state that needs attention', () => {
    expect(metadataStateLabel('absent')).toBe('No TMDB key');
    expect(metadataStateLabel('invalid')).toBe('TMDB key rejected');
  });
});

describe('metadataCopy', () => {
  // Both faults must name the destination: a screen that cannot work is only
  // useful if it says what would fix it.
  it('sends both faults to the same place, with different verbs', () => {
    const absent = metadataCopy('absent');
    const invalid = metadataCopy('invalid');

    expect(absent.message).toContain('Settings → Metadata');
    expect(invalid.message).toContain('Settings → Metadata');
    expect(absent.title).not.toBe(invalid.title);
  });

  // Settings is admin-only, so the destination in the default copy is a door a
  // member cannot open. They get the same two facts without a dead link.
  it('names a person instead of a screen for a reader who cannot open it', () => {
    for (const fault of ['absent', 'invalid'] as const) {
      const copy = metadataCopy(fault, false);
      expect(copy.message).toContain('Ask a Caravan admin');
      expect(copy.message).not.toContain('Settings → Metadata');
      expect(copy.title).toBe(metadataCopy(fault).title);
    }
  });
});

describe('metadataToast', () => {
  // The few surfaces whose only affordance is a toast still have to name the
  // destination rather than repeat the provider's complaint.
  it('names the fix for a credential failure', () => {
    expect(
      metadataToast(apiError(503, { error: 'x', code: 'metadata_credential_absent' })),
    ).toContain('Settings → Metadata');
  });

  // Null is the caller's cue to render the server's own words unchanged: a 409
  // "already in the library" must not become a lecture about API keys.
  it('says nothing about anything else', () => {
    expect(metadataToast(apiError(409, { error: 'already in the library' }))).toBeNull();
  });

  it('carries the reader through to the copy', () => {
    const said = metadataToast(
      apiError(503, { error: 'x', code: 'metadata_credential_absent' }),
      false,
    );
    expect(said).toContain('Ask a Caravan admin');
  });
});
