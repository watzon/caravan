import { describe, expect, it } from 'vitest';
import { sourceLogoLockup } from './sourceLogo';

describe('sourceLogoLockup', () => {
  it('trusts an explicit flag over the filename', () => {
    expect(sourceLogoLockup('https://img/hUzeosd33nzE5MCNsZxCGEKTXaQ.png', false)).toBe(false);
    expect(sourceLogoLockup('https://img/hulu.png', true)).toBe(true);
  });

  it('treats the Marvel lockup as a lockup when the flag is missing', () => {
    expect(sourceLogoLockup('https://image.tmdb.org/t/p/w185/hUzeosd33nzE5MCNsZxCGEKTXaQ.png')).toBe(
      true,
    );
  });

  it('treats a flat mark as a silhouette when the flag is missing', () => {
    expect(sourceLogoLockup('https://image.tmdb.org/t/p/w185/pqUTCleNUiTLAVlelGxUgWn1ELh.png')).toBe(
      false,
    );
  });
});
