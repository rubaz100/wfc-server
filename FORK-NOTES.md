# mkwfc fork notes

This is the **mkwfc** fork of [WiiLink24/wfc-server](https://github.com/WiiLink24/wfc-server),
pinned at upstream commit `f0825bf` and tracked on branch **`mkwfc`**. The `upstream` remote
points at WiiLink24. Rebase onto upstream with `scripts/rebase-upstream.sh`.

## How we diverge from upstream

1. **Database: PostgreSQL → SQLite.** The `database/` package was ported from `pgxpool`
   (pgx/v4) to `database/sql` + `modernc.org/sqlite` (pure Go, no cgo). This removes the
   Postgres install dependency so the server can ship as one self-contained binary.
   - `connection.go` opens a SQLite file and exposes a thin `pool` shim that keeps the
     `QueryRow/Query/Exec(ctx, …)` call shape used across the package.
   - Schema lives in `database/migrations/` (embedded, applied on startup), translated from
     the original Postgres `schema.sql` (kept as `schema.postgres.sql` for reference).
   - `ng_device_id bigint[]` became the junction table `user_ng_device_ids`; the Postgres
     array operators (`&&`, `unnest`) in the ban-overlap queries were rewritten as `EXISTS`
     sub-selects.
   - SAKE `fields jsonb` became `TEXT` (SQLite JSON1); the `||` merge became `json_patch`.

2. **Importable server entrypoint.** The process dispatch in `main.go` was moved into the
   importable package `server` (`server.Run`), so a `mkwfc` wrapper binary can call it.
   `main.go` is now a thin shim calling `server.Run`. This does not yet remove the
   frontend/backend subprocess or RPC listeners; the normal runtime still has two processes.

Pre-existing local additions (admin API, bans/motd endpoints, config-write) were already in
the working tree when this fork branch was created and are carried along unchanged.

## Design notes

- **The privileged-bind helper is long-lived.** `privbind.Bind` tries `BindDirect`
  (plain `net.Listen`) first and only falls back to the osascript helper (`privilegedBind`)
  when that fails with a permission error — i.e. `mkwfc run` *without* sudo trying to bind
  53/80/443. The helper binds the listening sockets and passes them to the parent over
  `SCM_RIGHTS`. On macOS/BSD, closing a listening socket drains and RSTs its accept queue,
  and the parent's passed dup does *not* keep that queue alive — so if the helper closed its
  descriptors right after the hand-off, the parent's sockets would stop delivering data
  (accepted connections read EOF; measured ~55–90% failure). To avoid this, `runHelper`
  blocks after sending the descriptors and holds them open until the parent closes the
  control connection; the parent registers that close (and reaps the helper) on
  `Sockets.CloseAll`, and also triggers it on context cancellation. The helper therefore
  lives for the whole server run. `sudo mkwfc run` uses `BindDirect` and never spawns it.

  Caveat: the osascript GUI-escalation path has not been exercised end-to-end with a real
  console (it needs interactive admin authorization); the mechanism is covered by
  `TestDescriptorPassingAndHighPortDirectBind`, which drives `runHelper` directly.

## Combined release integration (2026-10-31)

Regular builds embed version 0.3.2 and advertise race protocol 2 only when the
matching manifest/assets are present. PAL CPU/auto-fill and Knockout share a
negotiated roster and authoritative owner event stream. Gameplay remains off
by default and experimental; CPU items/Battle/other regions are unfinished.

Startup detects and persists LAN-derived bindings while retaining custom binds,
ports, certificates and signing keys. XML server settings remain durable; shared
Host Manager last-tab/refresh preferences and the existing password hash live
in SQLite. The pending password-auth and Aero panel work is included in this
release alongside port preflight and macOS rebind fixes.

This integration was compiled only. No automated, smoke, browser or Dolphin
checks were run, as requested by the user. Historical test expectations require
revision before automated validation resumes.

## CPU item integration (2026-11-01)

Regular builds embed payload 0.3.3 and require race protocol 3 for CPU/Knockout
negotiation. CPU inventory snapshots use a separate 32-byte owner-authenticated
relay with frozen membership, epoch/sequence, reserved-byte and item bounds
checks. Native owner item AI and EVENT/RACE effects are retained; guest item
state uses bounded received rows rather than two-local-human outgoing buffers.
Inventory is cleared for KO and terminal racers. CPU snapshot publications
pause under GameSpy output backpressure while pending inventory is retained.

Host Manager describes CPU items as experimental and Battle as unsupported.
Only compilation and source review were performed, with no tests as requested.
GPCM inventory versus UDP item-event timing, live effects/collisions and Wii/vWii
hardware behavior remain unverified. An inventory-only stalled stream can keep
the last valid item state while owner heartbeats continue.

## Host Manager live races (2026-11-01)

Payload 0.3.4 adds independent telemetry format 1 for PAL online VS, even with
CPU/Knockout switches off. The Live races tab displays authenticated room
leaderboards and native-world racer markers over the loaded ENPT path graph.
Separate point/edge chunks preserve branches without copying map textures.

GPCM accepts only current room authority on the authenticated active session,
validates bounded snapshots and map chunks, and retains at most 256 rooms for
two minutes after their last report. Epoch/sequence and connection identity
prevent stale stream replacement; missing/disconnected reporters become stale.
The moderator-only Host Manager `/api/races` exposes no addresses or secrets.
The frontend pauses when hidden/logged out and supports manual refresh; Live
races is included in persistent last-tab preferences. Capability metadata
identifies the actual embedded producer independently of CPU protocol 3.

Source review and compilation only; no tests or runtime sessions were run.
Native coordinate/map coverage, repeated races, split-screen identities and
physical hardware still require user validation.

## Course-loading hook correction (2026-11-01, 0.3.5)

The embedded native payload now preserves incoming branch labels around three
CPU call hooks. Captured RAM from the user's failed 0.3.4 session confirms
that the GM loop's initial branch entered the middle of a CTR patch and
recursively called the native Update function until its stack overwrote game
code. Sender and remote-kart skip branches had the same unsafe layout and are
also corrected. All existing CPU/item/Knockout/live-view features remain in
the PAL build; CPU protocol 3 and telemetry format 1 are unchanged.

Native/Go compilation and static branch-entry/assembly review only. No new
autonomous game tests. The diagnostic Dolphin debug/graph profile workaround
was restored; successful course loading and complete races need user validation.

## Race-end transport and spectator corrections (2026-11-01, 0.3.6)

The user's 0.3.5 run reached racing, but the owner later received a fatal GPCM
parse error: its buffered input contained a NUL. An earlier nested `wl:report`
key also showed malformed report framing. The eliminated guest initiated QR2
shutdown before closing GPCM, separately and before the owner's parse failure.

GPCM now drains complete records even when TCP also carries part of the next
record. The 16 KiB limit applies to each logical record, with a bounded pending
tail, rather than a coalesced batch. Malformed starts and NUL bytes remain fatal;
diagnostics identify allowlisted record types and byte offsets without logging
credentials or message contents. The stream correction does not itself prove
where the malformed native output originated.

The embedded payload integrates a bounded complete-report queue and a single
GP publication path, replacing separate field appends that could interleave.
Spectator handling refreshes idle counters only for an eliminated unfinished
local human, preserves Knockout placement/time state through finishing stages,
and includes race-notice corrections. Existing CPU/Knockout protocol 3 and live
telemetry format 1 remain unchanged.

Source review and compilation only. No automated tests or new races were run;
spectator retention, in-race notices, results and live views remain unverified
in gameplay after these corrections.

## Knockout pre-elimination inactivity (2026-11-01, 0.3.7)

The user's 0.3.6 retry logged guest shutdown before the first KO event; it was
never eliminated. The prior spectator-only reset therefore could not prevent
the ordinary AFK kick. The owner remained GPCM-connected and accepted a final
KO event without NUL/parse errors. The user clarified that results appeared,
followed by both clients seeing the peer-disconnect message. Connection
retention through results and return to the room remain unverified.

Payload 0.3.7 intercepts only the native online VS input-idle decision during
the entire negotiated KO race. Ordinary VS inactivity, general disconnects
and the separate hard race limit retain native behavior. GPCM recognizes a
bounded `wl:ko_afk` diagnostic with input slots 0–3; it does not interpret the
record as authority or change server state. Native clients report suppression
once per controller/race through the serialized report queue. Protocol versions
remain CPU/KO 3 and telemetry 1. No automated tests or autonomous races run;
full gameplay/results validation remains with the user.

## Knockout HUD and spectator controls (2026-11-01, 0.3.8)

The user confirmed 0.3.7 works in their current two-Dolphin setup. Payload 0.3.8
adds M4 camera selection and HUD lifecycle polish: D-pad Left/Right cycles
eligible humans/CPUs, Down resumes automatic leader following, and the race
notice shows placement, watched target/mode and controls. It restores native
finish/status text on spectator exit. Camera settings/history refresh only
on target changes, with existing one-camera guards. No server protocol or
native finish/network behavior changes; CPU/KO 3 and telemetry 1 remain.

The native input/camera ABI and font/pane capacity were reviewed from source,
retail assembly and assets. Native/Go compilation only; no automated tests,
rendering checks or autonomous races. New HUD/control behavior remains for
user manual validation; physical hardware and broader room coverage are open.
