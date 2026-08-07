<script lang="ts">
  /**
   * Enabling adult content (PLAN Part 2 phase 8, was phase 10 task 5).
   *
   * Turning the module on is not a toggle: it changes what this Caravan
   * exposes, and it cannot run without a stash-box endpoint to ask. So the
   * switch opens this instead, and the modal runs the decision in two beats.
   *
   *  1. The confirmation — the three exposure defaults restated (DLNA off,
   *     prepared drives excluded, members granted one by one), and only from
   *     here is the module switched on.
   *  2. The first stash-box, but only when the server says one is needed.
   *
   * That second beat is a FALLBACK rather than a first step, and the reason is
   * a wall this modal cannot see over: every instance route lives under the
   * adult mux and answers 404 while the module is off, so the client cannot ask
   * whether any endpoint is configured before enabling. Guessing either way
   * would be wrong for half of all users — a fresh install has none, and a
   * re-enable after a switch-off has all of them, since a disable deletes
   * nothing. So the modal asks the only party that knows: it posts a bare
   * enable, and reveals the form only when the server answers
   * `adult_credential_absent`.
   *
   * The enable is one POST that validates the endpoint against the provider
   * BEFORE it writes anything and commits `adult_enabled` last. That is why
   * there is no separate probe button: a passing test followed by a failing
   * write is not a state this can reach. A credential the box refuses leaves
   * the instance table and the switch byte-identical, and lands the user back
   * on the form with the box's own words under it.
   *
   * Cancel writes nothing at all, from either step.
   */
  import { api, errorText } from '../api/client';
  import { STASHBOX_PRESETS } from '../api/types';
  import { adultFault } from '../credentials';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Modal from './Modal.svelte';
  import TextInput from './TextInput.svelte';

  const SELECT_CLASS =
    'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none disabled:opacity-50';

  interface Props {
    onclose: () => void;
    /**
     * Called once `adult_enabled` is actually on. It carries nothing: the
     * instance the enable may have created is read back from
     * GET /adult/stashbox-instances by the card that owns it, which is now the
     * only place a stash-box endpoint is edited.
     */
    onenabled: () => void;
  }

  let { onclose, onenabled }: Props = $props();

  let step = $state<'confirm' | 'instance'>('confirm');
  let enabling = $state(false);
  let error = $state<string | null>(null);

  /** '' is Custom: the preset only fills the fields, it does not bind them. */
  let presetID = $state(STASHBOX_PRESETS[0]?.id ?? '');
  let name = $state(STASHBOX_PRESETS[0]?.label ?? '');
  let endpoint = $state(STASHBOX_PRESETS[0]?.endpoint ?? '');
  let apiKey = $state('');

  function pickPreset(id: string) {
    presetID = id;
    const preset = STASHBOX_PRESETS.find((p) => p.id === id);
    if (!preset) return;
    name = preset.label;
    endpoint = preset.endpoint;
  }

  let instanceError = $derived.by(() => {
    if (name.trim() === '') return 'Give this stash-box a name.';
    if (endpoint.trim() === '') return 'Enter the stash-box GraphQL endpoint.';
    return null;
  });

  async function enable() {
    if (step === 'instance' && instanceError) {
      error = instanceError;
      return;
    }
    enabling = true;
    error = null;
    try {
      await api.setAdultEnabled(
        true,
        step === 'instance'
          ? { name: name.trim(), endpoint: endpoint.trim(), api_key: apiKey.trim() }
          : undefined,
      );
      onenabled();
    } catch (err) {
      error = errorText(err);
      // "Nothing configured" is the one failure this modal can answer, and the
      // form is the answer. Anything else — a box that refused the key, a store
      // write that failed — is reported where it happened.
      if (adultFault(err) === 'absent') step = 'instance';
    } finally {
      enabling = false;
    }
  }
</script>

