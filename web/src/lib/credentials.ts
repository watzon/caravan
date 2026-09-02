/**
 * The credential model the SPA branches on.
 *
 * Two things arrive from the server and they are deliberately different:
 *
 *  - `GET /system/status` reports a cached verdict per credentialed provider —
 *    absent, invalid or ok — so the sidebar can say so without anything calling
 *    the provider.
 *  - Every guarded surface answers 503 with a stable `code` on the error
 *    envelope, so a screen that cannot render knows *why* and can point at the
 *    fix instead of showing the provider's complaint as a toast.
 *
 * This module is pure: it turns a code (or a legacy bare 503) into the fault
 * the UI renders, and holds the one copy of the directed sentence. Unit-tested
 * in credentials.test.ts.
 */

import { ApiError, errorCode } from './api/client';
import type { SystemStatus } from './api/types';
import { translate } from './i18n.svelte';

/**
 * TMDB's provider id (core.ProviderTMDB). It is named here because it is the
 * one id this module treats specially: the flat `metadata_credential` fields a
 * pre-map server answers with are TMDB's and nothing else's.
 */
export const PROVIDER_TMDB = 'tmdb';

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
      title: translate('credential.metadata.invalid.title'),
      message: translate(
        canFix
          ? 'credential.metadata.invalid.admin'
          : 'credential.metadata.invalid.member',
      ),
    };
  }
  return {
    title: translate('credential.metadata.absent.title'),
    message: translate(
      canFix
        ? 'credential.metadata.absent.admin'
        : 'credential.metadata.absent.member',
    ),
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

/**
 * The sidebar's short label for a credential that needs attention.
 *
 * `provider` is the display name to say it about, defaulting to TMDB because
 * that is the only credential the label existed for before the status map did.
 */
export function metadataStateLabel(
  state: MetadataCredentialState,
  provider = 'TMDB',
): string | null {
  if (state === 'absent') return translate('credential.state.absent', { provider });
  if (state === 'invalid') return translate('credential.state.invalid', { provider });
  return null;
}

/** The state a status payload reports, defaulting to the optimistic answer. */
export function metadataStateOf(
  reported: string | undefined | null,
): MetadataCredentialState {
  return reported === 'absent' || reported === 'invalid' ? reported : 'ok';
}

/**
 * One provider's state on a status payload.
 *
 * The per-provider map is the answer whenever the server sent one, including
 * when it holds no entry for `providerId` — a credentialed provider the server
 * knows about is always in it, so an id missing from a map that exists is one
 * this server has no credential for, which is not a problem to warn about.
 *
 * A status with no map at all is an older server, or a fixture written before
 * the map existed. Those still carry TMDB's verdict in the flat fields, so
 * TMDB is answered from there and every other id reads as the optimistic "ok"
 * — the same rule `metadataStateOf` documents for a field that is not there.
 */
export function providerStateOf(
  status: SystemStatus | null | undefined,
  providerId: string,
): MetadataCredentialState {
  const byProvider = status?.metadata_credentials;
  if (byProvider) return metadataStateOf(byProvider[providerId]?.state);
  return providerId === PROVIDER_TMDB ? metadataStateOf(status?.metadata_credential) : 'ok';
}
