/**
 * System status (SPEC §11 `GET /system/status`), shared by the sidebar widget,
 * the first-run gate and the settings screen. One fetch, one source of truth.
 */

import { api, errorText } from '../api/client';
import type { SystemStatus } from '../api/types';
import { metadataStateOf, type MetadataCredentialState } from '../credentials';

class SystemState {
  status = $state<SystemStatus | null>(null);
  error = $state<string | null>(null);
  loading = $state(true);

  /** True until an administrator exists and storage has been configured. */
  get needsSetup(): boolean {
    return this.status !== null && (this.status.needs_setup ?? this.status.storage_root === '');
  }

  /**
   * The TMDB key's state as of the last status fetch (PLAN phase 10 task 2).
   *
   * A status we have not fetched — and a member, who may not fetch it at all —
   * reads as "ok": the badge this feeds is a warning, and warning about a
   * credential nobody has looked at would be noise. The guarded surfaces get
   * their answer from the failed call itself, not from here.
   */
  get metadataCredential(): MetadataCredentialState {
    return metadataStateOf(this.status?.metadata_credential);
  }

  /** Why the key was rejected, in the provider's words. '' unless invalid. */
  get metadataCredentialReason(): string {
    return this.status?.metadata_credential_reason ?? '';
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
