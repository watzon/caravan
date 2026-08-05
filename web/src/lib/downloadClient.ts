/**
 * External download-client settings helpers (SPEC §5.1 "Download Clients").
 *
 * The server owns the type table (GET /download-clients/types): which backends
 * exist, which protocol each carries and which credentials each needs. These
 * helpers only fall back when that list has not arrived yet, and validate what
 * the form can check without a round trip.
 *
 * Pure — unit-tested in downloadClient.test.ts.
 */

import type { DownloadClientTypeInfo, DownloadClientType } from './api/types';

/**
 * What the form offers before GET /download-clients/types answers. The server's
 * answer replaces this wholesale; it exists so the add form is usable on a slow
 * or failed load rather than showing an empty type picker.
 */
export const FALLBACK_DOWNLOAD_CLIENT_TYPES: DownloadClientTypeInfo[] = [
  {
    type: 'qbittorrent',
    label: 'qBittorrent',
    protocol: 'torrent',
    uses_login: true,
    uses_api_key: false,
    supported: false,
  },
  {
    type: 'sabnzbd',
    label: 'SABnzbd',
    protocol: 'usenet',
    uses_login: false,
    uses_api_key: true,
    supported: false,
  },
  {
    type: 'nzbget',
    label: 'NZBGet',
    protocol: 'usenet',
    uses_login: true,
    uses_api_key: false,
    supported: false,
  },
];

/** The descriptor for a type, or a usable stand-in for one we do not know. */
export function describeType(
  types: readonly DownloadClientTypeInfo[],
  type: DownloadClientType,
): DownloadClientTypeInfo {
  return (
    types.find((t) => t.type === type) ??
    FALLBACK_DOWNLOAD_CLIENT_TYPES.find((t) => t.type === type) ?? {
      type,
      label: type,
      protocol: 'torrent',
      uses_login: true,
      uses_api_key: true,
      supported: false,
    }
  );
}

/**
 * Why this configuration cannot be saved, or null when it can.
 *
 * The server validates too; this exists so the user is told before a round
 * trip. `hasStoredCredential` is the redaction consequence: an edit form starts
 * with a blank credential field over a stored credential, and blank must not be
 * read as missing.
 */
export function validateDownloadClient(input: {
  name: string;
  url: string;
  username: string;
  apiKey: string;
  type: DownloadClientTypeInfo;
  hasStoredCredential: boolean;
}): string | null {
  if (input.name.trim() === '') return 'Give the download client a name.';
  const url = input.url.trim();
  if (url === '') return 'The download client needs a base URL.';
  if (!/^https?:\/\//i.test(url)) return 'The URL must start with http:// or https://.';
  if (input.type.uses_login && input.username.trim() === '') {
    return `${input.type.label} needs a username.`;
  }
  if (input.type.uses_api_key && input.apiKey.trim() === '' && !input.hasStoredCredential) {
    return `${input.type.label} needs an API key.`;
  }
  return null;
}

export const DEFAULT_DOWNLOAD_CLIENT_PRIORITY = 25;

/**
 * A count field where blank and nonsense both mean "no limit".
 *
 * Unlike priority there is no sensible default other than zero: a cap the user
 * has not set must never stop a download from starting, so anything unreadable
 * reads as unlimited rather than as the smallest number.
 */
export function parseCount(text: string): number {
  const trimmed = text.trim();
  if (trimmed === '') return 0;
  const n = Number(trimmed);
  if (!Number.isInteger(n) || n < 0) return 0;
  return n;
}

/**
 * Turn the priority field's text into a number. Anything unparseable or
 * negative falls back to the server's own default rather than blocking a save
 * on a typo — the same forgiveness parseCategories shows.
 */
export function parsePriority(text: string): number {
  const trimmed = text.trim();
  // Guarded explicitly: Number('') is 0, and a cleared field means "I did not
  // set one", not "highest possible priority".
  if (trimmed === '') return DEFAULT_DOWNLOAD_CLIENT_PRIORITY;
  const n = Number(trimmed);
  if (!Number.isInteger(n) || n < 0) return DEFAULT_DOWNLOAD_CLIENT_PRIORITY;
  return n;
}
