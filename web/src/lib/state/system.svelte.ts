/**
 * System status (SPEC §11 `GET /system/status`), shared by the sidebar widget,
 * the first-run gate and the settings screen. One fetch, one source of truth.
 */

import { api, errorText } from '../api/client';
import type { SystemStatus } from '../api/types';
import {
  PROVIDER_TMDB,
  providerStateOf,
  type MetadataCredentialState,
} from '../credentials';

/** One provider's credential health, as the surfaces that warn about it read it. */
export interface ProviderCredentialState {
  state: MetadataCredentialState;
  /** The provider's own words for a rejection. '' unless invalid. */
  reason: string;
  /** RFC3339 timestamp of the verdict. '' when nothing has checked yet. */
  checkedAt: string;
}

/** A credential that needs attention, with the id to name it by. */
export interface UnhealthyCredential extends ProviderCredentialState {
  id: string;
}

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
    return this.credential(PROVIDER_TMDB).state;
  }

  /** Why the key was rejected, in the provider's words. '' unless invalid. */
  get metadataCredentialReason(): string {
    return this.credential(PROVIDER_TMDB).reason;
  }

  /**
   * One provider's credential health as of the last status fetch.
   *
   * The per-provider map is read when the server sent one, and TMDB falls back
   * to the flat fields when it did not — the server fills those from the map's
   * TMDB entry, so the two answers are the same answer.
   */
  credential(providerId: string): ProviderCredentialState {
    const state = providerStateOf(this.status, providerId);
    const entry = this.status?.metadata_credentials?.[providerId];
    if (entry) return { state, reason: entry.reason ?? '', checkedAt: entry.checked_at ?? '' };
    if (this.status?.metadata_credentials || providerId !== PROVIDER_TMDB) {
      return { state, reason: '', checkedAt: '' };
    }
    return {
      state,
      reason: this.status?.metadata_credential_reason ?? '',
      checkedAt: this.status?.metadata_credential_checked_at ?? '',
    };
  }

  /**
   * Every credential that needs attention, for the sidebar's warning rows.
   *
   * Sorted by id so the rows do not reorder between polls. A server that sent
   * no map is asked about TMDB alone, which is every credential such a server
   * has an opinion about.
   */
  get unhealthyCredentials(): UnhealthyCredential[] {
    const byProvider = this.status?.metadata_credentials;
    const ids = byProvider ? Object.keys(byProvider).sort() : [PROVIDER_TMDB];
    return ids
      .map((id) => ({ id, ...this.credential(id) }))
      .filter((c) => c.state !== 'ok');
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
