# Usenet: the built-in engine

Caravan downloads NZBs itself. There is no SABnzbd, no NZBGet, and nothing to
install alongside it — the same way there is no external torrent client
(SPEC §5.1, PLAN phase 7).

**Neither protocol requires an external download client.** Torrents go to the
built-in BitTorrent engine, NZBs go to the built-in Usenet engine, and both are
the default with nothing configured. An external client is a choice for people
who already run one, not a dependency; see
[docs/download-clients.md](download-clients.md) if you want one.

What a Usenet download does need is a **news server**, because that is where the
articles live. That is the only thing to configure.

---

## Configuring a news server

**Settings → Usenet servers → Add server.**

| Field | What it is |
| --- | --- |
| Name | Your label for it. It is what error messages call this server. |
| Host | The provider's hostname, e.g. `news.example.com`. |
| Port | Blank uses `563` with TLS on, `119` with it off. |
| TLS | On by default, and worth leaving on: it is what keeps your password off the wire. |
| Username / Password | Blank means the server is used anonymously and no `AUTHINFO` is sent. |
| Max connections | The provider's connection limit. Blank uses 8. |
| Priority | Failover order, lowest first. See below. |
| Enabled | Off keeps the configuration without using the server. |

**Test** dials the server, completes the TLS handshake and authenticates. It
does not fetch an article — there is no message-id every provider is guaranteed
to carry, and a missing one would report a working server as broken.

Your password is stored in the database and never comes back out: the API
returns `has_password: true` and nothing else, it never appears in a log line or
an error message (SPEC §12). Saving with the password field left blank keeps the
stored one; clearing it explicitly removes it.

Changes take effect on the next queue poll — a couple of seconds — with no
restart. Downloads already in flight keep using the connections they had; the
old ones are closed once their articles have finished arriving.

### Backup servers and failover

Priority is the failover order: **lowest number is tried first**.

A backup provider is asked only for the articles the servers above it could not
supply. That is the point of the ordering, and it is what makes a cheap block
account a sensible second server — you pay it only for what your main provider
has lost.

Failover is per article, not per download, and it happens in two situations:

- **The article is not there.** Every server that says "no such article" is
  crossed off, and the next one is asked.
- **The article arrived damaged.** A copy that fails its own yEnc checksum is
  re-requested from the *next* server down, never the same one — asking again
  gets the same bad bytes. A backup's copy of a damaged article is frequently
  clean, and finding it there costs nothing, while repairing it spends recovery
  blocks.

A server that merely drops a connection is retried a few times on the same
server before the next one is tried, with a capped backoff. Moving a whole
download onto a paid block account because of one dropped socket would be an
expensive reflex.

---

## What a download actually does

A Usenet download is three jobs, not one. The queue shows which one is running
as a badge beside the state.

### 1. Downloading

The NZB is fetched from the indexer and parsed, then its articles are fetched in
parallel, yEnc-decoded, and written straight to their offset in the target file.
Parts arrive in whatever order the servers answer; yEnc says where each one
goes, so nothing has to be buffered.

Progress is measured in **on-the-wire bytes** — the encoded size, which is what
your provider counts against your quota, and the only total that is known before
the first article is decoded.

**An article that cannot be had is not a failure.** It becomes a hole and the
download carries on. Usenet articles rot, and par2 exists to fill exactly these
holes; abandoning a fifteen-gigabyte release over one dead article would throw
away the case Usenet is built around.

Before it starts, the engine checks there is room for what is left to fetch plus
64 MiB of headroom. Resuming only counts what is still missing, so a download
that is nearly finished is not refused for space its own parts already occupy.

### 2. Repairing

**Only when it is needed.** par2 recovery volumes are a repair budget, not
payload: a release that arrived intact never pays to download them (SPEC §5.1).
When the download finished with holes, the engine fetches the volumes, verifies
every file against the set, and rebuilds the damaged blocks.

It also runs a second time in one specific case: when unpacking fails on an
archive that looked complete. That is a poster's own bad bytes rather than a
lost article — yEnc's per-article checksum cannot see it — and it is exactly
what the recovery blocks are for. Repair runs at most once per download, so a
release that is simply broken fails rather than looping.

### 3. Extracting

Any `.rar` or `.zip` set in the download directory is unpacked, and the archive
volumes and par2 files are removed once what came out of them has been verified.
A release posted as plain files is not an error — there is simply nothing to
unpack, and the files are already where the import expects them.

Extraction refuses to write outside the download directory: entries naming `..`,
absolute paths or symlinks reject the whole archive rather than being skipped.
An archive that tries that is not one to extract the rest of.

### Then: import

