# DLNA media server

Caravan advertises the library on the LAN as a DLNA/UPnP-AV Digital Media Server
(SPEC §5.1, PLAN phase 4 task 2). It is on by default: a smart TV, console or
phone on the same network finds "Caravan" in its media-server list, browses
Movies and TV, and plays files directly.

**Scope: browse and serve.** Files are served exactly as they are on disk, with
byte ranges so seeking works. Nothing is transcoded on the fly. The answer to
"my TV cannot play this file" is the convert-for-TV queue (SPEC §8), which
produces one file every client can decode, not a per-client stream.

## What is exposed

Everything hangs off Caravan's own HTTP listener under `/dlna`, so there is one
port and one firewall rule.

| Path | What |
| --- | --- |
| `/dlna/device.xml` | Device description (SSDP `LOCATION` points here) |
| `/dlna/cds.xml`, `/dlna/cms.xml` | Service descriptions (SCPD) |
| `/dlna/control/cds` | ContentDirectory SOAP control |
| `/dlna/control/cms` | ConnectionManager SOAP control |
| `/dlna/media/{id}.{ext}` | One library file, with `Range` support |

Discovery is SSDP on `239.255.255.250:1900`: `ssdp:alive` notifications on a
timer, `ssdp:byebye` on shutdown, and unicast answers to `M-SEARCH`.

The content tree is `root → Movies / TV → series → seasons → episodes`. Only
files that exist in the library appear; a wanted-but-unowned movie is not a
browsable dead end. Posters are served through the existing
`/api/v1/images/{path}` endpoint as `upnp:albumArtURI`.

### Deliberate omissions

- **No `Search`.** It is optional in ContentDirectory:1. `GetSearchCapabilities`
  returns empty and `Search` returns UPnP error 720, which is consistent; faking
  it would be worse.
- **No GENA eventing.** `eventSubURL` is present and empty, and `SystemUpdateID`
  is pinned. Clients re-browse when the user navigates.
- **No `DLNA.ORG_PN` in `protocolInfo`.** A profile name is a claim about
  bitrate and level that this server cannot verify, and a wrong one makes
  clients refuse files they could play. `DLNA.ORG_OP=01` (byte-seek) is
  advertised, which is the flag that matters.

## Settings

| Key | Default | Meaning |
| --- | --- | --- |
| `dlna_enabled` | `true` (absent reads as on) | Advertise on the LAN |
| `dlna_friendly_name` | `Caravan` | Name shown in a client's device list |
| `dlna_uuid` | generated | Device identity, stable across restarts |

Edited from **Settings → DLNA**. Saving takes effect immediately; no restart.
`GET /api/v1/dlna` reports the toggle *and* whether SSDP actually came up —
they differ on hosts with no usable multicast.

## Manual verification

The automated tests cover the wire format, the content hierarchy, range serving
and a real M-SEARCH round trip over a loopback socket. What they cannot cover is
a specific television, so verify against a reference client:

1. **Discovery.** With Caravan running, on the same L2 network:

   ```
   # Should print a search response with LOCATION pointing at this host.
   gssdp-discover -i <iface> -t urn:schemas-upnp-org:device:MediaServer:1
   ```

   Or check `Settings → DLNA` reads "Advertising".

2. **Device description.** `curl http://<caravan-host>:8677/dlna/device.xml` —
   expect `<friendlyName>`, a `uuid:` UDN and two services.

3. **Browse.** Point a reference control point at it (below) and walk
   root → Movies → an item.

4. **Playback and seeking.** Play a file, then scrub. If playback works but
   scrubbing does not, the `DLNA.ORG_OP=01` flag or the `Range` handling is the
   place to look:

   ```
   curl -r 0-99 -o /dev/null -D - http://<host>:8677/dlna/media/1.mkv
   # expect: 206 Partial Content + Content-Range: bytes 0-99/<size>
   ```

### Reference clients

These are what the implementation is checked against. Per the phase-4 risk note,
client variance is unbounded and Caravan implements the specification rather
than per-TV workarounds — a set that needs a quirk is a bug report, not a patch
to this server.

| Client | Platform | Notes |
| --- | --- | --- |
| **VLC** (Local Network → UPnP) | desktop, mobile | Best first check: strict-ish, verbose logs |
| **Kodi** (UPnP source) | desktop, TV boxes | Exercises browse paging and album art |
| **gupnp-tools** (`gupnp-universal-cp`) | Linux | Raw SOAP; shows faults verbatim |
| **BubbleUPnP** | Android | Common real-world control point |
| **Samsung Tizen / LG webOS** | TV | The acceptance target; browse + play + seek |

## Troubleshooting

- **Not discoverable.** SSDP is multicast and does not cross subnets or most VPN
  interfaces, and Docker's default bridge network blocks it — the container
  needs host networking. `Settings → DLNA` shows the reason.
- **Visible but empty.** The library is empty or nothing has been imported yet;
  DLNA shows files, not wanted items.
- **Plays but will not seek.** Check the `Range` response above. Some clients
  also refuse to seek in Matroska regardless of the server.
- **Wrong address advertised.** `LOCATION` and every `res` URL are built from
  the address the client reached Caravan on, so a multi-homed host advertises
  the interface the kernel routes the discovery group through.
