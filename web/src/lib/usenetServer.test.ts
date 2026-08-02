import { describe, expect, it } from 'vitest';
import {
  DEFAULT_USENET_MAX_CONNECTIONS,
  DEFAULT_USENET_PORT,
  DEFAULT_USENET_PRIORITY,
  DEFAULT_USENET_TLS_PORT,
  defaultUsenetPort,
  isDefaultUsenetPort,
  parseUsenetNumber,
  validateUsenetServer,
} from './usenetServer';

/** A configuration with nothing wrong with it, for one-field overrides. */
function valid(over: Partial<Parameters<typeof validateUsenetServer>[0]> = {}) {
  return {
    name: 'Eweka',
    host: 'news.eweka.nl',
    port: '563',
    username: 'user',
    password: 'secret',
    maxConnections: '20',
    hasStoredPassword: false,
    ...over,
  };
}

describe('defaultUsenetPort', () => {
  it('is the NNTPS port with TLS and the plaintext port without', () => {
    expect(defaultUsenetPort(true)).toBe(DEFAULT_USENET_TLS_PORT);
    expect(defaultUsenetPort(false)).toBe(DEFAULT_USENET_PORT);
  });
});

describe('isDefaultUsenetPort', () => {
  it('treats both protocol defaults and a blank box as untouched', () => {
    expect(isDefaultUsenetPort('')).toBe(true);
    expect(isDefaultUsenetPort('  ')).toBe(true);
    expect(isDefaultUsenetPort('563')).toBe(true);
    expect(isDefaultUsenetPort('119')).toBe(true);
  });

  it('leaves a port the user actually chose alone', () => {
    expect(isDefaultUsenetPort('9119')).toBe(false);
    expect(isDefaultUsenetPort('443')).toBe(false);
  });
});

describe('parseUsenetNumber', () => {
  it('reads a whole number', () => {
    expect(parseUsenetNumber('20', DEFAULT_USENET_MAX_CONNECTIONS)).toBe(20);
    expect(parseUsenetNumber('  5  ', DEFAULT_USENET_PRIORITY)).toBe(5);
    expect(parseUsenetNumber('0', DEFAULT_USENET_PRIORITY)).toBe(0);
  });

  it('falls back rather than blocking a save on a typo', () => {
    // Number('') is 0, and a cleared box means "I did not set one".
    expect(parseUsenetNumber('', 8)).toBe(8);
    expect(parseUsenetNumber('abc', 8)).toBe(8);
    expect(parseUsenetNumber('-1', 8)).toBe(8);
    expect(parseUsenetNumber('1.5', 8)).toBe(8);
  });
});

describe('validateUsenetServer', () => {
  it('accepts a complete configuration', () => {
    expect(validateUsenetServer(valid())).toBeNull();
  });

  it('accepts an anonymous server with no credentials at all', () => {
    expect(validateUsenetServer(valid({ username: '', password: '' }))).toBeNull();
  });

  it('accepts a blank port, which means the protocol default', () => {
    expect(validateUsenetServer(valid({ port: '' }))).toBeNull();
  });

  it('requires a name and a host', () => {
    expect(validateUsenetServer(valid({ name: '  ' }))).toMatch(/name/i);
    expect(validateUsenetServer(valid({ host: '  ' }))).toMatch(/hostname/i);
  });

  it('rejects a URL pasted into the hostname box', () => {
    // The likely mistake: the provider's web address rather than the NNTP host.
    expect(validateUsenetServer(valid({ host: 'https://news.eweka.nl' }))).toMatch(/hostname/i);
    expect(validateUsenetServer(valid({ host: 'news.eweka.nl/nntp' }))).toMatch(/path/i);
  });

  it('rejects a port outside the range a TCP port can hold', () => {
    expect(validateUsenetServer(valid({ port: '0' }))).toMatch(/65535/);
    expect(validateUsenetServer(valid({ port: '70000' }))).toMatch(/65535/);
    expect(validateUsenetServer(valid({ port: 'nope' }))).toMatch(/65535/);
  });

  it('rejects a connection cap below one, which could never fetch anything', () => {
    expect(validateUsenetServer(valid({ maxConnections: '0' }))).toMatch(/at least 1/i);
    expect(validateUsenetServer(valid({ maxConnections: '-4' }))).toMatch(/at least 1/i);
  });

  it('rejects a password with no username to send it for', () => {
    expect(validateUsenetServer(valid({ username: '', password: 'secret' }))).toMatch(/username/i);
  });

  // The redaction consequence: an edit form is blank over a stored password, so
  // clearing the username while one is stored is the same broken pair.
  it('counts a stored password when the username is cleared', () => {
    expect(
      validateUsenetServer(valid({ username: '', password: '', hasStoredPassword: true })),
    ).toMatch(/username/i);
  });

  // ...but a blank password field over a stored one is the normal edit, not an
  // error.
  it('accepts a blank password field when one is already stored', () => {
    expect(validateUsenetServer(valid({ password: '', hasStoredPassword: true }))).toBeNull();
  });
});
