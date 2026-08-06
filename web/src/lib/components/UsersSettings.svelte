<script lang="ts">
  /**
   * Settings → Users (SPEC §11 `/users`): the accounts that can sign in, and
   * what each of them may do.
   *
   * On a Caravan with no accounts at all the API is open and everybody who can
   * reach it is an admin, exactly as a passwordless server always behaved. That
   * makes the first account created here the thing that closes the server — it
   * is where the old "set a password" flow went, one door further along.
   *
   * A password is only ever sent, never received: the argon2id hash is the one
   * column that does not leave the server (SPEC §12), so there is no "change"
   * field to prefill and every write here is a new password or none at all.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import { MIN_PASSWORD_LENGTH, type User, type UserRole } from '../api/types';
  import { pushToast } from '../state/toast.svelte';
  import { session } from '../state/session.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';

  const SELECT_CLASS =
    'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none disabled:opacity-50';

  /** One dialog at a time, so the three of them share one Modal. */
  type Dialog =
    | { kind: 'create' }
    | { kind: 'password'; user: User }
    | { kind: 'delete'; user: User };

  let users = $state<User[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let dialog = $state<Dialog | null>(null);
  let busy = $state(false);
  /** Why the open dialog's last submit failed. Shown in the dialog, not a toast. */
  let dialogError = $state<string | null>(null);

  let username = $state('');
  let password = $state('');
  let role = $state<UserRole>('member');

  async function load() {
    loading = true;
    try {
      users = await api.listUsers();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function open(next: Dialog) {
    dialog = next;
    dialogError = null;
    username = '';
    password = '';
    // With no admin yet, Member is the one choice the server refuses: the
    // account being created is what closes an open Caravan, and a gated
    // Caravan with no admin can never be administered again. Offering it as
    // the default would make the empty state's own "make yourself an admin"
    // the thing that has to be corrected.
    role = admins === 0 ? 'admin' : 'member';
  }

  function close() {
    dialog = null;
    dialogError = null;
    password = '';
  }

  let rows = $derived(users ?? []);
  let admins = $derived(rows.filter((u) => u.role === 'admin').length);

  /** Mirrors the server's delete rule so the click is never a dead end. */
  function undeletable(user: User): boolean {
    return user.role === 'admin' && admins <= 1 && rows.length > 1;
  }

  /**
   * Whether the open dialog's primary action can fire. A username with a space
   * on either end is refused by the server rather than trimmed — " chris" and
   * "chris" would otherwise be one account under a name only one of them can
   * type — so it is refused here too, with the same reason.
   */
  let canSubmit = $derived.by(() => {
    if (busy || dialog === null) return false;
    if (dialog.kind === 'delete') return true;
    if (password.length < MIN_PASSWORD_LENGTH) return false;
    if (dialog.kind === 'password') return true;
    return username !== '' && username.trim() === username;
  });

  async function create() {
    busy = true;
    dialogError = null;
    try {
      const created = await api.createUser({ username, password, role });
      users = [...rows, created];
      pushToast(`Added ${created.username}.`, 'success');
      // The first account is what closes an open server, so this browser's own
      // identity just changed from "implicit admin" to a real one.
      await session.refresh();
      close();
    } catch (err) {
      dialogError = errorText(err);
    } finally {
      busy = false;
    }
  }

  async function resetPassword(user: User) {
    busy = true;
    dialogError = null;
    try {
      await api.resetUserPassword(user.id, password);
      pushToast(`Reset ${user.username}'s password. Their other browsers are signed out.`, 'success');
      close();
    } catch (err) {
      dialogError = errorText(err);
    } finally {
      busy = false;
    }
  }

  async function remove(user: User) {
    busy = true;
    dialogError = null;
    try {
      await api.deleteUser(user.id);
      users = rows.filter((u) => u.id !== user.id);
      pushToast(`Deleted ${user.username}.`, 'neutral');
      close();
    } catch (err) {
      // 409 is the last-admin refusal, and the server's sentence explains it
      // better than a generic one would. It belongs beside the button that
      // caused it rather than in a toast that outlives the dialog.
      dialogError = errorText(err);
    } finally {
      busy = false;
    }
  }

  function submit() {
    if (!canSubmit || dialog === null) return;
    if (dialog.kind === 'create') void create();
    else if (dialog.kind === 'password') void resetPassword(dialog.user);
    else void remove(dialog.user);
  }
</script>

<SettingsCard
  title="Users"
  description="Who can sign in. Admins run Caravan; members can only browse Discover and ask for things.">
  {#snippet action()}
    <Button variant="primary" size="sm" onclick={() => open({ kind: 'create' })}>
      <Icon name="plus" size={14} />
      Add user
    </Button>
  {/snippet}

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && users === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 2 }) as _, i (i)}
        <Skeleton class="h-12 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="inbox"
      title="No accounts yet"
      message="This Caravan is open: anyone who can reach it can use and configure it. Adding the first account is what puts a login in front of it — make yourself an admin.">
      {#snippet action()}
        <Button variant="primary" onclick={() => open({ kind: 'create' })}>Add user</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as user (user.id)}
        <li class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2">
              <span class="truncate text-base font-medium text-ink" title={user.username}>{user.username}</span>
              <Badge mono tone={user.role === 'admin' ? 'accent' : 'neutral'}>
                {user.role === 'admin' ? 'ADMIN' : 'MEMBER'}
              </Badge>
              {#if user.username === session.username}
                <Badge tone="info">You</Badge>
              {/if}
            </p>
          </div>

          <div class="flex w-full shrink-0 items-center justify-end gap-2 sm:w-auto">
            <Button
              variant="ghost"
              size="sm"
              onclick={() => open({ kind: 'password', user })}>
              Reset password
            </Button>
            <!-- The last admin cannot go while anyone else has an account: a
                 Caravan with members and no admin can never be administered
                 again. The one exception is the final account of all, whose
                 deletion is how the server is reopened. The server enforces
                 both — this only stops the click from being a dead end. -->
            <Button
              variant="ghost"
              size="sm"
              disabled={undeletable(user)}
              title={undeletable(user)
                ? 'The only admin cannot be deleted while other accounts exist'
                : `Delete ${user.username}`}
              onclick={() => open({ kind: 'delete', user })}>
              <Icon name="trash" size={14} />
              <span class="sr-only">Delete {user.username}</span>
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</SettingsCard>

{#if dialog}
  {@const current = dialog}
  <Modal
    title={current.kind === 'create'
      ? 'Add user'
      : current.kind === 'password'
        ? `Reset ${current.user.username}'s password`
        : `Delete ${current.user.username}`}
    width="max-w-lg"
    onclose={close}>
    <form
      class="flex flex-col gap-4 p-4"
      onsubmit={(event) => {
        event.preventDefault();
        submit();
      }}>
      {#if current.kind === 'delete'}
        <p class="text-base text-ink-secondary">
          {current.user.username} can no longer sign in, and any browser signed in as them is
          turned out immediately. What they already asked for stays on the requests screen —
          deleting somebody does not delete the history of what they wanted.
        </p>
      {:else}
        {#if current.kind === 'create'}
          <Field
            label="Username"
            for="user-username"
            help="Case-insensitive at sign-in. No space at either end — a name nobody can retype is a lockout.">
            <TextInput id="user-username" bind:value={username} autofocus placeholder="chris" />
          </Field>
        {/if}

        <Field
          label={current.kind === 'create' ? 'Password' : 'New password'}
          for="user-password"
          help="At least {MIN_PASSWORD_LENGTH} characters. Stored as an argon2id hash and never returned by the API.">
          <TextInput
            id="user-password"
            bind:value={password}
            type="password"
            autofocus={current.kind === 'password'}
            placeholder="•••••" />
        </Field>

        {#if current.kind === 'create'}
          <Field
            label="Role"
            for="user-role"
            help="An admin can do everything on this screen. A member gets Discover and Requests, and nothing else.">
            <select id="user-role" bind:value={role} class={SELECT_CLASS}>
              <option value="member">Member</option>
              <option value="admin">Admin</option>
            </select>
          </Field>
        {:else}
          <p class="text-sm text-ink-secondary">
            {current.user.username} is signed out of every browser, including this one if it is
            them. They are not asked for the old password.
          </p>
        {/if}
      {/if}

      {#if dialogError}
        <p class="text-sm text-danger">{dialogError}</p>
      {/if}
    </form>

    {#snippet footer()}
      <Button variant="ghost" disabled={busy} onclick={close}>Cancel</Button>
      <Button
        variant={current.kind === 'delete' ? 'danger' : 'primary'}
        disabled={!canSubmit}
        onclick={submit}>
        {#if current.kind !== 'delete'}
          <Icon name="check" size={14} />
        {/if}
        {busy
          ? 'Working…'
          : current.kind === 'create'
            ? 'Add user'
            : current.kind === 'password'
              ? 'Set password'
              : 'Delete'}
      </Button>
    {/snippet}
  </Modal>
{/if}
