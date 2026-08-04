/**
 * The credential model the SPA branches on (PLAN phase 10 tasks 2-5).
 *
 * Two things arrive from the server and they are deliberately different:
 *
 *  - `GET /system/status` reports a cached verdict on the TMDB key — absent,
 *    invalid or ok — so the sidebar can say so without anything calling TMDB.
 *  - Every guarded surface answers 503 with a stable `code` on the error
 *    envelope, so a screen that cannot render knows *why* and can point at the
 *    fix instead of showing the provider's complaint as a toast.
 *
 * This module is pure: it turns a code (or a legacy bare 503) into the fault
 * the UI renders, and holds the one copy of the directed sentence. Unit-tested
 * in credentials.test.ts.
 */

import { ApiError, errorCode } from './api/client';

/** Error-envelope codes (internal/api/credentials.go). */
export const CODE_METADATA_ABSENT = 'metadata_credential_absent';
export const CODE_METADATA_INVALID = 'metadata_credential_invalid';
export const CODE_ADULT_ABSENT = 'adult_credential_absent';
export const CODE_ADULT_INVALID = 'adult_credential_invalid';

/**
 * The cached verdict `GET /system/status` reports. `ok` is the optimistic
 * answer for a key nothing has rejected, so a status with no field at all —
 * an older server, a test fixture — reads as ok rather than as a problem.
 */
export type MetadataCredentialState = 'absent' | 'invalid' | 'ok';

/** The two ways a credential stops a screen from rendering. */
export type CredentialFault = 'absent' | 'invalid';

/**
 * What a failed call says about the TMDB key, or null when it says nothing.
 *
 * A code is believed first. A 503 with no code is read as "absent": that is
 * what every metadata-needing route answered before this phase, so an older
 * server keeps the empty state it always had rather than falling through to a
 * raw error.
 */
export function metadataFault(err: unknown): CredentialFault | null {
  const code = errorCode(err);
  if (code === CODE_METADATA_ABSENT) return 'absent';
  if (code === CODE_METADATA_INVALID) return 'invalid';
  if (code !== '') return null;
  return err instanceof ApiError && err.status === 503 ? 'absent' : null;
}

/** The same question for the adult module's stash-box credential. */
export function adultFault(err: unknown): CredentialFault | null {
  const code = errorCode(err);
  if (code === CODE_ADULT_ABSENT) return 'absent';
  if (code === CODE_ADULT_INVALID) return 'invalid';
  return null;
}

export interface CredentialCopy {
  title: string;
  message: string;
}

/**
 * The one wording for each fault, in the two versions the two roles need.
 *
 * `canFix` is the reader's own ability to do the thing being named — in
 * practice `session.isAdmin`, because Settings is admin-only (MEMBER_ROUTES in
 * router.ts). Naming a destination a member cannot open is worse than naming
 * none: Discover and the request flow are exactly where a member lives, so the
 * "fix" they were handed used to bounce them straight back to the screen that
 * sent them. Both versions still say what would change the situation; only the
 * version with a door in it points at one.
 */
export function metadataCopy(fault: CredentialFault, canFix = true): CredentialCopy {
  if (fault === 'invalid') {
    return {
      title: 'TMDB rejected this API key',
      message: canFix
        ? 'The key on file was refused, so nothing can be looked up. Correct it in Settings → Metadata and this screen fills in.'
        : 'The key on file was refused, so nothing can be looked up. Ask a Caravan admin to correct the TMDB API key.',
    };
  }
  return {
    title: 'No TMDB API key yet',
    message: canFix
      ? 'Caravan reads titles, artwork and episode data from TMDB. Add your TMDB API key in Settings → Metadata and this screen fills in.'
      : 'Caravan reads titles, artwork and episode data from TMDB. Ask a Caravan admin to add a TMDB API key.',
  };
}

/**
 * The directed sentence for a credential failure, for the few surfaces whose
 * only affordance is a toast — approving a request happens from a row or a
 * dialog that is about to close, so there is no empty state to fall back to.
 * Null when the failure was not about the credential, which is the caller's
 * cue to render the server's own words unchanged.
 */
export function metadataToast(err: unknown, canFix = true): string | null {
  const fault = metadataFault(err);
  return fault ? metadataCopy(fault, canFix).message : null;
}

/** The sidebar's short label for a credential that needs attention. */
export function metadataStateLabel(state: MetadataCredentialState): string | null {
  if (state === 'absent') return 'No TMDB key';
  if (state === 'invalid') return 'TMDB key rejected';
  return null;
}

/** The state a status payload reports, defaulting to the optimistic answer. */
export function metadataStateOf(
  reported: string | undefined | null,
): MetadataCredentialState {
  return reported === 'absent' || reported === 'invalid' ? reported : 'ok';
}
