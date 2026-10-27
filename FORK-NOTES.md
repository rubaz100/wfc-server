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

## Known issues

- **Privileged-bind escalation path is broken on macOS.** `privbind.Bind` tries
  `BindDirect` (plain `net.Listen`) first and only falls back to the osascript helper
  (`privilegedBind`) when that fails with a permission error — i.e. `mkwfc run` *without*
  sudo trying to bind 53/80/443. In that fallback, the helper binds the listening sockets,
  passes them to the parent over `SCM_RIGHTS`, then closes them. On macOS/BSD, closing a
  listening socket drains and RSTs its accept queue, and the parent's passed dup does not
  keep that queue alive, so connections accepted afterward lose data and read EOF (verified:
  helper holding the fds open → 0% failure; helper closing them → ~55–90%). `sudo mkwfc run`
  uses `BindDirect` and is unaffected. Fix: keep the privileged helper alive holding the
  descriptors for the server's lifetime, closing them only when the parent closes the control
  socket. `TestDescriptorPassingAndHighPortDirectBind` is `t.Skip`-ped until then.
