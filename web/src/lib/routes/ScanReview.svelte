<script lang="ts">
  /**
   * Scan review (SPEC §10.1 step 3, §13): everything the scanner could not
   * confidently match parks here with the parser's best guess, and the user
   * resolves it by hand. Nothing is ever dropped silently.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { MovieMeta, SceneMeta, SeriesMeta, UnmatchedFile } from '../api/types';
  import { unmatchedMatchStrategy, type UnmatchedMatchStrategy } from '../match';
  import AddItemModal from '../components/AddItemModal.svelte';
  import ScenePickerModal from '../components/ScenePickerModal.svelte';
  import Badge from '../components/Badge.svelte';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import MiddleEllipsis from '../components/MiddleEllipsis.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import {
    UNKNOWN,
    episodeCode,
    formatBytes,
    formatConfidence,
  } from '../format';
  import { libraries } from '../state/libraries.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { libraryChanged, scanReviewResolved } from '../state/activity';
  import { system } from '../state/system.svelte';
  import { useI18n } from '../i18n.svelte';

  const { t } = useI18n();

  let files = $state<UnmatchedFile[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let scanning = $state(false);
  let busyID = $state<number | null>(null);
  let matching = $state<UnmatchedFile | null>(null);
  let matchStrategy = $state<UnmatchedMatchStrategy | null>(null);

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
      libraryChanged();
      pushToast(
        t('route.scanReview.scanFinished', {
          files: summary.media_files,
          unmatched: summary.unmatched,
        }),
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
      scanReviewResolved();
      pushToast(t('route.scanReview.removed'), 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  async function confirmMatch(kind: 'movie' | 'series', row: MovieMeta | SeriesMeta) {
    const file = matching;
    if (!file) return;
    try {
      // The ref pair travels beside tmdb_id, not instead of it: the pair is the
      // only thing that names a hit from a provider other than TMDB (which
      // carries tmdb_id 0), and the server lets it win where both are present.
      // Half a pair is refused, so an old stub with no ref sends neither.
      await api.matchUnmatched(file.id, {
        type: kind,
        tmdb_id: row.tmdb_id,
        ...(row.provider && row.provider_ref
          ? { provider: row.provider, provider_ref: row.provider_ref }
          : {}),
      });
      files = (files ?? []).filter((f) => f.id !== file.id);
      scanReviewResolved();
      closeMatch();
      pushToast(t('route.scanReview.matched'), 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    }
  }

  async function confirmSceneMatch(scene: SceneMeta) {
    const file = matching;
    if (!file) return;
    try {
      await api.matchUnmatched(file.id, {
        type: 'scene',
        provider: scene.provider,
        provider_ref: scene.stash_id,
      });
      files = (files ?? []).filter((f) => f.id !== file.id);
      scanReviewResolved();
      closeMatch();
      pushToast(t('route.scanReview.matched'), 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    }
  }

  function libraryOf(file: UnmatchedFile) {
    if (!file.library_id) return undefined;
    return libraries.all.find((library) => library.id === file.library_id);
  }

  function openMatch(file: UnmatchedFile) {
    const strategy = unmatchedMatchStrategy(file, libraries.all);
    if (!strategy) {
      pushToast(t('route.scanReview.libraryUnavailable'), 'danger');
      return;
    }
    matching = file;
    matchStrategy = strategy;
  }

  function closeMatch() {
    matching = null;
    matchStrategy = null;
  }

  /**
   * The library column's text, or "" when the row has no library. A scoped row
   * whose library is not in the store yet — the list is still loading, or the
   * library was deleted — still says so by id rather than reading as unscoped.
   */
  function libraryPill(file: UnmatchedFile): string {
    if (!file.library_id) return '';
    return libraryOf(file)?.name ?? t('route.scanReview.library', { id: file.library_id });
  }

  /**
   * The park reason in the user's words. `manual-grab` is
   * library.ReasonManualGrab — an untied grab from the universal search, which
   * is not a failure at all: it is the outcome the user asked for, and reading
   * the raw token as one more scanner complaint would be wrong.
   */
  function reasonLabel(reason: string): string {
    return reason === 'manual-grab' ? t('route.scanReview.grabbedManually') : reason || UNKNOWN;
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
    <p class="text-base text-ink-secondary">{t('route.scanReview.description')}</p>
    <div class="ml-auto flex items-center gap-2">
      <Button variant="secondary" onclick={load}>
        <Icon name="refresh" size={14} />
        {t('route.scanReview.refresh')}
      </Button>
      <Button variant="primary" onclick={rescan} disabled={scanning}>
        <Icon name="folder" size={14} />
        {scanning ? t('route.scanReview.scanning') : t('route.scanReview.rescan')}
      </Button>
    </div>
  </div>

  {#if credentialState !== 'ok'}
    <Banner
      tone="warning"
      icon="warning"
      title={credentialState === 'invalid'
        ? t('route.scanReview.invalidKeyTitle')
        : t('route.scanReview.missingKeyTitle')}
      message={t('route.scanReview.keyMessage')}>
      {#snippet action()}
        <Button variant="primary" href="/settings/metadata">{t('route.scanReview.openMetadata')}</Button>
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
      title={t('route.scanReview.emptyTitle')}
      message={t('route.scanReview.emptyMessage')}>
      {#snippet action()}
        <Button variant="primary" onclick={rescan} disabled={scanning}>
          {scanning ? t('route.scanReview.scanning') : t('route.scanReview.rescan')}
        </Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="overflow-x-auto rounded-md border border-border">
      <table class="w-full min-w-[1000px] border-collapse text-sm">
        <thead>
          <tr class="bg-surface text-left">
            <th class="micro-label px-3 py-2 font-semibold">{t('route.scanReview.file')}</th>
            <th class="micro-label px-3 py-2 font-semibold">{t('route.scanReview.parserGuess')}</th>
            <th class="micro-label px-3 py-2 font-semibold">{t('route.scanReview.confidence')}</th>
            <th class="micro-label px-3 py-2 font-semibold">{t('route.scanReview.libraryHeader')}</th>
            <th class="micro-label px-3 py-2 font-semibold">{t('route.scanReview.reason')}</th>
            <th class="micro-label px-3 py-2 text-right font-semibold">{t('route.scanReview.size')}</th>
            <th class="micro-label px-3 py-2 text-right font-semibold">{t('route.scanReview.actions')}</th>
          </tr>
        </thead>
        <tbody>
          {#each queue as file (file.id)}
            {@const parsed = file.parsed}
            {@const libraryName = libraryPill(file)}
            <tr class="border-t border-border align-top transition-colors duration-150 hover:bg-raised">
              <td class="w-full max-w-0 px-3 py-3 font-mono text-ink" title={file.path}>
                <MiddleEllipsis text={file.path} />
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
                  <Badge tone="info" title={t('route.scanReview.scopedLibrary')}>
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
                    onclick={() => openMatch(file)}>
                    {t('route.scanReview.match')}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busyID === file.id}
                    onclick={() => dismiss(file)}>
                    {t('route.scanReview.ignore')}
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

{#if matching && matchStrategy}
  {#if matchStrategy.kind === 'scene'}
    <ScenePickerModal
      title={t('route.scanReview.matchTitle', { path: matching.path })}
      siteQuery={matchStrategy.siteQuery}
      sceneDate={matchStrategy.sceneDate}
      fallbackQuery={matchStrategy.fallbackQuery}
      providers={matchStrategy.providers}
      onpick={confirmSceneMatch}
      onclose={closeMatch} />
  {:else}
    <AddItemModal
      title={t('route.scanReview.matchTitle', { path: matching.path })}
      kind={matchStrategy.mediaType}
      initialQuery={matchStrategy.query}
      libraryID={matchStrategy.libraryID}
      onpick={confirmMatch}
      onclose={closeMatch} />
  {/if}
{/if}
