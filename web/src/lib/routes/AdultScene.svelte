<script lang="ts">
  /**
   * One provider-backed scene. Unlike an imported episode, this is identified by
   * the provider instance and its opaque stash id, so it remains addressable
   * before Caravan owns the site or any local episode exists.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { SceneMeta } from '../api/types';
  import { sceneYear } from '../adult';
  import { durationBadge } from '../explore';
  import { UNKNOWN, formatDate } from '../format';
  import type { MetadataLink } from '../metadataLinks';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import MetadataLinks from '../components/MetadataLinks.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { useI18n } from '../i18n.svelte';

  interface Props {
    provider: string;
    stashID: string;
  }

  let { provider, stashID }: Props = $props();
  const { t } = useI18n();

  let scene = $state<SceneMeta | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let requesting = $state(false);
  let controller: AbortController | null = null;
  let duration = $derived(durationBadge(scene?.duration ?? 0));
  let providerLinks = $derived<MetadataLink[]>(
    scene?.url ? [{ label: scene.provider, href: scene.url }] : [],
  );


  async function load() {
    controller?.abort();
    const nextController = new AbortController();
    controller = nextController;
    loading = true;
    try {
      scene = await api.adultScene(provider, stashID, nextController.signal);
      error = null;
    } catch (err) {
      if (nextController.signal.aborted) return;
      error = errorText(err);
    } finally {
      if (!nextController.signal.aborted) loading = false;
    }
  }

  async function request() {
    const current = scene;
    if (!current || current.in_library || current.requested) return;
    requesting = true;
    try {
      await api.createRequest({
        media_type: 'scene',
        tmdb_id: 0,
        stash_id: current.stash_id,
        title: current.title,
        year: sceneYear(current.date),
        poster_path: current.image_url,
        provider: current.provider,
      });
      // One flag changed; retain the provider response rather than rereading it.
      scene = { ...current, requested: true };
      pushToast(t('route.adultScene.requested', { title: current.title }), 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      requesting = false;
    }
  }

  onMount(() => {
    void load();
    return () => controller?.abort();
  });
</script>

<div class="flex flex-col gap-6">
  <a
    href="/discover/adult"
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    {t('route.adultScene.back')}
  </a>

  {#if error}
    <LoadError message={error} onretry={() => void load()} />
  {:else if loading && scene === null}
    <div class="flex flex-col gap-6 md:flex-row">
      <Skeleton class="aspect-video w-full rounded-md md:w-96" />
      <div class="flex min-w-0 flex-1 flex-col gap-3">
        <Skeleton class="h-8 w-1/2" />
        <Skeleton class="h-4 w-1/3" />
        <Skeleton class="h-20 w-full" />
      </div>
    </div>
  {:else if scene}
    {@const current = scene}
    <div class="flex flex-col gap-6 md:flex-row">
      <div class="w-full shrink-0 md:w-96">
        <Poster path={current.image_url} alt={current.title} fallbackIcon="flame" aspect="video" />
      </div>

      <div class="flex min-w-0 flex-1 flex-col gap-4">
        <div class="flex flex-wrap items-start gap-4">
          <div class="min-w-0 flex-1">
            <h2 class="font-display text-2xl font-semibold tracking-tight text-ink">{current.title}</h2>
            <div class="mt-2 flex flex-wrap items-center gap-2">
              {#if current.in_library}
                <Badge tone="success">
                  <span class="inline-flex items-center gap-1"><Icon name="check" size={10} />{t('component.sceneCard.inLibrary')}</span>
                </Badge>
              {:else if current.requested}
                <Badge tone="warning">
                  <span class="inline-flex items-center gap-1"><Icon name="clock" size={10} />{t('component.sceneCard.requested')}</span>
                </Badge>
              {/if}
              <MetadataLinks links={providerLinks} />
            </div>
          </div>

          {#if !current.in_library && !current.requested}
            <Button variant="primary" disabled={requesting} onclick={request}>
              {requesting ? t('route.adultScene.requesting') : t('route.adultScene.request')}
            </Button>
          {/if}
        </div>

        <p class="max-w-3xl text-md text-ink-secondary">
          {current.overview || t('route.adultScene.noOverview')}
        </p>

        <dl class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div class="min-w-0">
            <dt class="micro-label">{t('route.adultScene.site')}</dt>
            <dd class="mt-1 truncate text-sm text-ink" title={current.site_name}>
              {current.site_name || UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">{t('route.adultScene.released')}</dt>
            <dd class="mt-1 text-sm text-ink">{formatDate(current.date)}</dd>
          </div>
          <div>
            <dt class="micro-label">{t('route.adultScene.duration')}</dt>
            <dd class="mt-1 font-mono text-sm text-ink">{duration || UNKNOWN}</dd>
          </div>
          <div class="min-w-0">
            <dt class="micro-label">{t('route.adultScene.code')}</dt>
            <dd class="mt-1 truncate font-mono text-sm text-ink" title={current.code}>
              {current.code || UNKNOWN}
            </dd>
          </div>
        </dl>

        <section aria-labelledby="scene-performers-heading">
          <h3 id="scene-performers-heading" class="micro-label">{t('route.adultScene.performers')}</h3>
          {#if current.performers.length > 0}
            <p class="mt-1 text-sm text-ink">{current.performers.join(', ')}</p>
          {:else}
            <p class="mt-1 text-sm text-ink-secondary">{t('route.adultScene.noPerformers')}</p>
          {/if}
        </section>
      </div>
    </div>
  {/if}
</div>
