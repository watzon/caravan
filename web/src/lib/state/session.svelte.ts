/**
 * Who is signed in, and therefore which half of the SPA to render (SPEC §11).
 *
 * Fed once at boot by `GET /auth/me` and again after a login. The server is the
 * enforcer — every route a member may not reach answers 403 whatever this store
 * says — so everything below is courtesy: hiding a door that would not open is
 * kinder than letting somebody walk into it.
 */

import { adultVisible } from '../adult';
import { api, errorText } from '../api/client';
import type { SessionUser, UserRole } from '../api/types';

class SessionState {
  /** Null until /auth/me answers, and again after a logout. */
  user = $state<SessionUser | null>(null);
  /** The first fetch has not come back yet. */
  loading = $state(true);
  /** Why /auth/me failed. A 401 is the login screen's business, not this. */
  error = $state<string | null>(null);

  /** "" on a server with no accounts: there is nobody to name. */
  get username(): string {
    return this.user?.username ?? '';
  }

  get role(): UserRole {
    return this.user?.role ?? 'admin';
  }

  /** True when the server has no accounts, so anyone who reaches it is an admin. */
  get open(): boolean {
    return this.user?.open === true;
  }

  /**
   * Whether to render the admin half of the app.
   *
   * An unknown identity reads as admin on purpose. The cost of guessing wrong
   * that way is a nav group whose screens 403; guessing the other way would
   * bounce an admin off /settings on every boot, before /auth/me had answered.
   * Only a *known* member gets the reduced UI.
   */
  get isAdmin(): boolean {
    return this.user === null || this.user.role === 'admin';
  }

  /**
   * Whether the adult module is on screen at all for this browser.
   *
   * Unlike `isAdmin`, an unknown identity reads as NOT visible. The costs are
   * asymmetric in the other direction here: guessing "admin" wrong shows a nav
   * group whose screens 403, while guessing "adult" wrong shows the module on a
   * server where somebody switched it off — which is the one thing this phase
   * promises never happens.
   */
  get adult(): boolean {
    return adultVisible(this.user);
  }

  async refresh(): Promise<void> {
    this.loading = true;
    try {
      this.user = await api.me();
      this.error = null;
    } catch (err) {
      // The previous answer is left standing: a failed poll is not evidence
      // that the person's role changed, and downgrading the UI on a blip would
      // yank screens out from under them.
      this.error = errorText(err);
    } finally {
      this.loading = false;
    }
  }

  /** Forget who was signed in — the logout half of `refresh`. */
  forget(): void {
    this.user = null;
    this.error = null;
  }
}

export const session = new SessionState();
