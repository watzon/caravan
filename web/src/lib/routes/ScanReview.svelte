<script lang="ts">
  /**
   * Scan review (SPEC §10.1 step 3, §13): everything the scanner could not
   * confidently match parks here with the parser's best guess, and the user
   * resolves it by hand. Nothing is ever dropped silently.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { UnmatchedFile } from '../api/types';
  import AddItemModal from '../components/AddItemModal.svelte';
  import Badge from '../components/Badge.svelte';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import {
    UNKNOWN,
    episodeCode,
    formatBytes,
    formatConfidence,
    truncateMiddle,
  } from '../format';
  import { libraries } from '../state/libraries.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';

  let files = $state<UnmatchedFile[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let scanning = $state(false);
  let busyID = $state<number | null>(null);
  let matching = $state<UnmatchedFile | null>(null);

  async function load() {
    loading = true;
    try {
      files = await api.listUnmatched();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
    // Names for the library column. /libraries is admin-only and this screen
    // is an admin's, so the lazy load is safe here.
    void libraries.load();
  });

  async function rescan() {
    scanning = true;
    try {
      await api.rescan();
      const summary = await api.awaitScan();
      pushToast(
        `Scan finished: ${summary.media_files} files in the library, ${summary.unmatched} unmatched.`,
        summary.unmatched > 0 ? 'warning' : 'success',
      );
      await load();
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      scanning = false;
    }
  }

  async function dismiss(file: UnmatchedFile) {
    busyID = file.id;
    try {
      await api.dismissUnmatched(file.id);
      files = (files ?? []).filter((f) => f.id !== file.id);
      pushToast('Removed from the review queue.', 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  async function confirmMatch(kind: 'movie' | 'series', tmdbID: number) {
    const file = matching;
    if (!file) return;
    try {
      await api.matchUnmatched(file.id, { type: kind, tmdb_id: tmdbID });
      files = (files ?? []).filter((f) => f.id !== file.id);
      matching = null;
      pushToast('Matched and queued for import.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    }
  }

  /**
   * What the manual match searches for.
   *
   * A file the scanner parked is only ever what its name looks like. A file an
   * untied universal-search grab parked knows better than that: the user
   * already said which library it belongs to, and a library has exactly one
   * kind — so that answer beats the parser's guess. An adult library is the
   * one case it cannot answer, because a site is named by a stash-box id and
   * the match dialog resolves TMDB ids; there the parse still decides.
   */
  function guessKind(file: UnmatchedFile): 'movie' | 'series' {
    const kind = libraryOf(file)?.kind;
    if (kind === 'movie') return 'movie';
    if (kind === 'tv') return 'series';
    return (file.parsed.episodes?.length ?? 0) > 0 ? 'series' : 'movie';
  }

  function libraryOf(file: UnmatchedFile) {
    if (!file.library_id) return undefined;
    return libraries.all.find((l) => l.id === file.library_id);
  }

  /**
   * The library column's text, or "" when the row has no library. A scoped row
   * whose library is not in the store yet — the list is still loading, or the
   * library was deleted — still says so by id rather than reading as unscoped.
   */
  function libraryPill(file: UnmatchedFile): string {
    if (!file.library_id) return '';
    return libraryOf(file)?.name ?? `Library ${file.library_id}`;
  }

  /**
   * The park reason in the user's words. `manual-grab` is
   * library.ReasonManualGrab — an untied grab from the universal search, which
   * is not a failure at all: it is the outcome the user asked for, and reading
   * the raw token as one more scanner complaint would be wrong.
   */
  function reasonLabel(reason: string): string {
    return reason === 'manual-grab' ? 'Grabbed manually' : reason || UNKNOWN;
  }

  let queue = $derived(files ?? []);

  /**
   * A scan with no usable TMDB key still runs: it walks the disk, parses every
   * name and imports what it finds — it just cannot ask TMDB which title any of
   * it is, so everything lands here (PLAN phase 10 task 3). Without this banner
   * that reads as a broken scanner, which is the one thing it is not.
   */
  let credentialState = $derived(system.metadataCredential);
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <p class="text-base text-ink-secondary">
      Files the scanner could not confidently match. Resolve each one against TMDB.
    </p>
    <div class="ml-auto flex items-center gap-2">
      <Button variant="secondary" onclick={load}>
        <Icon name="refresh" size={14} />
        Refresh
      </Button>
      <Button variant="primary" onclick={rescan} disabled={scanning}>
        <Icon name="folder" size={14} />
        {scanning ? 'Scanning…' : 'Rescan library'}
      </Button>
    </div>
  </div>

  {#if credentialState !== 'ok'}
    <Banner
      tone="warning"
      icon="warning"
      title={credentialState === 'invalid'
        ? 'Nothing can be matched: TMDB rejected your API key'
        : 'Nothing can be matched: no TMDB API key'}
      message="The scan still ran — every file was found, parsed and imported — but naming a title takes TMDB, so everything it found is parked here. Add a working key and rescan to match them in one pass.">
      {#snippet action()}
        <Button variant="primary" href="/settings/metadata">Open metadata settings</Button>
      {/snippet}
    </Banner>
  {/if}

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && files === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 5 }) as _, i (i)}
        <Skeleton class="h-12 w-full rounded-md" />
      {/each}
    </div>
  {:else if queue.length === 0}
    <EmptyState
      icon="check"
      title="Nothing to review"
      message="Every file the scanner found was matched to a library item. Run a rescan after adding new media.">
      {#snippet action()}
        <Button variant="primary" onclick={rescan} disabled={scanning}>
          {scanning ? 'Scanning…' : 'Rescan library'}
        </Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="overflow-x-auto rounded-md border border-border">
      <table class="w-full min-w-[1000px] border-collapse text-sm">
        <thead>
          <tr class="bg-surface text-left">
            <th class="micro-label px-3 py-2 font-semibold">File</th>
            <th class="micro-label px-3 py-2 font-semibold">Parser guess</th>
            <th class="micro-label px-3 py-2 font-semibold">Confidence</th>
            <th class="micro-label px-3 py-2 font-semibold">Library</th>
            <th class="micro-label px-3 py-2 font-semibold">Reason</th>
            <th class="micro-label px-3 py-2 text-right font-semibold">Size</th>
            <th class="micro-label px-3 py-2 text-right font-semibold">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each queue as file (file.id)}
            {@const parsed = file.parsed}
            {@const libraryName = libraryPill(file)}
            <tr class="border-t border-border align-top transition-colors duration-150 hover:bg-raised">
              <td class="px-3 py-3 font-mono text-ink" title={file.path}>
                {truncateMiddle(file.path, 56)}
              </td>
              <td class="px-3 py-3">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="text-ink">{parsed.title || UNKNOWN}</span>
                  {#if parsed.year > 0}
                    <Badge mono>{parsed.year}</Badge>
                  {/if}
                  {#if (parsed.episodes?.length ?? 0) > 0}
                    <Badge mono tone="info">
                      {episodeCode(parsed.season, parsed.episodes[0] ?? 0)}{(parsed.episodes
                        ?.length ?? 0) > 1
                        ? `+${(parsed.episodes?.length ?? 1) - 1}`
                        : ''}
                    </Badge>
                  {/if}
                  {#if parsed.quality && parsed.quality !== 'unknown'}
                    <Badge mono>{parsed.quality}</Badge>
                  {/if}
                  {#if parsed.source && parsed.source !== 'unknown'}
                    <Badge mono>{parsed.source}</Badge>
                  {/if}
                  {#if parsed.group}
                    <Badge mono tone="neutral">{parsed.group}</Badge>
                  {/if}
                </div>
              </td>
              <td class="px-3 py-3">
                <Badge
                  mono
                  tone={parsed.confidence >= 0.7
                    ? 'success'
                    : parsed.confidence >= 0.4
                      ? 'warning'
                      : 'danger'}>
                  {formatConfidence(parsed.confidence)}
                </Badge>
              </td>
              <td class="px-3 py-3">
                <!-- Only when there is one. A scan-parked file has no library
                     yet — that is what the match decides — and an em dash says
                     "not yet" where a blank cell would just look broken. -->
                {#if libraryName}
                  <Badge tone="info" title="This file is scoped to one library">
                    {libraryName}
                  </Badge>
                {:else}
                  <span class="text-ink-muted">{UNKNOWN}</span>
                {/if}
              </td>
              <td class="px-3 py-3 text-ink-secondary">{reasonLabel(file.reason)}</td>
              <td class="px-3 py-3 text-right font-mono text-ink-secondary">
                {formatBytes(file.size)}
              </td>
              <td class="px-3 py-3">
                <div class="flex justify-end gap-2">
                  <Button
                    variant="primary"
                    size="sm"
                    disabled={busyID === file.id}
                    onclick={() => (matching = file)}>
                    Match
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busyID === file.id}
                    onclick={() => dismiss(file)}>
                    Ignore
                  </Button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

{#if matching}
  <AddItemModal
    title="Match “{matching.path}”"
    kind={guessKind(matching)}
    initialQuery={matching.parsed.title}
    onpick={confirmMatch}
    onclose={() => (matching = null)} />
{/if}
