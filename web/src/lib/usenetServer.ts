/**
 * News-server settings helpers (SPEC §5.1 "Usenet servers").
 *
 * A news server is where Caravan's built-in engine reads article bodies from.
 * These helpers hold the defaults the form shows and the checks it can make
 * without a round trip; the server validates everything again.
 *
 * Pure — unit-tested in usenetServer.test.ts.
 */

/** internal/core.UsenetDefault* — the values the server fills a blank in with. */
export const DEFAULT_USENET_PORT = 119;
export const DEFAULT_USENET_TLS_PORT = 563;
export const DEFAULT_USENET_MAX_CONNECTIONS = 8;
export const DEFAULT_USENET_PRIORITY = 25;

/** The port a blank field means, which depends on whether TLS is on. */
export function defaultUsenetPort(tls: boolean): number {
  return tls ? DEFAULT_USENET_TLS_PORT : DEFAULT_USENET_PORT;
}

/**
 * Whether `text` is the port the TLS toggle would have filled in anyway.
 *
 * The form uses this to decide if flipping TLS may rewrite the port box:
 * a user who typed 9119 keeps it, a user who never touched the field moves
 * from 563 to 119 with the toggle.
 */
export function isDefaultUsenetPort(text: string): boolean {
  const trimmed = text.trim();
  return (
    trimmed === '' ||
    trimmed === String(DEFAULT_USENET_PORT) ||
    trimmed === String(DEFAULT_USENET_TLS_PORT)
  );
}

/**
 * Turn a numeric field's text into a value for the wire.
 *
 * Anything unparseable or negative falls back to `fallback` rather than
 * blocking a save on a typo — the same forgiveness parsePriority shows. The
 * empty string is guarded explicitly: Number('') is 0, and a cleared box means
 * "I did not set one", not "zero".
 */
export function parseUsenetNumber(text: string, fallback: number): number {
  const trimmed = text.trim();
  if (trimmed === '') return fallback;
  const n = Number(trimmed);
  if (!Number.isInteger(n) || n < 0) return fallback;
  return n;
}

/**
 * Why this configuration cannot be saved, or null when it can.
 *
 * The server validates too; this exists so the user is told before a round
 * trip. `hasStoredPassword` is the redaction consequence: an edit form starts
 * with a blank password field over a stored one, and blank must not be read as
 * missing.
 */
export function validateUsenetServer(input: {
  name: string;
  host: string;
  port: string;
  username: string;
  password: string;
  maxConnections: string;
  hasStoredPassword: boolean;
}): string | null {
  if (input.name.trim() === '') return 'Give the news server a name.';
  const host = input.host.trim();
  if (host === '') return 'The news server needs a hostname.';
  // A hostname, not a URL: pasting the provider's web address is the likely
  // mistake, and it fails at dial time with a much worse message.
  if (/^[a-z]+:\/\//i.test(host)) return 'Enter just the hostname, without http:// or a path.';
  if (host.includes('/')) return 'Enter just the hostname, without a path.';

  const port = input.port.trim();
  if (port !== '') {
    const n = Number(port);
    if (!Number.isInteger(n) || n < 1 || n > 65535) {
      return 'The port must be a whole number between 1 and 65535.';
    }
  }

  const max = input.maxConnections.trim();
  if (max !== '') {
    const n = Number(max);
    if (!Number.isInteger(n) || n < 1) {
      return 'Connections must be a whole number of at least 1.';
    }
  }

  // A password with nothing to send it for is the one credential combination
  // the transport refuses outright, so catch it here rather than at dial time.
  if (input.username.trim() === '' && (input.password !== '' || input.hasStoredPassword)) {
    return 'A password needs a username to go with it.';
  }
  return null;
}
