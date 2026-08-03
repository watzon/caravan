<script lang="ts">
  /**
   * Login (SPEC §11). Rendered without the shell, like first run: there is
   * nothing to navigate to until the session exists.
   *
   * It appears only once the server has answered 401, so reaching it means the
   * server has accounts - there is no "is auth on?" probe to get wrong.
   */
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import TextInput from '../components/TextInput.svelte';
  import { auth } from '../state/auth.svelte';
  import { session } from '../state/session.svelte';
  import { system } from '../state/system.svelte';

  let username = $state('');
  let password = $state('');

  async function submit() {
    if (username.trim() === '') {
      auth.error = 'Enter your username.';
      return;
    }
    if (password === '') {
      auth.error = 'Enter your password.';
      return;
    }
    if (await auth.login(username.trim(), password)) {
      password = '';
      // The shell mounts against fetches that 401'd. The role comes first:
      // it decides which navigation renders, and a member must never see the
      // admin one flash. System status is admin-only, so it is only asked for
      // when the account that just signed in may have it.
      await session.refresh();
      if (session.isAdmin) await system.refresh();
    }
  }
</script>

<div class="flex min-h-full items-center justify-center bg-bg p-6">
  <div class="w-full max-w-sm rounded-lg border border-border bg-surface p-8">
    <div class="mb-6 flex items-center gap-3">
      <span
        class="flex size-8 items-center justify-center rounded-md bg-accent text-ink-inverse"
        aria-hidden="true">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M3 17h18" />
          <path d="M5 17V9l4-4h7a3 3 0 0 1 3 3v9" />
          <circle cx="8" cy="19" r="2" />
          <circle cx="17" cy="19" r="2" />
        </svg>
      </span>
      <span class="font-display text-lg font-bold tracking-tight text-ink">CARAVAN</span>
    </div>

    <h1 class="font-display text-xl font-semibold tracking-tight text-ink">Sign in</h1>
    <p class="mt-2 text-base text-ink-secondary">
      This Caravan is password-protected. Sign in with your account.
    </p>

    <form
      class="mt-6 flex flex-col gap-6"
      onsubmit={(event) => {
        event.preventDefault();
        submit();
      }}>
      <Field label="Username" for="login-username">
        <TextInput id="login-username" bind:value={username} autofocus placeholder="admin" />
      </Field>

      <Field label="Password" for="login-password">
        <TextInput
          id="login-password"
          bind:value={password}
          type="password"
          placeholder="•••••" />
      </Field>

      {#if auth.error}
        <LoadError message={auth.error} />
      {/if}

      <Button variant="primary" type="submit" disabled={auth.busy} class="self-start">
        <Icon name="check" size={14} />
        {auth.busy ? 'Signing in…' : 'Sign in'}
      </Button>
    </form>
  </div>
</div>
