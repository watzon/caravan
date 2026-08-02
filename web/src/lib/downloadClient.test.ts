import { describe, expect, it } from 'vitest';
import type { DownloadClientTypeInfo } from './api/types';
import {
  DEFAULT_DOWNLOAD_CLIENT_PRIORITY,
  FALLBACK_DOWNLOAD_CLIENT_TYPES,
  describeType,
  parsePriority,
  validateDownloadClient,
} from './downloadClient';

const QBIT: DownloadClientTypeInfo = {
  type: 'qbittorrent',
  label: 'qBittorrent',
  protocol: 'torrent',
  uses_login: true,
  uses_api_key: false,
  supported: true,
};

const SAB: DownloadClientTypeInfo = {
  type: 'sabnzbd',
  label: 'SABnzbd',
  protocol: 'usenet',
  uses_login: false,
  uses_api_key: true,
  supported: true,
};

describe('describeType', () => {
  it('prefers the server-served descriptor', () => {
    expect(describeType([QBIT, SAB], 'sabnzbd')).toBe(SAB);
  });

  it('falls back to the built-in table before the list arrives', () => {
    expect(describeType([], 'nzbget').label).toBe('NZBGet');
    // Nothing is claimed supported until the server says so.
    expect(describeType([], 'nzbget').supported).toBe(false);
  });

  it('never returns undefined for a type it has never heard of', () => {
    const unknown = describeType([], 'transmission' as never);
    expect(unknown.label).toBe('transmission');
  });
});

describe('validateDownloadClient', () => {
  const base = { name: 'qBit', url: 'http://127.0.0.1:8080', username: 'admin', apiKey: '' };

  it('accepts a complete login client', () => {
    expect(
      validateDownloadClient({ ...base, type: QBIT, hasStoredCredential: false }),
    ).toBeNull();
  });

  it('requires a name and an http URL', () => {
    expect(
      validateDownloadClient({ ...base, name: ' ', type: QBIT, hasStoredCredential: false }),
    ).toMatch(/name/i);
    expect(
      validateDownloadClient({ ...base, url: '', type: QBIT, hasStoredCredential: false }),
    ).toMatch(/URL/);
    expect(
      validateDownloadClient({ ...base, url: '127.0.0.1:8080', type: QBIT, hasStoredCredential: false }),
    ).toMatch(/http/);
  });

  it('asks for the credential the backend actually uses', () => {
    expect(
      validateDownloadClient({ ...base, username: '', type: QBIT, hasStoredCredential: false }),
    ).toMatch(/username/);
    // SABnzbd does not use a username, so a blank one is not a problem.
    expect(
      validateDownloadClient({ ...base, username: '', apiKey: 'k', type: SAB, hasStoredCredential: false }),
    ).toBeNull();
    expect(
      validateDownloadClient({ ...base, apiKey: '', type: SAB, hasStoredCredential: false }),
    ).toMatch(/API key/);
  });

  // The redaction consequence: the edit form starts blank over a stored key,
  // and blank must not be read as missing.
  it('accepts a blank API key when one is already stored', () => {
    expect(
      validateDownloadClient({ ...base, apiKey: '', type: SAB, hasStoredCredential: true }),
    ).toBeNull();
  });

  // A blank password is legitimate: qBittorrent can skip auth for localhost.
  it('never demands a password', () => {
    expect(
      validateDownloadClient({ ...base, type: QBIT, hasStoredCredential: false }),
    ).toBeNull();
  });
});

describe('parsePriority', () => {
  it('reads what the user typed', () => {
    expect(parsePriority(' 5 ')).toBe(5);
    expect(parsePriority('0')).toBe(0);
  });

  it('falls back to the default rather than blocking a save on a typo', () => {
    for (const bad of ['', 'high', '-1', '2.5']) {
      expect(parsePriority(bad)).toBe(DEFAULT_DOWNLOAD_CLIENT_PRIORITY);
    }
  });
});

describe('FALLBACK_DOWNLOAD_CLIENT_TYPES', () => {
  it('covers every backend the server knows, with one credential shape each', () => {
    expect(FALLBACK_DOWNLOAD_CLIENT_TYPES.map((t) => t.type)).toEqual([
      'qbittorrent',
      'sabnzbd',
      'nzbget',
    ]);
    for (const t of FALLBACK_DOWNLOAD_CLIENT_TYPES) {
      expect(t.uses_login || t.uses_api_key).toBe(true);
    }
  });
});
