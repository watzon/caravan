/**
 * System status (SPEC §11 `GET /system/status`), shared by the sidebar widget,
 * the first-run gate and the settings screen. One fetch, one source of truth.
 */

import { api, errorText } from '../api/client';
import type { SystemStatus } from '../api/types';

class SystemState {
  status = $state<SystemStatus | null>(null);
  error = $state<string | null>(null);
  loading = $state(true);

  /** True once we know the server has no storage root yet (SPEC §10.1). */
  get needsSetup(): boolean {
    return this.status !== null && this.status.storage_root === '';
  }

  async refresh(): Promise<void> {
    this.loading = true;
    try {
      this.status = await api.systemStatus();
      this.error = null;
    } catch (err) {
      this.error = errorText(err);
    } finally {
      this.loading = false;
    }
  }
}

export const system = new SystemState();
