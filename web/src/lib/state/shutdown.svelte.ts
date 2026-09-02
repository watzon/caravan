/**
 * Safe shutdown for the portable drive (SPEC §2.3, §11).
 *
 * This is the one action whose success is the server disappearing, so it cannot
 * be a toast on a screen that keeps polling. The shell has to be replaced by a
 * terminal "safe to eject" screen, which is why the phase lives here rather
 * than inside the button: the sidebar starts it, and App.svelte renders the
 * ending.
 *
 * The 202 is not the ending. The server writes it before the teardown begins
 * (internal/api/system.go), and everything the eject promise is about happens
 * after: the request drain, the import watcher, the DLNA byebye, the download
 * engine flushing every torrent's resume data back through sqlite,
 * `PRAGMA wal_checkpoint(TRUNCATE)`, the database close, and the fsync'd clean
 * marker (cmd/caravan/serve.go). So the success path promises nothing until the
 * origin has actually stopped answering.
 */

import { ApiError, api, errorText } from '../api/client';

/**
 * idle      — nothing happening, the shell is live.
 * stopping  — the request is out; the server is tearing down and still writing.
 * stopped   — the server is gone. Terminal: nothing here reconnects.
 */
export type ShutdownPhase = 'idle' | 'stopping' | 'stopped';

/** How often the teardown is probed once the 202 is in. */
const POLL_MS = 500;

/**
 * How long to keep probing before giving up and calling it stopped anyway.
 *
 * A ceiling is needed because "unreachable" is the only signal available and a
 * server that hangs on shutdown would otherwise leave the user on a spinner
 * forever. It is generous on purpose: srv.Shutdown alone is allowed 10s, and a
 * rolled-back storage migration copies the library back before the process
 * returns.
 */
const TIMEOUT_MS = 120_000;

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

class ShutdownState {
  phase = $state<ShutdownPhase>('idle');
  /** Why the last attempt failed; only ever set while back in 'idle'. */
  error = $state<string | null>(null);

  /** True while the confirm dialog is up. */
  confirming = $state(false);

  /**
   * True once the wait for the process to exit has run out of patience. The
   * eject screen says so rather than claiming a flush it never saw.
   */
  timedOut = $state(false);

  async run(): Promise<void> {
    this.phase = 'stopping';
    this.error = null;
    this.timedOut = false;
    this.confirming = false;
    try {
      await api.shutdown();
    } catch (err) {
      // A transport failure here is the expected shape of success as often as
      // it is a failure: the server answers 202 and then closes its listener,
      // and a browser that lost the connection mid-reply cannot tell the two
      // apart. Either way the teardown is under way, so it joins the same wait.
      if (!(err instanceof ApiError) || err.status !== 0) {
        // A real answer (401, 429, 503) means the server is alive and refused;
        // going back to idle keeps the app usable and says why.
        this.error = errorText(err);
        this.phase = 'idle';
        return;
      }
    }
    await this.awaitExit();
    this.phase = 'stopped';
  }

  /**
   * Poll until the origin stops answering, which is the only observable proof
   * the process reached the end of its teardown.
   *
   * Any HTTP answer at all — 200, 401, 503 — means the listener is still up and
   * the drive is still being written to. Only a transport failure means gone.
   */
  private async awaitExit(): Promise<void> {
    const deadline = Date.now() + TIMEOUT_MS;
    for (;;) {
      await sleep(POLL_MS);
      try {
        await api.systemStatus();
      } catch (err) {
        if (err instanceof ApiError && err.status === 0) return;
        // Still answering, just not with a 200.
      }
      if (Date.now() >= deadline) {
        this.timedOut = true;
        return;
      }
    }
  }
}

export const shutdown = new ShutdownState();
