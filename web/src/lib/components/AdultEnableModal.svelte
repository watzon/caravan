<script lang="ts">
  /**
   * Enabling adult content (PLAN phase 10 task 5, Paper "Adult enable modal").
   *
   * Turning the module on is not a toggle: it needs a stash-box credential that
   * works, and it changes what this Caravan exposes. So the switch opens this
   * instead, and the modal runs the decision in two beats.
   *
   *  1. The credential — endpoint (ThePornDB preset, or any stash-box) and API
   *     key.
   *  2. The confirmation — the three exposure defaults restated (DLNA off,
   *     prepared drives excluded, members granted one by one), and only from
   *     here can the module be switched on.
   *
   * The enable is one POST that validates the credential against the provider
   * BEFORE it writes anything and commits `adult_enabled` last. That is why
   * there is no separate probe button: a passing test followed by a failing
   * write is not a state this can reach, because the thing that proves the
   * credential is the same call that commits it. A credential the provider
   * refuses leaves the endpoint, the key and the switch byte-identical, and
   * lands the user back on step 1 with the provider's own words under the field
   * that was wrong.
   *
   * Cancel writes nothing at all, from either step: there is no partial state
   * to unwind because there was never a partial write.
   */
  import { untrack } from 'svelte';
  import { api, errorText } from '../api/client';
  import { STASHBOX_TPDB_ENDPOINT } from '../api/types';
  import { adultFault } from '../credentials';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Modal from './Modal.svelte';
  import TextInput from './TextInput.svelte';

  const SELECT_CLASS =
    'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none disabled:opacity-50';

  interface Props {
    /** Seeds from the stored settings, so re-enabling a module that was
     * configured once and switched off does not ask for it all again. */
    initialEndpoint?: string;
    initialApiKey?: string;
    onclose: () => void;
    /**
     * Called once `adult_enabled` is actually on, with the credential the
     * enable committed — the card behind this modal re-seeds its own fields
     * from it rather than refetching settings it was handed as a prop.
     */
    onenabled: (committed: { endpoint: string; apiKey: string }) => void;
  }

  let { initialEndpoint = '', initialApiKey = '', onclose, onenabled }: Props = $props();

  // The three fields seed from the props exactly once: this modal is mounted
  // per use, so reading them untracked is the intent, not an oversight — a
  // re-seed mid-edit would overwrite a half-typed key.
  const seedEndpoint = untrack(() => initialEndpoint.trim());

  /** '' is stored as "use the preset", which is what the server resolves it to. */
  let preset = $state(seedEndpoint === '' || seedEndpoint === STASHBOX_TPDB_ENDPOINT);
  let endpoint = $state(seedEndpoint === '' ? STASHBOX_TPDB_ENDPOINT : seedEndpoint);
  let apiKey = $state(untrack(() => initialApiKey));

  let step = $state<'credential' | 'confirm'>('credential');
  let enabling = $state(false);
  let error = $state<string | null>(null);

  /** The endpoint as it goes over the wire: blank means "the server's default". */
  let wireEndpoint = $derived(preset ? '' : endpoint.trim());
  let wireKey = $derived(apiKey.trim());

  function toConfirm() {
    if (wireKey === '') {
      error = 'Enter the stash-box API key this Caravan should use.';
      return;
    }
    error = null;
    step = 'confirm';
  }

  function toCredential() {
    error = null;
    step = 'credential';
  }

  async function enable() {
    enabling = true;
    error = null;
    try {
      await api.setAdultEnabled(true, { endpoint: wireEndpoint, apiKey: wireKey });
      onenabled({ endpoint: wireEndpoint, apiKey: wireKey });
    } catch (err) {
      error = errorText(err);
      // A credential the provider refused is a field problem, so it is shown
      // on the fields. Anything else — a store write that failed, a server
      // that went away — is not, and stays where it happened.
      if (adultFault(err)) step = 'credential';
    } finally {
      enabling = false;
    }
  }
</script>

<Modal title="Enable adult content" width="max-w-xl" {onclose}>
  <div class="flex flex-col gap-4 p-4">
    {#if step === 'credential'}
      <p class="text-base text-ink-secondary">
        Adult metadata needs its own source — TMDB supplies none of it. Caravan speaks the stash-box
        protocol, so a ThePornDB account is the default and any stash-box endpoint works. The
        credential is checked against the provider before anything is switched on.
      </p>

      <Field
        label="Endpoint"
        for="adult-enable-endpoint">
        <select
          id="adult-enable-endpoint"
          aria-describedby="adult-enable-endpoint-help"
          value={preset ? 'tpdb' : 'custom'}
          onchange={(event) => (preset = event.currentTarget.value === 'tpdb')}
          class={SELECT_CLASS}>
          <option value="tpdb">ThePornDB — {STASHBOX_TPDB_ENDPOINT}</option>
          <option value="custom">Custom stash-box…</option>
        </select>
        <p id="adult-enable-endpoint-help" class="text-sm text-ink-secondary">
          The provider's GraphQL endpoint. The preset follows Caravan's default; pick Custom to point at your own stash-box.
        </p>
      </Field>

      {#if !preset}
        <Field
          label="Custom endpoint"
          for="adult-enable-endpoint-url"
          help="An absolute http(s) URL. Anything else is refused rather than silently ignored.">
          <TextInput
            id="adult-enable-endpoint-url"
            bind:value={endpoint}
            mono
            placeholder={STASHBOX_TPDB_ENDPOINT} />
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
    {:else}
      <p class="text-base text-ink-secondary">
        Caravan will check this credential against the provider and, if it answers, turn the module
        on. Nothing else about what this Caravan exposes changes — here is what stays where it is:
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
    {/if}
  </div>

  {#snippet footer()}
    {#if step === 'confirm'}
      <Button variant="ghost" disabled={enabling} onclick={toCredential}>Back</Button>
    {/if}
    <span class="flex-1"></span>
    <Button variant="ghost" disabled={enabling} onclick={onclose}>Cancel</Button>
    {#if step === 'credential'}
      <Button variant="primary" onclick={toConfirm}>Continue</Button>
    {:else}
      <Button variant="primary" disabled={enabling} onclick={enable}>
        {enabling ? 'Checking the credential…' : 'Enable adult content'}
      </Button>
    {/if}
  {/snippet}
</Modal>
