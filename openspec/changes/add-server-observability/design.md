# Design: Server observability

## Context

See `proposal.md` for motivation. This change builds on three earlier
changes and assumes their shapes:

- `refactor-registry-store`: `registry.Ledger` with `LoadForPlanning` and the
  list methods, running against a `Store`.
- `add-sqlite-store`: the `events` table and the ledger read behind
  `registry history`.
- `add-serve-tsnet`: `tsipam serve` (one replica, SQLite only), the
  `svc:tsipam` HTTPS listener, `WhoIs` identity, the authorization middleware
  for `github.com/rshade/tsipam/cap/ipam` (actions plus optional alias, site,
  and service scopes), `--server`, the `forbidden` error, and server-side live
  reads using the server's `TAILSCALE_TAILNET` and API credentials.

Relevant existing code:

- `ipamsvc.Plan` = `LoadForPlanning`, then `CollectLive`, then
  `planner.Compare`. The report is deterministic, and devices and Services
  are two consecutive API reads (see
  `docs/explanation/registry-and-live-state.md`).
- `cmd/tsipam/calc.go:nextFreeTaken` builds the taken set (reservations plus
  advertised routes) for `calc next-free`. `subnet allocate` uses the same
  rule inside the ledger.
- Sanitization uses `tsacl/pkg/policycore/sanitize`. `Sanitizer.Value(kind,
  value)` returns a field-level placeholder, and a `Sanitizer` is not safe
  for concurrent use. The kind constants (`site`, `cidr`, `tailnet`, ...)
  live privately in `cmd/tsipam/main.go`.

## Goals / Non-Goals

**Goals:**

- Scheduled drift that is provably the same comparison as `plan`.
- Metrics and pages that work with one tailnet grant and no extra
  credentials, ports, or front-end toolchain.
- Any value redacted by the CLI is also redacted on a page or in a metric
  label when the server runs sanitized.

**Non-Goals:**

- Expiring reservations, inbound Tailscale webhooks, outbound
  notifications, Pulumi/Terraform/Kubernetes integrations, and an MCP
  endpoint (tier 2, separate changes).
- Drift history or trend storage. Only the latest report is kept, and
  Prometheus holds the time series.
- Triggering a drift run on demand. `plan --server` already does a live
  comparison.
- Writing to the registry from the UI. Any form, button, or edit flow.
- Shipping alert rules or Grafana dashboards as artifacts. The how-to shows
  example rules only.
- Multiple replicas.

## Decisions

### The latest report lives in memory, not in SQLite

`internal/drift.Holder` keeps, per alias, the last complete report, the
live taken-prefix set from that run, and the last run status, behind an
`RWMutex`. Each target runs at startup, so a restart refills it within one
collection.

*Alternative:* a `drift_reports` table. Rejected. The data is derived and
reproducible, so persisting it adds schema, migrations, and backup volume
for nothing a restart cannot rebuild in seconds. It would also tie the
SQLite schema to the report format.

### One comparison function for `plan` and the scheduler

The body of `ipamsvc.Plan` moves into an unexported `compare(ctx, alias)`
that both the goa method and `drift.Scheduler` call. The scheduler calls it
in-process, not over HTTP, so it never needs a capability of its own. Each
target has a goroutine on a `time.Ticker`. A tick is skipped when that
target's run is still in progress (a `TryLock`). Each run's context is
bounded by `min(interval, 5m)` and cancelled at server shutdown.

Startup checks: `--drift-interval` must be `0` or at least `1m`. If any
target is configured, live credentials must be present, because failing
loudly at startup beats a dashboard full of `failed` runs. The 1-minute
floor bounds API usage: two list calls per target per run.

### Drift endpoint and command

A new goa method `latest_drift` (`GET /tailnet/{tailnet}/ipam/drift`)
returns `ComparisonReport` plus a `DriftRun` type (`started_at`,
`completed_at`, `outcome`, optional `error`). It requires `read` on the
alias. It adds one goa error, `drift_unavailable` (HTTP 503), used for "not
configured" and for "no complete run yet"; the message says which.

