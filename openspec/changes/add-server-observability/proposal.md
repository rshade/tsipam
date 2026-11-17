# Proposal: Server observability

## Why

Once `tsipam serve` (from `add-serve-tsnet`) is the shared registry for a
team, people need to see the registry and its drift without running a
command: what is reserved, how full each pool is, and whether the live
tailnet still agrees. Today drift is only visible when someone runs `plan`
or `check`, and pool exhaustion is only discovered when `subnet allocate`
fails.

## What Changes

- **Scheduled drift.** `tsipam serve` gains `--drift-tailnet <alias>`
  (repeatable) and `--drift-interval` (default `15m`, `0` disables). For each
  target it runs the same read-only comparison as `plan` at startup and on
  every interval, and keeps the latest report and run status in memory.
- **Drift report endpoint and command.** A new read-only API method and a
  `tsipam drift` command (remote-only, needs `--server`) return the latest
  scheduled report in the existing comparison envelope, plus a `run` object
  with timestamps and outcome.
- **Prometheus metrics.** `/metrics` on the `svc:tsipam` listener, behind the
  `read` capability. It exposes drift rows by kind and state, run health,
  reservation counts, free site IDs, and per-root-pool utilization and free
  blocks.
- **Read-only web UI.** Pages under `/`, rendered on the server: an
  overview, reservation search across all four kinds, pool usage bars,
  the latest drift report, and reservation history. It is behind the `read`
  capability, uses no JavaScript, and accepts only `GET` and `HEAD`.
- **Server-side sanitization.** The server's sanitize level and key apply to
  the outputs it renders itself: UI pages and metric labels. The drift
  endpoint returns real values, like every other API method, and
  `tsipam drift` sanitizes on the client as every command already does.

## Capabilities

### New Capabilities

- `server-drift`: scheduled registry-versus-live comparison inside
  `tsipam serve`, its retained latest report, and the read-only drift
  endpoint and `tsipam drift` command.
- `server-metrics`: the Prometheus exposition endpoint, its metric families,
  label rules, and authorization.
- `server-ui`: the read-only, server-rendered web interface and its
  authorization and security headers.

### Modified Capabilities

- `schema`: the schema catalog adds the `drift` command and its flags, and
  explains how its exit codes relate to `plan`/`check`.

## Impact

- **Depends on:** `refactor-registry-store`, `add-sqlite-store` (the events
  table backing the history page), and `add-serve-tsnet` (`serve`,
  `--server`, WhoIs identity, the `read` capability, and the server-held
  Tailscale API credentials).
- **Code:** new `internal/usage` (pure pool-utilization arithmetic), new
  `internal/drift` (scheduler and latest-report holder), new
  `internal/metrics`, new `internal/webui` (templates and embedded CSS);
  `design/` and `gen/` (one read method); `cmd/tsipam` (`drift` command and
  `serve` flags).
- **Dependencies:** `github.com/prometheus/client_golang` (Apache-2.0) and
  its transitive modules, checked against the license allowlist.
- **Docs:** a new how-to for monitoring and alerting, plus updates to the
  registry-and-live-state explanation covering scheduled runs.
- **Not touched:** registry format, existing commands' output, and exit
  codes.
