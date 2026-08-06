<script lang="ts">
  import type { RuntimeDiagnostics as Diagnostics } from '../api/types';
  import { formatBytes } from '../format';
  import SettingsCard from './SettingsCard.svelte';

  interface Props {
    diagnostics: Diagnostics;
  }

  let { diagnostics }: Props = $props();

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
  title="Runtime diagnostics"
  description="Process details and resolved paths for troubleshooting this Caravan server.">
  <dl class="grid gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
    <div>
      <dt class="micro-label">Started</dt>
      <dd class="mt-1 font-mono text-sm text-ink">{new Date(diagnostics.started_at).toLocaleString()}</dd>
      <dd class="text-sm text-ink-secondary">Up {formatUptime(diagnostics.uptime_seconds)}</dd>
    </div>
    <div>
      <dt class="micro-label">Runtime</dt>
      <dd class="mt-1 font-mono text-sm text-ink">{diagnostics.go_version}</dd>
      <dd class="font-mono text-sm text-ink-secondary">{diagnostics.os}/{diagnostics.arch}</dd>
    </div>
    <div>
      <dt class="micro-label">Process</dt>
      <dd class="mt-1 font-mono text-sm text-ink">{diagnostics.goroutines} goroutines</dd>
      <dd class="text-sm text-ink-secondary">{formatBytes(diagnostics.memory_alloc_bytes)} allocated</dd>
    </div>
    <div>
      <dt class="micro-label">Listen address</dt>
      <dd class="mt-1 break-all font-mono text-sm text-ink">{diagnostics.listen_address}</dd>
      <dd class="text-sm text-ink-secondary">Log level {diagnostics.log_level}</dd>
    </div>
    <div>
      <dt class="micro-label">Database</dt>
      <dd class="mt-1 break-all font-mono text-sm text-ink">{diagnostics.database_path}</dd>
      <dd class="text-sm text-ink-secondary">{formatBytes(diagnostics.database_size_bytes)}</dd>
    </div>
    <div>
      <dt class="micro-label">Configuration</dt>
      <dd class="mt-1 break-all font-mono text-sm text-ink">{diagnostics.config_file}</dd>
      <dd class="break-all text-sm text-ink-secondary">Directory {diagnostics.config_dir}</dd>
    </div>
  </dl>
</SettingsCard>
