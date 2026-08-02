/**
 * Session state for the optional single-user password (SPEC §11).
 *
 * There is no token to hold: the session is an HttpOnly cookie the browser
 * attaches by itself. All this tracks is whether the server has told us the
 * session is missing or expired, which is what puts the login screen in front
 * of the app.
 */

import { api, errorText, onUnauthorized } from '../api/client';
import { clearToasts } from './toast.svelte';

class AuthState {
  /** The server answered 401: nothing works until a login succeeds. */
  required = $state(false);
  /** A login or logout is in flight. */
  busy = $state(false);
  /** Why the last login failed; cleared by the next attempt. */
  error = $state<string | null>(null);

  /** Returns true when the password was accepted. */
  async login(password: string): Promise<boolean> {
    this.busy = true;
    this.error = null;
    try {
      await api.login(password);
      this.required = false;
      return true;
    } catch (err) {
      this.error = errorText(err);
      return false;
    } finally {
      this.busy = false;
    }
  }

  /**
   * End the session. The login screen goes up whether or not the server
   * answered: a logout that failed to reach the server has still ended the
   * user's intent to be logged in, and the next request will 401 anyway.
   */
  async logout(): Promise<void> {
    this.busy = true;
    try {
      await api.logout();
    } catch {
      // Deliberately ignored - see above.
    } finally {
      this.busy = false;
      this.error = null;
      this.required = true;
      clearToasts();
    }
  }
}

export const auth = new AuthState();

// One 401 anywhere means the session is gone. Toasts are cleared with it: a
// dead session fails every in-flight poll at once.
onUnauthorized(() => {
  auth.required = true;
  clearToasts();
});
