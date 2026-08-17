/**
 * Which treatment a shelf mark gets. Flat one-color artwork silhouettes to
 * ink; a lockup (Marvel's red box) keeps the tri-tone so the letters survive.
 *
 * The filename fallback covers a cached /discover payload that predates the
 * `lockup` field.
 */
const LOCKUP_FILES = ['hUzeosd33nzE5MCNsZxCGEKTXaQ'];

export function sourceLogoLockup(src: string, lockup?: boolean): boolean {
  if (typeof lockup === 'boolean') return lockup;
  return LOCKUP_FILES.some((id) => src.includes(id));
}