Completion flows through **the same import path as every other engine**
(`internal/library/importdownload.go`). There is no separate Usenet import.

The file is parsed, matched against what the grab was for, and hardlinked or
moved into the library under its proper name. If the release was obfuscated
badly enough that the filename cannot be matched to anything, it lands in the
**stuck-import queue** for a manual match rather than being guessed at — that
queue is the designed fallback, not a failure.

---

## Failure modes, and what the reasons mean

A failed download stays in the queue with its data on disk. **Resume is also
retry**: it clears the failure and starts the stages again, reusing everything
already downloaded.

| What the queue says | What happened | What to do |
| --- | --- | --- |
| `N of M articles could not be fetched from any news server` | No server could be reached, or none gave a straight answer. This is a transport problem, not a rot problem — the articles may well still be there. | Check the server under Settings → Usenet servers, then resume. |
| `unrepairable: N article(s) are missing or damaged, which costs X recovery blocks, and the release carries only Y — Z short` | Real rot, past what the poster's par2 set can rebuild. The numbers are the whole answer: `Z` more recovery blocks would have saved it. | Nothing will fix this release. Add a backup provider with better retention and grab it again, or grab a different release. |
| `N article(s) are missing or damaged and the release posted no par2 recovery files` | The release has holes and shipped no repair budget at all. | Same: grab a different release. A poster who omits par2 has left you no options. |
| `N article(s) are missing or damaged in files the release's par2 set does not cover` | The damage is in a file the par2 set does not protect. | Grab a different release. |
| `none of the release's par2 recovery volumes could be downloaded` | The volumes have rotted along with the content. | Grab a different release. |
| `not enough free disk space: N bytes needed, M free on /path` | The preflight refused before writing anything. | Free space and resume. |
| `the release's archive is password protected` | Encrypted archive. Terminal — there is no password to try. | Grab a different release. |
| `unpacking the release failed: …` | The archives are damaged past repair, or truncated. The failing volume and entry are named. | Resume once in case it was transient; otherwise grab a different release. |
| `no news server is configured` | Nothing to fetch from. | Add one under Settings → Usenet servers. |

Two distinctions are worth internalising, because they decide what to do next:

- **"Could not be fetched" is not "missing."** The first is your provider or
  your network; the second is retention. Only the second is par2's problem.
- **The deficit number is the useful one.** "4 short" tells you a better-stocked
  provider would have made the difference; "40 short" tells you the release is
  gone.

---

## Pausing, resuming and removing

**Pause** stops the transfer and keeps everything already written. Completed
articles are recorded in a small sidecar inside the download's own directory,
not in the database — the database is a disposable cache, and refetching a
half-finished download because a cache was deleted is a bill you should not have
to pay.

**Resume** picks up from there. So does a restart: the NZB is kept beside the
data, so Caravan comes back knowing the whole plan without re-grabbing.

**Remove** drops the download and asks separately about the data. Removing a
download never touches the library — an imported file is a hardlink or a move
away from the download data (SPEC §13).

---

## Where things live

Everything is under the storage root, so an import is a hardlink or a rename
rather than a copy (SPEC §1.2 pillar 3):

```
<storage root>/
  incomplete/
    .caravan/<handle>.nzb        the download's plan, for resuming
    <release title>/             the download's own directory
      .caravan-segments.json     which articles are already on disk
      <files being assembled>
  library/
    Movies/…                     where the import lands
```

The download directory is named after the release. Two live downloads that
share a title get separate directories — one directory for both would mix their
files and their resume state.

---

## Routing, if you also run an external client

**Settings → Download clients → Routing** picks which engine each protocol goes
to. Both default to their built-in engine.

Picking an external client as the Usenet default does not retire the built-in
engine: it stays in the routing table without a protocol, so the downloads it is
still holding remain listable, pausable and removable, and they still reach the
import watcher. New grabs go to the client you picked. The same is true in
reverse for torrents.

---

## Limits worth knowing

- **No per-download or global speed limit for Usenet.** The per-server
  connection cap is the throttle. The rate limits under Settings → Downloads
  apply to the torrent engine.
- **No download queue slots.** Every added release starts immediately; the
  per-server connection caps are what bound the load on your provider.
- **Nested archives are not recursed into**, and archives inside subdirectories
  are ignored. Top-level sets only.
- **Spanned zips (`.z01`, `.z02`) are not supported.** A `.zip` that is part of
  one fails loudly rather than being mistaken for a rar volume set.
- **Repair is single-threaded.** A badly damaged multi-gigabyte release takes a
  while; a lightly damaged one is quick, which is the common case.
- **Extracted files get fixed `0644`/`0755` permissions.** Archive permission
  bits are ignored.