<Modal title="Enable adult content" width="max-w-xl" {onclose}>
  <div class="flex flex-col gap-4 p-4">
    {#if step === 'confirm'}
      <p class="text-base text-ink-secondary">
        Caravan will turn the module on. Nothing else about what this Caravan exposes changes — here
        is what stays where it is:
      </p>

      <ul class="flex flex-col gap-2.5 rounded-md border border-border bg-raised p-3">
        <li class="flex items-start gap-2">
          <span class="mt-2 size-1.5 shrink-0 rounded-full bg-ink-muted"></span>
          <span class="text-base text-ink-secondary">
            <span class="font-medium text-ink">DLNA stays off</span> for the Adult library. Sharing
            it is its own toggle on the Playback screen, behind a warning, because DLNA has no
            accounts.
          </span>
        </li>
        <li class="flex items-start gap-2">
          <span class="mt-2 size-1.5 shrink-0 rounded-full bg-ink-muted"></span>
          <span class="text-base text-ink-secondary">
            <span class="font-medium text-ink">Prepared drives leave it out.</span>
            <code class="font-mono text-sm">caravan prepare</code> includes it only when told to with
            <code class="font-mono text-sm">--include-adult</code>.
          </span>
        </li>
        <li class="flex items-start gap-2">
          <span class="mt-2 size-1.5 shrink-0 rounded-full bg-ink-muted"></span>
          <span class="text-base text-ink-secondary">
            <span class="font-medium text-ink">Members see nothing until granted</span>, one by one,
            on the access list that appears once this is on. Admins always reach it.
          </span>
        </li>
      </ul>

      {#if error}
        <p class="text-sm text-danger" role="alert">{error}</p>
      {/if}
    {:else}
      <p class="text-base text-ink-secondary">
        This Caravan has no stash-box endpoint yet, and adult metadata needs its own source — TMDB
        supplies none of it. Caravan speaks the stash-box protocol, so any of these boxes works, and
        more can be added later on Settings → Metadata. The credential is checked against the box
        before anything is switched on.
      </p>

      <Field label="Stash-box" for="adult-enable-preset">
        <select
          id="adult-enable-preset"
          aria-describedby="adult-enable-preset-help"
          value={presetID}
          onchange={(event) => pickPreset(event.currentTarget.value)}
          class={SELECT_CLASS}>
          {#each STASHBOX_PRESETS as preset (preset.id)}
            <option value={preset.id}>{preset.label} — {preset.endpoint}</option>
          {/each}
          <option value="">Custom stash-box…</option>
        </select>
        <p id="adult-enable-preset-help" class="text-sm text-ink-secondary">
          The box's GraphQL endpoint. Pick Custom to point at your own stash-box.
        </p>
      </Field>

      <Field
        label="Name"
        for="adult-enable-name"
        help="How this box is labelled in a library's provider chain.">
        <TextInput id="adult-enable-name" bind:value={name} placeholder="StashDB" />
      </Field>

      {#if presetID === ''}
        <Field
          label="Custom endpoint"
          for="adult-enable-endpoint-url"
          help="An absolute http(s) URL. Anything else is refused rather than silently ignored.">
          <TextInput
            id="adult-enable-endpoint-url"
            bind:value={endpoint}
            mono
            placeholder="https://stashdb.org/graphql" />
        </Field>
      {/if}

      <Field
        label="API key"
        for="adult-enable-api-key"
        {error}
        help="Stored in the database, never in caravan.yaml or logs.">
        <TextInput
          id="adult-enable-api-key"
          bind:value={apiKey}
          type="password"
          mono
          placeholder="•••••" />
      </Field>
    {/if}
  </div>

  {#snippet footer()}
    <span class="flex-1"></span>
    <Button variant="ghost" disabled={enabling} onclick={onclose}>Cancel</Button>
    <Button variant="primary" disabled={enabling} onclick={enable}>
      {enabling ? 'Checking the credential…' : 'Enable adult content'}
    </Button>
  {/snippet}
</Modal>