`tsipam drift` reuses `planning_output.go` and adds a `run` object to the
envelope's `comparison` plus one human line, for example `Last run: drift at
<time>`. Exit codes: `drift_unavailable`, `forbidden`, and transport errors
exit 2. Any printed report exits 0. Gating stays with `check`, so `drift`
never has to decide whether a stale report is safe to gate on.

`drift` is `read-only` in `leafContracts` and joins the pinned MCP tool set. Its
report is a result, not a verdict, so an agent gets the rows that `check` cannot
return. `drift_unavailable` is also its error code. In an MCP call it uses the
server `TSIPAM_SERVER` names, and a `server` input is refused, as
`add-serve-tsnet` defines.

### Metrics: same listener, unscoped `read`, computed at scrape time

`/metrics` is mounted on the `svc:tsipam` mux before the goa handler. The
authorization middleware requires `read` with alias scope `*`.

*Alternative considered:* a second tailnet port, or a node-local port
outside the Service. Rejected. A Service host must advertise every port the
Service defines, so a new port means changing the Service definition. It
would need a second auth path. And per-instance scraping is moot with one
replica. Reusing the Service listener keeps one name, one TLS certificate,
and one grant model.

Uses `prometheus/client_golang` with a private `prometheus.Registry`. That
holds the Go and process collectors plus one custom `Collector` that, on
each scrape, reads each alias's registry through `Ledger` and the drift
`Holder`. No counters are mutated on the request path, so the exposition
always matches current state.

| Metric | Type | Labels |
| --- | --- | --- |
| `tsipam_reservations` | gauge | `tailnet`, `kind` |
| `tsipam_site_ids_free` | gauge | `tailnet` |
| `tsipam_pool_utilization_ratio` | gauge | `tailnet`, `pool`, `site` |
| `tsipam_pool_free_blocks` | gauge | `tailnet`, `pool`, `site`, `size` |
| `tsipam_drift_rows` | gauge | `tailnet`, `kind`, `state` |
| `tsipam_drift_count` | gauge | `tailnet` |
| `tsipam_drift_last_run_timestamp_seconds` | gauge | `tailnet` |
| `tsipam_drift_last_success_timestamp_seconds` | gauge | `tailnet` |
| `tsipam_drift_last_run_success` | gauge | `tailnet` |
| `tsipam_drift_last_run_duration_seconds` | gauge | `tailnet` |

`--metrics-block-size` is repeatable and defaults to `24` and `64`. A size
applies to a pool only when the pool's prefix length is at most that size
and the size is valid for the pool's family. Cardinality is bounded by root
pools times sizes. The registry is human-curated (hundreds of pools), so
there is no cap. The how-to notes the arithmetic.

### Pool arithmetic is one pure package

New `internal/usage` (no I/O) provides:

- root-pool detection: a reservation contained in no other reservation;
  cross-site containment is already impossible
- utilization: the union of strictly-contained reservations over pool size,
  using `netipx.IPSet`
- free aligned blocks per size: pool minus taken, then counted per
  resulting range with arithmetic, never by enumerating blocks

`cmd/tsipam/calc.go:nextFreeTaken`'s taken-set assembly moves here, so the
CLI, the metrics, and the UI share one definition of "taken". Results are
`float64`, because IPv6 counts exceed `int64`. The tests compare free-block
counts with `ipcalc.NextFree` enumeration on small pools.

### Sanitization on the server

The kind constants and the "which field is which kind" mapping move from
`cmd/tsipam/main.go` to a new `internal/redact` package, used by both the
CLI and the server. The server builds one long-lived `sanitize.Sanitizer`
per alias (Options `Tool: "tsipam"`, `Tailnet: alias`) from its own
`TAILSCALE_SANITIZE`/`TAILSCALE_SANITIZE_KEY`, each behind a mutex because
`Sanitizer` is not concurrency-safe. Pages and metric labels call
`Value(kind, v)` per field, which is exact field mapping and not text
sweeping.

Without a key, UI placeholders are stable for the process's lifetime. For
metrics they would churn on every restart and break series identity, so
per-pool series are dropped and one warning is logged. The drift API
method returns real values; the CLI client sanitizes as it already does.

### UI: server-rendered `html/template` with embedded CSS

New `internal/webui`: `html/template` pages and one stylesheet in
`embed.FS`, served by the same mux. Routes:

- `/`: overview
- `/ui/{tailnet}/reservations`: search, with query parameters `kind`,
  `site`, `service`, `owner`, `ticket`, `q`
- `/ui/{tailnet}/pools`
- `/ui/{tailnet}/drift`
- `/ui/{tailnet}/history?before=<event-id>`
- `/static/`

These sit beside the API's `/tailnet/{tailnet}/ipam/...` paths without
overlap. Usage bars are CSS widths computed on the server. Search filtering
runs on the server against `Ledger` list results. History paging is keyset
paging on the event ID from `add-sqlite-store`.

*Why no JavaScript application:* a JS toolchain would add Node and a bundler
to the release build, new lint and vulnerability surfaces, and a second
language for a read-only, list-shaped view. `html/template` escapes by
context, and `script-src 'none'` becomes enforceable. If interactivity is
ever needed, it can be added as progressive enhancement.

Headers on every page:

- `Content-Security-Policy: default-src 'none'; style-src 'self'; img-src
  'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`
- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: no-referrer`

The search form is a `GET` form, which `form-action 'self'` allows.

Scoped readers: the middleware passes the caller's grant to the handler.
The overview lists only aliases with `read`. Rows are filtered by site and
service scopes using the same scope evaluation the API uses (from
`add-serve-tsnet`).

### Assumptions recorded

- The server has exactly one live tailnet (its `TAILSCALE_TAILNET`), so a
  drift target is just a registry alias. If `add-serve-tsnet` later allows
  several live tailnets, `--drift-tailnet` can grow an `=<live>` suffix
  compatibly.
- `add-serve-tsnet` exposes scope evaluation as a reusable function, not
  only as goa middleware. If it does not, task 4.1 extracts it.
- The `Store` can enumerate aliases. If `add-serve-tsnet` or
  `add-sqlite-store` does not already add `Ledger.Tailnets`, task 1.3 adds
  it to the contract and the conformance suite.

## Risks / Trade-offs

- [Risk] Drift from one run caused by the consecutive-read window pages
  someone. → Mitigation: the documented alert uses `for:` of at least two
  intervals, and the `run` object carries both timestamps.
- [Risk] Scheduled runs add API load and rate-limit exposure. → Mitigation:
  a 1-minute floor, a 15-minute default, two calls per target per run, and
  no overlapping runs.
- [Risk] Identity values leak through metric labels. → Mitigation: labels
  sanitize with the server's level, and per-pool series require a key.
  Unsanitized labels are an informed default: scraping already requires
  unscoped `read`.
- [Risk] A scrape reads every alias's registry and can contend with
  writers. → Mitigation: SQLite in WAL mode lets reads run alongside
  writes, and the registry is small.
- [Risk] The license allowlist rejects a transitive module of
  `client_golang`, for example `klauspost/compress` with its mixed
  licenses. → Mitigation: task 2.1 runs `mise run license-check` before any
  code depends on it. The fallback is a hand-written text-format encoder
  for these ten families.
- [Trade-off] In-memory reports mean no drift data until the first run after
  a restart. This is accepted, and is visible as `drift_unavailable` and
  through the absent drift-row series.

## Migration Plan

Additive. `serve` without the new flags behaves as before, except that
`/metrics` and the UI routes now exist behind `read`. Rollback is a revert;
no stored data is involved.

## Open Questions

- Should `tsipam_reservations` also break down by site? Deferred until a
  dashboard needs it, because it multiplies cardinality by the number of
  sites.
