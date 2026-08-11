<script lang="ts">
  import type { RuntimeDiagnostics as Diagnostics } from '../api/types';
  import { formatBytes } from '../format';
  import { currentLocale, useI18n } from '../i18n.svelte';
  import SettingsCard from './SettingsCard.svelte';

  interface Props {
    diagnostics: Diagnostics;
  }

  let { diagnostics }: Props = $props();
  const { t, tp } = useI18n();

  function formatUptime(seconds: number): string {
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    if (days > 0) return `${days}d ${hours}h ${minutes}m`;
    if (hours > 0) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
  }
</script>

<SettingsCard
  title={t('component.runtimeDiagnostics.title')}
  description={t('component.runtimeDiagnostics.description')}>
  <dl class="grid gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
    <div>
      <dt class="micro-label">{t('component.runtimeDiagnostics.started')}</dt>
      <dd class="mt-1 font-mono text-sm text-ink">{new Date(diagnostics.started_at).toLocaleString(currentLocale())}</dd>
      <dd class="text-sm text-ink-secondary">{t('component.runtimeDiagnostics.up', { duration: formatUptime(diagnostics.uptime_seconds) })}</dd>
    </div>
    <div>
      <dt class="micro-label">{t('component.runtimeDiagnostics.runtime')}</dt>
      <dd class="mt-1 font-mono text-sm text-ink">{diagnostics.go_version}</dd>
      <dd class="font-mono text-sm text-ink-secondary">{diagnostics.os}/{diagnostics.arch}</dd>
    </div>
    <div>
      <dt class="micro-label">{t('component.runtimeDiagnostics.process')}</dt>
      <dd class="mt-1 font-mono text-sm text-ink">{tp('component.runtimeDiagnostics.goroutines', diagnostics.goroutines)}</dd>
      <dd class="text-sm text-ink-secondary">{t('component.runtimeDiagnostics.allocated', { bytes: formatBytes(diagnostics.memory_alloc_bytes) })}</dd>
    </div>
    <div>
      <dt class="micro-label">{t('component.runtimeDiagnostics.listenAddress')}</dt>
      <dd class="mt-1 break-all font-mono text-sm text-ink">{diagnostics.listen_address}</dd>
      <dd class="text-sm text-ink-secondary">{t('component.runtimeDiagnostics.logLevel', { level: diagnostics.log_level })}</dd>
    </div>
    <div>
      <dt class="micro-label">{t('component.runtimeDiagnostics.database')}</dt>
      <dd class="mt-1 break-all font-mono text-sm text-ink">{diagnostics.database_path}</dd>
      <dd class="text-sm text-ink-secondary">{formatBytes(diagnostics.database_size_bytes)}</dd>
    </div>
    <div>
      <dt class="micro-label">{t('component.runtimeDiagnostics.configuration')}</dt>
      <dd class="mt-1 break-all font-mono text-sm text-ink">{diagnostics.config_file}</dd>
      <dd class="break-all text-sm text-ink-secondary">{t('component.runtimeDiagnostics.directory', { path: diagnostics.config_dir })}</dd>
    </div>
  </dl>
</SettingsCard>
